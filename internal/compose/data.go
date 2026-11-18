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
					Kind:          data.KindBind,
					Name:          hostPath,
					Declared:      hostPath,
					DeclaredPaths: []string{hostPath},
					ReadOnly:      true,
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
	return c.consumersForFilter(ctx, t, "volume="+volumeName)
}

// BindConsumers implements data.Backend. Docker records the bind source in two
// spellings: the host path in container inspect on Docker Desktop, and a
// Docker-visible path such as /host_mnt<host path> for the `volume` ps filter.
// Both are queried and the results are merged, so a stopped consumer is never
// missed because of the platform mapping.
func (c *Compose) BindConsumers(ctx context.Context, t core.Target, hostPath string) ([]data.ContainerConsumer, error) {
	hostPath = strings.TrimSpace(hostPath)
	if hostPath == "" {
		return nil, nil
	}
	candidates := []string{hostPath}
	if strings.HasPrefix(hostPath, "/") && !strings.HasPrefix(hostPath, "/host_mnt/") {
		candidates = append(candidates, "/host_mnt"+hostPath)
	}
	if strings.HasPrefix(hostPath, "/host_mnt/") {
		candidates = append(candidates, strings.TrimPrefix(hostPath, "/host_mnt"))
	}
	seen := map[string]bool{}
	var out []data.ContainerConsumer
	for _, candidate := range candidates {
		consumers, err := c.consumersForFilter(ctx, t, "volume="+candidate)
		if err != nil {
			return nil, err
		}
		for _, consumer := range consumers {
			key := consumer.Name + "\x00" + consumer.Project + "\x00" + consumer.Service
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, consumer)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// consumersForFilter runs one `docker ps -a --filter <filter>` query and parses
// the tab-separated name/project/service/state records.
func (c *Compose) consumersForFilter(ctx context.Context, t core.Target, filter string) ([]data.ContainerConsumer, error) {
	format := "{{.Names}}\t{{.Label \"com.docker.compose.project\"}}\t{{.Label \"com.docker.compose.service\"}}\t{{.State}}"
	res, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: []string{"ps", "-a", "--filter", filter, "--format", format},
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
//
// The helper lifetime is explicit: a unique name is chosen before anything
// starts, `docker create` makes the container, `docker start -a` runs it, and
// every exit path (success, error, cancellation, deadline) removes it by name
// with a bounded retry loop. The container is never left to the fate of the
// killed CLI child; the caller holds the stack lock until this method returns.
func (c *Compose) ArchiveVolume(ctx context.Context, volumeName, hostDir string) (data.OpResult, error) {
	name := "dcraft-backup-" + randomSuffix()
	createArgs := []string{
		"create", "--name", name,
		"--label", "dcraft.backup=1",
		"--label", "dcraft.backup.volume=" + volumeName,
		"-v", volumeName + ":/data:ro",
		"-v", hostDir + ":/backup",
		"--entrypoint", "tar",
		c.HelperImage(),
		"-C", "/data", "-czf", "/backup/" + data.ArchiveDataFile, ".",
	}
	createRes, createErr := c.run(ctx, execx.Command{Name: "docker", Args: createArgs})
	output := strings.TrimSpace(createRes.Combined())
	if ctxErr := ctx.Err(); ctxErr != nil {
		return data.OpResult{Output: output}, c.finishHelper(name, ctxErr, output, true)
	}
	if createErr != nil {
		return data.OpResult{Output: output}, c.finishHelper(name, createErr, output, true)
	}
	if !createRes.Succeeded() {
		return data.OpResult{Output: output}, c.finishHelper(name,
			fmt.Errorf("volume archive helper could not be created: status %d", createRes.ExitCode), output, true)
	}

	startRes, startErr := c.run(ctx, execx.Command{Name: "docker", Args: []string{"start", "-a", name}})
	startOutput := strings.TrimSpace(startRes.Combined())
	if output != "" && startOutput != "" {
		output += "\n"
	}
	output += startOutput
	if ctxErr := ctx.Err(); ctxErr != nil {
		return data.OpResult{Output: output}, c.finishHelper(name, ctxErr, output, false)
	}
	if startErr != nil {
		return data.OpResult{Output: output}, c.finishHelper(name, startErr, output, false)
	}
	if !startRes.Succeeded() {
		return data.OpResult{Output: output}, c.finishHelper(name,
			fmt.Errorf("volume archive helper exited with status %d", startRes.ExitCode), output, false)
	}
	return data.OpResult{Output: output}, c.finishHelper(name, nil, output, false)
}

// finishHelper removes the helper by name and confirms it is gone. It never
// uses the operation context, so cancellation cannot skip cleanup. The lock
// holder calls this before returning, so no later operation can see the
// helper as a volume consumer.
func (c *Compose) finishHelper(name string, cause error, output string, lateCreation bool) error {
	cleanupErr := c.removeHelper(name, lateCreation)
	if cleanupErr != nil {
		if cause != nil {
			return fmt.Errorf("%w; %v", cause, cleanupErr)
		}
		return cleanupErr
	}
	return cause
}

func (c *Compose) removeHelper(name string, lateCreation bool) error {
	timeout := c.cleanupTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	poll := c.cleanupPoll
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	grace := c.cleanupGrace
	if grace <= 0 {
		grace = 3 * time.Second
	}
	now := time.Now()
	deadline := now.Add(timeout)
	graceUntil := now
	if lateCreation {
		// A create request whose client was killed can complete in the daemon
		// after our first inspection. Keep removing and watching for a short
		// grace window instead of trusting the first "not found".
		graceUntil = now.Add(grace)
	}
	found := false
	var lastErr error
	for {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, rmErr := c.run(cleanupCtx, execx.Command{Name: "docker", Args: []string{"rm", "-f", name}})
		inspectRes, inspectErr := c.run(cleanupCtx, execx.Command{
			Name: "docker", Args: []string{"inspect", "--format", "{{.Id}}", name},
		})
		cancel()
		switch {
		case inspectErr == nil && !inspectRes.Succeeded():
			// The daemon answered and the container no longer exists. After an
			// interrupted create, wait out the grace window in case the daemon
			// creates it late.
			if !lateCreation || found || time.Now().After(graceUntil) {
				return nil
			}
			lastErr = fmt.Errorf("helper container %s not created yet; watching for a late create", name)
		case inspectErr == nil:
			found = true
			lastErr = fmt.Errorf("helper container %s still exists", name)
		case rmErr == nil:
			lastErr = inspectErr
		default:
			lastErr = fmt.Errorf("rm helper: %v; inspect helper: %v", rmErr, inspectErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("remove backup helper %s: %w", name, lastErr)
		}
		time.Sleep(poll)
	}
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
