package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Inspect implements stack.Backend. It reads the resolved Compose model and
// the project's containers. It never inspects environment variables, labels or
// commands, so user secrets are not copied into the result.
func (c *Compose) Inspect(ctx context.Context, t core.Target) (stack.Snapshot, error) {
	configRes, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "config", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if err != nil || !configRes.Succeeded() {
		return stack.Snapshot{}, core.FailCause(err, classifyConfigFailure(configRes, err))
	}
	config, err := parseConfig(configRes.Stdout)
	if err != nil {
		return stack.Snapshot{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectOutput,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgInspectOutputAction},
		})
	}

	psRes, err := c.run(ctx, execx.Command{
		Name: "docker",
		Args: append(t.ComposeArgs(), "ps", "--all", "--format", "json"),
		Dir:  t.ProjectDir,
	})
	if err != nil || !psRes.Succeeded() {
		return stack.Snapshot{}, core.FailCause(err, classifyPSFailure(psRes, err))
	}
	records, err := parsePS(psRes.Stdout)
	if err != nil {
		return stack.Snapshot{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectOutput,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgInspectOutputAction},
		})
	}

	projectName := strings.TrimSpace(config.Name)
	if projectName == "" {
		projectName = t.ProjectName
	}
	if projectName == "" && t.ProjectDir != "" {
		projectName = filepath.Base(t.ProjectDir)
	}

	return buildSnapshot(t, projectName, config, records), nil
}

type configDoc struct {
	Name     string                     `json:"name"`
	Services map[string]json.RawMessage `json:"services"`
}

type configService struct {
	Image       string          `json:"image"`
	Build       json.RawMessage `json:"build"`
	Healthcheck json.RawMessage `json:"healthcheck"`
	DependsOn   json.RawMessage `json:"depends_on"`
	Ports       []configPort    `json:"ports"`
}

type configPort struct {
	Mode      string          `json:"mode"`
	HostIP    string          `json:"host_ip"`
	Target    int             `json:"target"`
	Published json.RawMessage `json:"published"`
	Protocol  string          `json:"protocol"`
}

type psRecord struct {
	Name       string        `json:"Name"`
	Service    string        `json:"Service"`
	State      string        `json:"State"`
	Health     string        `json:"Health"`
	Image      string        `json:"Image"`
	ExitCode   int           `json:"ExitCode"`
	Publishers []psPublisher `json:"Publishers"`
}

type psPublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

func parseConfig(data []byte) (configDoc, error) {
	var doc configDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return configDoc{}, fmt.Errorf("compose config JSON: %w", err)
	}
	return doc, nil
}

func parsePS(data []byte) ([]psRecord, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var out []psRecord
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "null" {
			continue
		}
		var rec psRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("compose ps JSON line: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func parseService(raw json.RawMessage) (configService, error) {
	var svc configService
	if err := json.Unmarshal(raw, &svc); err != nil {
		return configService{}, err
	}
	return svc, nil
}

func hasBuild(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func hasHealthcheck(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return false
	}
	var hc struct {
		Disable bool `json:"disable"`
	}
	if err := json.Unmarshal(raw, &hc); err == nil && hc.Disable {
		return false
	}
	return true
}

func buildSnapshot(t core.Target, projectName string, config configDoc, records []psRecord) stack.Snapshot {
	snap := stack.Snapshot{
		Backend:     "compose",
		ProjectName: projectName,
		ProjectDir:  t.ProjectDir,
		Environment: t.Environment,
		Services:    []stack.Service{},
	}

	byService := map[string][]psRecord{}
	for _, rec := range records {
		name := rec.Service
		if name == "" {
			name = "(unknown)"
		}
		byService[name] = append(byService[name], rec)
	}

	var names []string
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := config.Services[name]
		svc, err := parseService(raw)
		if err != nil {
			continue
		}
		service := serviceFromConfig(name, svc, byService[name])
		delete(byService, name)
		snap.Services = append(snap.Services, service)
	}

	var extra []string
	for name := range byService {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		snap.Services = append(snap.Services, serviceFromInstances(name, byService[name]))
	}

	snap.Summary = summarizeServices(snap.Services)
	return snap
}

func serviceFromConfig(name string, cfg configService, records []psRecord) stack.Service {
	svc := stack.Service{
		Name:                  name,
		Configured:            true,
		Image:                 cfg.Image,
		Build:                 hasBuild(cfg.Build),
		HealthcheckConfigured: hasHealthcheck(cfg.Healthcheck),
		Instances:             []stack.Instance{},
		Connections:           []stack.Connection{},
	}
	svc.Instances = instancesFromRecords(records)
	if svc.Image == "" && len(svc.Instances) > 0 {
		svc.Image = svc.Instances[0].Image
	}
	svc.State, svc.Health, svc.AppVerified = aggregateInstances(svc.Instances)
	svc.Connections = connectionsFor(cfg.Ports, records)
	return svc
}

func serviceFromInstances(name string, records []psRecord) stack.Service {
	svc := stack.Service{
		Name:        name,
		Configured:  false,
		Instances:   instancesFromRecords(records),
		Connections: []stack.Connection{},
	}
	if len(svc.Instances) > 0 {
		svc.Image = svc.Instances[0].Image
	}
	svc.State, svc.Health, svc.AppVerified = aggregateInstances(svc.Instances)
	svc.Connections = connectionsFor(nil, records)
	return svc
}

func instancesFromRecords(records []psRecord) []stack.Instance {
	instances := make([]stack.Instance, 0, len(records))
	for _, rec := range records {
		instances = append(instances, stack.Instance{
			Name:     rec.Name,
			State:    normalizeInstanceState(rec.State),
			Health:   normalizeHealth(rec.Health),
			Image:    rec.Image,
			ExitCode: rec.ExitCode,
		})
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	return instances
}

func normalizeInstanceState(state string) string {
	s := strings.ToLower(strings.TrimSpace(state))
	if s == "" {
		return string(stack.StateUnknown)
	}
	return s
}

func normalizeHealth(h string) stack.Health {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "healthy":
		return stack.HealthHealthy
	case "starting":
		return stack.HealthStarting
	case "unhealthy":
		return stack.HealthUnhealthy
	default:
		return stack.HealthNone
	}
}

func aggregateInstances(instances []stack.Instance) (stack.ServiceState, stack.Health, bool) {
	if len(instances) == 0 {
		return stack.StateNotCreated, stack.HealthNone, false
	}
	running, restarting, paused, created, exited := 0, 0, 0, 0, 0
	healthy, starting, unhealthy := 0, 0, 0
	for _, in := range instances {
		switch in.State {
		case string(stack.StateRunning):
			running++
		case string(stack.StateRestarting):
			restarting++
		case string(stack.StatePaused):
			paused++
		case string(stack.StateCreated):
			created++
		case string(stack.StateExited):
			exited++
		}
		switch in.Health {
		case stack.HealthHealthy:
			healthy++
		case stack.HealthStarting:
			starting++
		case stack.HealthUnhealthy:
			unhealthy++
		}
	}

	var state stack.ServiceState
	switch {
	case running > 0:
		state = stack.StateRunning
	case restarting > 0:
		state = stack.StateRestarting
	case paused > 0:
		state = stack.StatePaused
	case created > 0:
		state = stack.StateCreated
	case exited > 0:
		state = stack.StateExited
	default:
		state = stack.StateUnknown
	}

	var health stack.Health
	switch {
	case running == 0:
		health = stack.HealthNone
	case unhealthy > 0:
		health = stack.HealthUnhealthy
	case starting > 0:
		health = stack.HealthStarting
	case healthy == running && healthy == len(instances):
		health = stack.HealthHealthy
	default:
		health = stack.HealthNone
	}

	verified := running == len(instances) && healthy == len(instances) && len(instances) > 0
	return state, health, verified
}

func connectionsFor(configured []configPort, records []psRecord) []stack.Connection {
	connections := []stack.Connection{}
	seen := map[string]bool{}
	runtimeTargets := map[string]bool{}

	for _, rec := range records {
		for _, pub := range rec.Publishers {
			key := fmt.Sprintf("%s|%d|%d|%s", pub.URL, pub.PublishedPort, pub.TargetPort, pub.Protocol)
			if seen[key] {
				continue
			}
			seen[key] = true
			runtimeTargets[fmt.Sprintf("%d|%s", pub.TargetPort, pub.Protocol)] = true
			connections = append(connections, stack.Connection{
				Source:        stack.SourceRuntime,
				HostIP:        pub.URL,
				PublishedPort: pub.PublishedPort,
				TargetPort:    pub.TargetPort,
				Protocol:      pub.Protocol,
			})
		}
	}

	for _, p := range configured {
		if runtimeTargets[fmt.Sprintf("%d|%s", p.Target, p.Protocol)] {
			continue
		}
		key := fmt.Sprintf("%s|%d|%d|%s", p.HostIP, parsePublished(p.Published), p.Target, p.Protocol)
		if seen[key] {
			continue
		}
		seen[key] = true
		connections = append(connections, stack.Connection{
			Source:        stack.SourceConfig,
			HostIP:        p.HostIP,
			PublishedPort: parsePublished(p.Published),
			TargetPort:    p.Target,
			Protocol:      p.Protocol,
		})
	}
	return connections
}

func parsePublished(raw json.RawMessage) int {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	if s == "" || s == "null" {
		return 0
	}
	if i := strings.IndexAny(s, "-,"); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func summarizeServices(services []stack.Service) stack.Summary {
	var s stack.Summary
	s.Services = len(services)
	for _, svc := range services {
		s.Instances += len(svc.Instances)
		switch svc.State {
		case stack.StateRunning:
			s.Running++
		case stack.StateNotCreated:
			if svc.Configured {
				s.NotCreated++
			}
		default:
			if len(svc.Instances) > 0 {
				s.Stopped++
			}
		}
		if svc.AppVerified {
			s.Healthy++
		}
		if svc.State == stack.StateRunning && !svc.AppVerified {
			s.Unverified++
		}
		if svc.Health == stack.HealthUnhealthy {
			s.Unhealthy++
		}
	}
	return s
}

func classifyConfigFailure(res execx.Result, runErr error) core.Diagnostic {
	text := res.Combined()
	if runErr != nil {
		return core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      runErr.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		}
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "no configuration file provided"):
		return core.Diagnostic{
			Category:    core.CategoryNoConfiguration,
			Severity:    core.SeverityError,
			Message:     msgNoConfiguration,
			Detail:      summarize(text),
			NextActions: []string{msgNoConfigurationActionOne, msgNoConfigurationActionTwo, msgNoConfigurationActionThree},
		}
	case strings.Contains(lower, "unknown flag"), strings.Contains(lower, "unknown command"):
		return unsupportedComposeDiagnostic(text)
	case looksLikeEngineFailure(lower):
		return engineDiagnostic(res)
	default:
		return core.Diagnostic{
			Category:    core.CategoryInvalidConfiguration,
			Severity:    core.SeverityError,
			Message:     msgInvalidConfiguration,
			Detail:      summarize(text),
			NextActions: []string{msgInvalidConfigurationActionOne, msgInvalidConfigurationActionTwo, msgInvalidConfigurationActionThree},
		}
	}
}

func classifyPSFailure(res execx.Result, runErr error) core.Diagnostic {
	text := res.Combined()
	if runErr != nil {
		return core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      runErr.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		}
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "no configuration file provided"):
		return core.Diagnostic{
			Category:    core.CategoryNoConfiguration,
			Severity:    core.SeverityError,
			Message:     msgNoConfiguration,
			Detail:      summarize(text),
			NextActions: []string{msgNoConfigurationActionOne, msgNoConfigurationActionTwo, msgNoConfigurationActionThree},
		}
	case strings.Contains(lower, "unknown flag"), strings.Contains(lower, "unknown command"):
		return unsupportedComposeDiagnostic(text)
	case looksLikeEngineFailure(lower):
		return engineDiagnostic(res)
	default:
		return core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgInspectPSFailed,
			Detail:      summarize(text),
			NextActions: []string{msgInspectPSFailedActionOne, msgInspectPSFailedActionTwo},
		}
	}
}

func looksLikeEngineFailure(lower string) bool {
	for _, needle := range []string{
		"permission denied",
		"cannot connect",
		"failed to connect",
		"is the docker daemon running",
		"connection refused",
		"no such file or directory",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func unsupportedComposeDiagnostic(text string) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryComposeUnsupported,
		Severity:    core.SeverityError,
		Message:     msgComposeUnsupported,
		Detail:      summarize(text),
		NextActions: []string{msgComposeUnsupportedActionOne, msgComposeUnsupportedActionTwo, msgComposeUnsupportedActionThree},
	}
}
