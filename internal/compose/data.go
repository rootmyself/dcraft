package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/execx"
)

// ArchiveDataFile is the data payload name inside one backup artifact.
const ArchiveDataFile = data.ArchiveDataFile

type dataConfigDoc struct {
	Name     string                       `json:"name"`
	Services map[string]dataConfigService `json:"services"`
	Volumes  map[string]dataConfigVolume  `json:"volumes"`
}

type dataConfigService struct {
	Volumes []dataConfigMount `json:"volumes"`
}

type dataConfigMount struct {
	Type     string          `json:"type"`
	Source   string          `json:"source"`
	Target   string          `json:"target"`
	ReadOnly bool            `json:"read_only"`
	Volume   json.RawMessage `json:"volume"`
}

type dataConfigVolume struct {
	Name     string          `json:"name"`
	External json.RawMessage `json:"external"`
}

// IsExternal accepts both the boolean form and the legacy object form that
// Compose has emitted for external volumes.
func (v dataConfigVolume) IsExternal() bool {
	raw := strings.TrimSpace(string(v.External))
	switch raw {
	case "", "null", "false":
		return false
	case "true":
		return true
	default:
		var obj struct {
			Name string `json:"name"`
		}
		return json.Unmarshal(v.External, &obj) == nil
	}
}

// Redact implements data.Backend. When no secret runtime is wired it is the
// identity function; the backup feature still routes every helper output
// through it so the boundary does not depend on a particular wiring.
func (c *Compose) Redact(ctx context.Context, t core.Target, text string) string {
	if c.secrets == nil || text == "" {
		return text
	}
	return c.secrets.RedactText(t, text)
}

func (c *Compose) redactError(ctx context.Context, t core.Target, err error) error {
	if err == nil {
		return nil
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		for i := range ce.Diagnostics {
			ce.Diagnostics[i].Detail = c.Redact(ctx, t, ce.Diagnostics[i].Detail)
			ce.Diagnostics[i].Message = c.Redact(ctx, t, ce.Diagnostics[i].Message)
		}
	}
	return err
}

// ProjectName implements data.Backend. Compose's config document always
// carries the effective name, including the folder default.
func (c *Compose) ProjectName(ctx context.Context, t core.Target) (string, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil || !res.Succeeded() {
		return "", c.redactError(ctx, t, core.FailCause(err, classifyConfigFailure(res, err)))
	}
	var doc struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return "", core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectOutput,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgInspectOutputAction},
		})
	}
	return strings.TrimSpace(doc.Name), nil
}

// Sources implements data.Backend. It resolves the Compose volume key to the
// actual engine volume name; it never guesses from the key. Anonymous volumes
// and bind mounts are reported so a later ticket can handle them, but ticket
// #16 only archives named volumes.
func (c *Compose) Sources(ctx context.Context, t core.Target) ([]data.DataSource, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return nil, c.redactError(ctx, t, core.FailCause(err, classifyConfigFailure(res, err)))
	}
	var doc dataConfigDoc
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectOutput,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgInspectOutputAction},
		})
	}

	type entry struct {
		source data.DataSource
	}
	byKey := map[string]*entry{}
	get := func(key string, init data.DataSource) *entry {
		if e, ok := byKey[key]; ok {
			return e
		}
		e := &entry{source: init}
		byKey[key] = e
		return e
	}

	var serviceNames []string
	for name := range doc.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, service := range serviceNames {
		mounts := doc.Services[service].Volumes
		for _, mount := range mounts {
			switch mount.Type {
			case "volume":
				if mount.Source != "" {
					declared := doc.Volumes[mount.Source]
					actual := strings.TrimSpace(declared.Name)
					if actual == "" {
						actual = mount.Source
					}
					key := "volume\x00" + mount.Source
					e := get(key, data.DataSource{
						Kind:     data.KindVolume,
						Key:      mount.Source,
						Name:     actual,
						External: declared.IsExternal(),
						ReadOnly: true,
					})
					e.source.Services = appendUnique(e.source.Services, service)
					e.source.MountTargets = appendUnique(e.source.MountTargets, mount.Target)
					e.source.ReadOnly = e.source.ReadOnly && mount.ReadOnly
					continue
				}
				key := "anonymous\x00" + service + "\x00" + mount.Target
				e := get(key, data.DataSource{
					Kind:      data.KindVolume,
					Anonymous: true,
					ReadOnly:  mount.ReadOnly,
				})
				e.source.Services = appendUnique(e.source.Services, service)
				e.source.MountTargets = appendUnique(e.source.MountTargets, mount.Target)
			case "bind":
				hostPath := strings.TrimSpace(mount.Source)
				if hostPath == "" {
					continue
				}
				e := get("bind\x00"+hostPath, data.DataSource{
					Kind:     data.KindBind,
					Name:     hostPath,
					ReadOnly: true,
				})
				e.source.Services = appendUnique(e.source.Services, service)
				e.source.MountTargets = appendUnique(e.source.MountTargets, mount.Target)
				e.source.ReadOnly = e.source.ReadOnly && mount.ReadOnly
			}
		}
	}

	out := make([]data.DataSource, 0, len(byKey))
	for _, e := range byKey {
		sort.Strings(e.source.Services)
		sort.Strings(e.source.MountTargets)
		out = append(out, e.source)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ServiceRuntime implements data.Backend. It reads the project's containers
// including stopped ones, so the caller can capture the running set before it
// stops consumers.
func (c *Compose) ServiceRuntime(ctx context.Context, t core.Target) ([]data.ServiceRuntime, error) {
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "ps", "--all", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || !res.Succeeded() {
		return nil, c.redactError(ctx, t, core.FailCause(err, classifyPSFailure(res, err)))
	}
	type row struct {
		Service  string `json:"Service"`
		State    string `json:"State"`
		ID       string `json:"ID"`
		ExitCode int    `json:"ExitCode"`
	}
	var out []data.ServiceRuntime
	trimmed := strings.TrimSpace(string(res.Stdout))
	if trimmed == "" || trimmed == "null" {
		return out, nil
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "null" {
			continue
		}
		var rec row
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryInternal,
				Severity:    core.SeverityError,
				Message:     msgInspectOutput,
				Detail:      summarize(err.Error()),
				NextActions: []string{msgInspectOutputAction},
			})
		}
		out = append(out, data.ServiceRuntime{
			Service: rec.Service, State: rec.State, ID: rec.ID, ExitCode: rec.ExitCode,
		})
	}
	return out, nil
}

// VolumeConsumers implements data.Backend with `docker ps -a`: stopped
// containers still reference the volume and must be considered. The query
// uses the actual engine name, never the Compose key.
func (c *Compose) VolumeConsumers(ctx context.Context, t core.Target, volumeName string) ([]data.ContainerConsumer, error) {
	format := "{{.Names}}\t{{.Label \"com.docker.compose.project\"}}\t{{.Label \"com.docker.compose.service\"}}\t{{.State}}"
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: []string{"ps", "-a", "--filter", "volume=" + volumeName, "--format", format},
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if !res.Succeeded() {
		return nil, c.redactError(ctx, t, core.FailCause(nil, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgConsumerListFailed,
			Detail:      summarize(res.Combined()),
			NextActions: []string{msgConsumerListFailedAction},
		}))
	}
	var out []data.ContainerConsumer
	for _, line := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		for len(fields) < 4 {
			fields = append(fields, "")
		}
		out = append(out, data.ContainerConsumer{
			Name: fields[0], Project: fields[1], Service: fields[2], State: fields[3],
		})
	}
	return out, nil
}

// StopServices implements data.Backend. It stops only the named services.
func (c *Compose) StopServices(ctx context.Context, t core.Target, services []string) (data.OpResult, error) {
	if len(services) == 0 {
		return data.OpResult{}, nil
	}
	res, err := c.lifecycleRun(ctx, t, "stop", append(append(t.ComposeArgs(), "stop"), services...))
	return data.OpResult{Output: res.Output}, err
}

// StartServices implements data.Backend. Compose `start` keeps container
// identities and starts only the named services; it never creates or
// reconfigures containers.
func (c *Compose) StartServices(ctx context.Context, t core.Target, services []string) (data.OpResult, error) {
	if len(services) == 0 {
		return data.OpResult{}, nil
	}
	res, err := c.lifecycleRun(ctx, t, "start", append(append(t.ComposeArgs(), "start"), services...))
	return data.OpResult{Output: res.Output}, err
}

// HelperImage implements data.Backend.
func (c *Compose) HelperImage() string {
	if image := strings.TrimSpace(c.getenv("DCRAFT_BACKUP_IMAGE")); image != "" {
		return image
	}
	return data.VolumeHelperImage
}

// ArchiveVolume implements data.Backend. The helper mounts the source
// read-only and writes the archive into a host directory. It runs as root so
// uid/gid and modes are stored faithfully. A live writer is not frozen by the
// read-only mount; the caller must stop consumers first.
func (c *Compose) ArchiveVolume(ctx context.Context, volumeName, hostDir string) (data.OpResult, error) {
	name := "dcraft-backup-" + randomSuffix()
	args := []string{
		"run", "--rm", "--name", name,
		"-v", volumeName + ":/data:ro",
		"-v", hostDir + ":/backup",
		"--entrypoint", "tar",
		c.HelperImage(),
		"-C", "/data", "-czf", "/backup/" + data.ArchiveDataFile, ".",
	}
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: args})
	if ctxErr := ctx.Err(); ctxErr != nil {
		c.killHelper(name)
		return data.OpResult{Output: summarize(res.Combined())}, ctxErr
	}
	out := data.OpResult{Output: summarize(res.Combined())}
	if err != nil {
		return out, err
	}
	if !res.Succeeded() {
		return out, fmt.Errorf("volume archive helper exited with status %d", res.ExitCode)
	}
	return out, nil
}

func (c *Compose) killHelper(name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 15*time.Second)
	defer cancel()
	_, _ = c.run(ctx, execx.Command{Name: "docker", Args: []string{"kill", name}})
}

func randomSuffix() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
