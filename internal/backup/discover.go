package backup

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// Discovery is the reusable read-only data-source discovery result. Ticket #17
// consumes the same sources (including bind mounts) and the runtime states.
type Discovery struct {
	ProjectName string
	Target      core.Target
	Sources     []DataSource
	Runtime     []data.ServiceRuntime
	Running     []string
}

// ConsumerReport separates services of the selected stack from containers
// that belong to another stack or cannot be safely stopped.
type ConsumerReport struct {
	Services []string
	Outside  []data.ContainerConsumer
}

// Discover reads the stack's data sources and observed service states. It
// never writes state and never stops anything.
func Discover(ctx context.Context, backend data.Backend, target core.Target, projectName string) (Discovery, error) {
	sources, err := backend.Sources(ctx, target)
	if err != nil {
		return Discovery{}, err
	}
	baseDir := target.ProjectDir
	if len(target.Files) > 0 {
		baseDir = filepath.Dir(target.Files[0])
	}
	for i := range sources {
		if sources[i].Kind == data.KindBind {
			sources[i] = NormalizeBindSource(sources[i], baseDir)
		}
	}
	sources = mergeBindSources(sources)
	runtime, err := backend.ServiceRuntime(ctx, target)
	if err != nil {
		return Discovery{}, err
	}
	d := Discovery{ProjectName: projectName, Target: target, Sources: sources, Runtime: runtime}
	for _, svc := range runtime {
		if svc.State == "running" || svc.State == "restarting" {
			d.Running = append(d.Running, svc.Service)
		}
	}
	sort.Strings(d.Running)
	return d, nil
}

// mergeBindSources unifies bind spellings that resolve to the same host
// folder: one selectable folder, one archive, and consumer queries for every
// declared spelling. Volume sources keep their order and identity.
func mergeBindSources(sources []DataSource) []DataSource {
	byPath := map[string]int{}
	out := make([]DataSource, 0, len(sources))
	for _, src := range sources {
		if src.Kind != data.KindBind {
			out = append(out, src)
			continue
		}
		key := "bind\x00" + src.Name
		if index, ok := byPath[key]; ok {
			merged := &out[index]
			merged.Services = mergeUnique(merged.Services, src.Services...)
			merged.MountTargets = mergeUnique(merged.MountTargets, src.MountTargets...)
			merged.DeclaredPaths = mergeUnique(merged.DeclaredPaths, src.DeclaredPaths...)
			merged.ReadOnly = merged.ReadOnly && src.ReadOnly
			continue
		}
		byPath[key] = len(out)
		out = append(out, src)
	}
	return out
}

func mergeUnique(values []string, extra ...string) []string {
	for _, value := range extra {
		if value != "" && !contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}

// Select resolves the user's volume and bind selection against the discovery
// result. Bind references are resolved relative to the first Compose file.
func (d Discovery) Select(volumes, binds []string) ([]DataSource, error) {
	baseDir := d.Target.ProjectDir
	if len(d.Target.Files) > 0 {
		baseDir = filepath.Dir(d.Target.Files[0])
	}
	return SelectSources(d.Sources, volumes, binds, baseDir)
}

// AffectedServices is the union of the Compose services configured to mount
// the selected sources.
func (d Discovery) AffectedServices(sources []DataSource) []string {
	seen := map[string]bool{}
	var out []string
	for _, src := range sources {
		for _, svc := range src.Services {
			if svc != "" && !seen[svc] {
				seen[svc] = true
				out = append(out, svc)
			}
		}
	}
	sort.Strings(out)
	return out
}

// RunningAmong returns the affected services that were running (or
// restarting) before the operation. Already-stopped services are never added.
func (d Discovery) RunningAmong(services []string) []string {
	state := map[string]string{}
	for _, svc := range d.Runtime {
		state[svc.Service] = svc.State
	}
	var out []string
	for _, svc := range services {
		switch state[svc] {
		case "running", "restarting":
			out = append(out, svc)
		}
	}
	return out
}

// ServicesInState returns affected services observed in one state.
func (d Discovery) ServicesInState(services []string, want string) []string {
	state := map[string]string{}
	for _, svc := range d.Runtime {
		state[svc.Service] = svc.State
	}
	var out []string
	for _, svc := range services {
		if state[svc] == want {
			out = append(out, svc)
		}
	}
	return out
}

// CheckConsumers verifies that every container referencing the selected
// volumes belongs to this stack and to a configured service. A stopped
// container still counts: it holds the volume reference. Anything else is
// reported as Outside so the caller refuses without stopping it.
func (d Discovery) CheckConsumers(ctx context.Context, backend data.Backend, sources []DataSource) (ConsumerReport, error) {
	report := ConsumerReport{}
	seenService := map[string]bool{}
	seenOutside := map[string]bool{}
	for _, src := range sources {
		var consumers []data.ContainerConsumer
		if src.Kind == data.KindBind {
			seenMount := map[string]bool{}
			spellings := append([]string{src.Name, src.Declared}, src.DeclaredPaths...)
			for _, hostPath := range spellings {
				if hostPath == "" || seenMount[hostPath] {
					continue
				}
				seenMount[hostPath] = true
				got, err := backend.BindConsumers(ctx, d.Target, hostPath)
				if err != nil {
					return ConsumerReport{}, err
				}
				consumers = append(consumers, got...)
			}
		} else {
			got, err := backend.VolumeConsumers(ctx, d.Target, src.Name)
			if err != nil {
				return ConsumerReport{}, err
			}
			consumers = got
		}
		allowed := map[string]bool{}
		for _, svc := range src.Services {
			allowed[svc] = true
		}
		for _, consumer := range consumers {
			ours := consumer.Project == d.ProjectName && d.ProjectName != "" &&
				consumer.Service != "" && allowed[consumer.Service]
			if ours {
				if !seenService[consumer.Service] {
					seenService[consumer.Service] = true
					report.Services = append(report.Services, consumer.Service)
				}
				continue
			}
			key := consumer.Name + "\x00" + consumer.Project
			if !seenOutside[key] {
				seenOutside[key] = true
				report.Outside = append(report.Outside, consumer)
			}
		}
	}
	sort.Strings(report.Services)
	sort.Slice(report.Outside, func(i, j int) bool {
		if report.Outside[i].Project != report.Outside[j].Project {
			return report.Outside[i].Project < report.Outside[j].Project
		}
		return report.Outside[i].Name < report.Outside[j].Name
	})
	return report, nil
}

// OutsideConsumers is the read-only shared-use probe used by selection
// previews. It returns only containers that do not belong to this stack's
// configured consumers; Run refuses them before stopping anything.
func (d Discovery) OutsideConsumers(ctx context.Context, backend data.Backend, src DataSource) ([]data.ContainerConsumer, error) {
	report, err := d.CheckConsumers(ctx, backend, []DataSource{src})
	if err != nil {
		return nil, err
	}
	return report.Outside, nil
}

// DescribeSource is the shared Korean label for selection screens and plans.
func DescribeSource(src DataSource) string {
	var parts []string
	if src.Kind == data.KindBind {
		label := "폴더 " + src.Name
		if src.Declared != "" && src.Declared != src.Name {
			label += " (선언 " + src.Declared + ")"
		}
		parts = append(parts, label)
	} else {
		parts = append(parts, src.Key+" → "+src.Name)
	}
	if len(src.MountTargets) > 0 {
		parts = append(parts, "마운트 "+strings.Join(src.MountTargets, ", "))
	}
	if len(src.Services) > 0 {
		parts = append(parts, "서비스 "+strings.Join(src.Services, ", "))
	}
	if src.External {
		parts = append(parts, "external")
	}
	if src.ReadOnly {
		parts = append(parts, "읽기 전용")
	}
	return strings.Join(parts, " · ")
}
