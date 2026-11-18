package backup

import (
	"context"
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

// Select resolves the user's volume selection against the discovery result.
func (d Discovery) Select(requested []string) ([]DataSource, error) {
	return SelectVolumeSources(d.Sources, requested)
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
		consumers, err := backend.VolumeConsumers(ctx, d.Target, src.Name)
		if err != nil {
			return ConsumerReport{}, err
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

// DescribeSource is the shared Korean label for selection screens and plans.
func DescribeSource(src DataSource) string {
	parts := []string{src.Key + " → " + src.Name}
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
