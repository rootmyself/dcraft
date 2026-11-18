// Package compose implements the Docker Compose adapter: environment
// preflight and read-only stack inspection. It depends only on the narrow
// execx boundary, so business rules can be exercised with a fake runner.
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// Compose is the Docker Compose adapter.
type Compose struct {
	runner execx.Runner
	getenv func(string) string
}

func New(runner execx.Runner, getenv func(string) string) *Compose {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return &Compose{runner: runner, getenv: getenv}
}

// Name identifies the backend in machine-readable results.
func (c *Compose) Name() string { return "compose" }

// probeDocument is a minimal valid Compose project used only for capability
// detection. It is fed on stdin and never creates resources.
const probeDocument = "services:\n  probe:\n    image: scratch\n"

const probeProject = "dcraft-capability-probe"

var dockerVersionRe = regexp.MustCompile(`(?i)docker version\s+([0-9][^\s,]*)`)

// Check implements core.Preflight.
func (c *Compose) Check(ctx context.Context) core.PreflightReport {
	report := core.PreflightReport{Diagnostics: []core.Diagnostic{}}

	if _, err := c.runner.LookPath("docker"); err != nil {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerMissing,
			Detail:      err.Error(),
			NextActions: []string{msgDockerMissingActionOne, msgDockerMissingActionTwo, msgDockerMissingActionThree},
		})
		return report
	}

	if res, err := c.run(ctx, execx.Command{Name: "docker", Args: []string{"--version"}}); err == nil && res.Succeeded() {
		if m := dockerVersionRe.FindStringSubmatch(res.Combined()); m != nil {
			report.Tools.DockerVersion = m[1]
		}
	}

	endpoint, err := c.dockerEndpoint(ctx)
	if err != nil {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryEngineEndpointUnknown,
			Severity:    core.SeverityError,
			Message:     msgEndpointUnknown,
			Detail:      err.Error(),
			NextActions: []string{msgEndpointUnknownActionOne, msgEndpointUnknownActionTwo},
		})
		return report
	}
	kind, remote, err := classifyEndpoint(endpoint)
	report.Tools.Endpoint = endpoint
	if err != nil {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryEngineEndpointUnknown,
			Severity:    core.SeverityError,
			Message:     msgEndpointUnknown,
			Detail:      err.Error(),
			NextActions: []string{msgEndpointUnknownActionOne, msgEndpointUnknownActionTwo},
		})
		return report
	}
	report.Tools.EndpointKind = kind
	if remote {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryRemoteEngine,
			Severity:    core.SeverityError,
			Message:     msgRemoteEngine,
			Detail:      endpoint,
			NextActions: []string{msgRemoteEngineActionOne, msgRemoteEngineActionTwo},
		})
		return report
	}

	versionRes, versionErr := c.run(ctx, execx.Command{Name: "docker", Args: []string{"compose", "version", "--short"}})
	if versionErr != nil {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      versionErr.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		})
		return report
	}
	if !versionRes.Succeeded() {
		text := versionRes.Combined()
		if isComposeMissingText(text) {
			report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
				Category:    core.CategoryComposeMissing,
				Severity:    core.SeverityError,
				Message:     msgComposeMissing,
				Detail:      summarize(text),
				NextActions: []string{msgComposeMissingActionOne, msgComposeMissingActionTwo, msgComposeMissingActionThree},
			})
			return report
		}
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryComposeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgComposeUnsupported,
			Detail:      summarize(text),
			NextActions: []string{msgComposeUnsupportedActionOne, msgComposeUnsupportedActionTwo, msgComposeUnsupportedActionThree},
		})
		return report
	}
	composeVersion := strings.TrimSpace(string(versionRes.Stdout))
	report.Tools.ComposeVersion = composeVersion
	if major, ok := majorVersion(composeVersion); !ok || major < 2 {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryComposeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgComposeTooOld,
			Detail:      composeVersion,
			NextActions: []string{msgComposeUnsupportedActionOne, msgComposeUnsupportedActionTwo, msgComposeUnsupportedActionThree},
		})
		return report
	}

	infoRes, infoErr := c.run(ctx, execx.Command{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
	if infoErr != nil {
		report.Diagnostics = append(report.Diagnostics, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      infoErr.Error(),
			NextActions: []string{msgDockerMissingActionTwo},
		})
		return report
	}
	if !infoRes.Succeeded() {
		report.Diagnostics = append(report.Diagnostics, engineDiagnostic(infoRes))
		return report
	}
	report.Tools.EngineVersion = strings.TrimSpace(string(infoRes.Stdout))

	if diag, ok := c.capabilityProbe(ctx); ok {
		report.Diagnostics = append(report.Diagnostics, diag)
	}
	return report
}

func (c *Compose) run(ctx context.Context, cmd execx.Command) (execx.Result, error) {
	return c.runner.Run(ctx, cmd)
}

func (c *Compose) dockerEndpoint(ctx context.Context) (string, error) {
	if host := strings.TrimSpace(c.getenv("DOCKER_HOST")); host != "" {
		return host, nil
	}
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: []string{"context", "ls", "--format", "json"}})
	if err != nil {
		return "", fmt.Errorf("docker context ls: %w", err)
	}
	if !res.Succeeded() {
		return "", fmt.Errorf("docker context ls: %s", summarize(res.Combined()))
	}
	contexts, err := parseContexts(res.Stdout)
	if err != nil {
		return "", fmt.Errorf("docker context ls: %w", err)
	}
	desired := strings.TrimSpace(c.getenv("DOCKER_CONTEXT"))
	for _, dc := range contexts {
		if desired != "" {
			if dc.Name == desired {
				if dc.DockerEndpoint == "" {
					return "", fmt.Errorf("context %q has no Docker endpoint", desired)
				}
				return dc.DockerEndpoint, nil
			}
			continue
		}
		if dc.Current {
			if dc.DockerEndpoint == "" {
				return "", fmt.Errorf("current context %q has no Docker endpoint", dc.Name)
			}
			return dc.DockerEndpoint, nil
		}
	}
	if desired != "" {
		return "", fmt.Errorf("docker context %q not found", desired)
	}
	return "", fmt.Errorf("no current Docker context")
}

type dockerContext struct {
	Current        bool   `json:"Current"`
	DockerEndpoint string `json:"DockerEndpoint"`
	Name           string `json:"Name"`
}

func parseContexts(data []byte) ([]dockerContext, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var out []dockerContext
		if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	var out []dockerContext
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var dc dockerContext
		if err := json.Unmarshal([]byte(line), &dc); err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, nil
}

// classifyEndpoint decides local vs remote from the endpoint scheme only.
// Docker Desktop's VM-backed unix socket is local.
func classifyEndpoint(raw string) (kind string, remote bool, err error) {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	if s == "" {
		return "", false, fmt.Errorf("empty Docker endpoint")
	}
	switch {
	case strings.HasPrefix(lower, "unix:"), strings.HasPrefix(s, "/"):
		return "unix", false, nil
	case strings.HasPrefix(lower, "npipe:"):
		return "npipe", false, nil
	case strings.HasPrefix(lower, "fd:"):
		return "fd", false, nil
	case strings.HasPrefix(lower, "tcp:"):
		return "tcp", true, nil
	case strings.HasPrefix(lower, "ssh:"):
		return "ssh", true, nil
	case strings.HasPrefix(lower, "http:"):
		return "http", true, nil
	case strings.HasPrefix(lower, "https:"):
		return "https", true, nil
	case strings.Contains(s, "://"):
		return "", false, fmt.Errorf("unsupported Docker endpoint scheme in %q", raw)
	case strings.Contains(s, ":"):
		// docker.example.com:2375 without a scheme is a remote TCP target.
		return "tcp", true, nil
	default:
		return "", false, fmt.Errorf("unsupported Docker endpoint %q", raw)
	}
}

func majorVersion(v string) (int, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	if v == "" {
		return 0, false
	}
	first := strings.SplitN(v, ".", 2)[0]
	n, err := strconv.Atoi(first)
	if err != nil {
		return 0, false
	}
	return n, true
}

func isComposeMissingText(text string) bool {
	lower := strings.ToLower(text)
	for _, needle := range []string{
		"unknown command: docker compose",
		"unknown command \"compose\"",
		"'compose' is not a docker command",
		"is not a docker command",
		"docker: unknown command",
		"not found",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (c *Compose) capabilityProbe(ctx context.Context) (core.Diagnostic, bool) {
	configArgs := []string{"compose", "-p", probeProject, "-f", "-", "config", "--format", "json"}
	res, err := c.run(ctx, execx.Command{Name: "docker", Args: configArgs, Stdin: strings.NewReader(probeDocument)})
	if err != nil || !res.Succeeded() {
		return c.probeFailure(res, err), true
	}
	var probe map[string]any
	if err := json.Unmarshal(res.Stdout, &probe); err != nil {
		return c.unsupportedDiagnostic(res, fmt.Errorf("config JSON output is not valid JSON: %w", err)), true
	}

	psArgs := []string{"compose", "-p", probeProject, "-f", "-", "ps", "--all", "--format", "json"}
	res, err = c.run(ctx, execx.Command{Name: "docker", Args: psArgs, Stdin: strings.NewReader(probeDocument)})
	if err != nil || !res.Succeeded() {
		return c.probeFailure(res, err), true
	}
	if !validNDJSON(res.Stdout) {
		return c.unsupportedDiagnostic(res, fmt.Errorf("ps JSON output is not valid JSON lines")), true
	}
	return core.Diagnostic{}, false
}

// probeFailure keeps the diagnostic taxonomy honest if the engine went away
// between the healthy check and a capability probe.
func (c *Compose) probeFailure(res execx.Result, err error) core.Diagnostic {
	if err != nil {
		return c.unsupportedDiagnostic(res, err)
	}
	if looksLikeEngineFailure(strings.ToLower(res.Combined())) {
		return engineDiagnostic(res)
	}
	return c.unsupportedDiagnostic(res, nil)
}

func validNDJSON(data []byte) bool {
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return false
		}
	}
	return true
}

func (c *Compose) unsupportedDiagnostic(res execx.Result, err error) core.Diagnostic {
	detail := ""
	if err != nil {
		detail = err.Error()
	} else {
		detail = summarize(res.Combined())
	}
	return core.Diagnostic{
		Category:    core.CategoryComposeUnsupported,
		Severity:    core.SeverityError,
		Message:     msgComposeUnsupported,
		Detail:      detail,
		NextActions: []string{msgComposeUnsupportedActionOne, msgComposeUnsupportedActionTwo, msgComposeUnsupportedActionThree},
	}
}

func engineDiagnostic(res execx.Result) core.Diagnostic {
	text := res.Combined()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "permission denied") {
		return core.Diagnostic{
			Category:    core.CategoryEnginePermission,
			Severity:    core.SeverityError,
			Message:     msgEnginePermission,
			Detail:      summarize(text),
			NextActions: []string{msgEnginePermissionActionOne, msgEnginePermissionActionTwo, msgEnginePermissionActionThree},
		}
	}
	return core.Diagnostic{
		Category:    core.CategoryEngineUnreachable,
		Severity:    core.SeverityError,
		Message:     msgEngineUnreachable,
		Detail:      summarize(text),
		NextActions: []string{msgEngineUnreachableActionOne, msgEngineUnreachableActionTwo, msgEngineUnreachableActionThree},
	}
}

func summarize(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	out := strings.Join(cleaned, "\n")
	const max = 800
	if len(out) > max {
		out = out[:max] + "…"
	}
	return out
}
