package compose_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

const (
	goodComposeVersion = "5.7.0"
	goodDockerVersion  = "Docker version 29.10.0, build fdd4b8e5da"
	contextDesktop     = `{"Current":false,"Description":"Current DOCKER_HOST based configuration","DockerEndpoint":"unix:///var/run/docker.sock","Error":"","Name":"default"}
{"Current":true,"Description":"Docker Desktop","DockerEndpoint":"unix:///Users/me/.docker/run/docker.sock","Error":"","Name":"desktop-linux"}
`
	minimalProbeConfig = `{"name":"dcraft-capability-probe","services":{"probe":{"image":"scratch"}}}`
)

type envMap map[string]string

func (e envMap) getenv(k string) string { return e[k] }

func fakeDocker(handler func(cmd execx.Command) (execx.Result, error)) *execxtest.Runner {
	return &execxtest.Runner{
		LookPathPath: "/usr/local/bin/docker",
		Handler:      handler,
	}
}

func defaults(overrides map[string]execx.Result) func(execx.Command) (execx.Result, error) {
	return func(cmd execx.Command) (execx.Result, error) {
		key := strings.Join(cmd.Args, " ")
		if r, ok := overrides[key]; ok {
			return r, nil
		}
		switch {
		case key == "--version":
			return execx.Result{Stdout: []byte(goodDockerVersion + "\n")}, nil
		case key == "compose version --short":
			return execx.Result{Stdout: []byte(goodComposeVersion + "\n")}, nil
		case key == "context ls --format json":
			return execx.Result{Stdout: []byte(contextDesktop)}, nil
		case key == "info --format {{.ServerVersion}}":
			return execx.Result{Stdout: []byte("29.9.3\n")}, nil
		case strings.HasSuffix(key, "config --format json"):
			return execx.Result{Stdout: []byte(minimalProbeConfig)}, nil
		case strings.HasSuffix(key, "ps --all --format json"):
			return execx.Result{}, nil
		}
		return execx.Result{Stderr: []byte("unexpected fake docker invocation: " + key), ExitCode: 99}, nil
	}
}

func check(t *testing.T, runner execx.Runner, env map[string]string) core.PreflightReport {
	t.Helper()
	return compose.New(runner, envMap(env).getenv).Check(context.Background(), nil)
}

func onlyDiagnostic(t *testing.T, report core.PreflightReport) core.Diagnostic {
	t.Helper()
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", report.Diagnostics)
	}
	return report.Diagnostics[0]
}

func TestPreflightSucceedsAndReportsTools(t *testing.T) {
	fake := fakeDocker(defaults(nil))
	report := check(t, fake, nil)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none", report.Diagnostics)
	}
	if report.Tools.DockerVersion != "29.10.0" {
		t.Fatalf("DockerVersion = %q, want 29.10.0", report.Tools.DockerVersion)
	}
	if report.Tools.ComposeVersion != "5.7.0" {
		t.Fatalf("ComposeVersion = %q, want 5.7.0", report.Tools.ComposeVersion)
	}
	if report.Tools.EngineVersion != "29.9.3" {
		t.Fatalf("EngineVersion = %q, want 29.9.3", report.Tools.EngineVersion)
	}
	if report.Tools.EndpointKind != "unix" {
		t.Fatalf("EndpointKind = %q, want unix", report.Tools.EndpointKind)
	}
	if !strings.Contains(report.Tools.Endpoint, ".docker/run/docker.sock") {
		t.Fatalf("Endpoint = %q, want desktop socket", report.Tools.Endpoint)
	}
}

func TestPreflightDockerMissing(t *testing.T) {
	fake := &execxtest.Runner{LookPathErr: errors.New("not found")}
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryDockerMissing {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryDockerMissing)
	}
	if !strings.Contains(d.Message, "Docker") {
		t.Fatalf("message = %q", d.Message)
	}
	if len(d.NextActions) < 2 {
		t.Fatalf("next actions = %v, want actionable guidance", d.NextActions)
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("docker was invoked despite missing CLI: %v", fake.Calls())
	}
}

func TestPreflightRefusesRemoteDockerHostWithoutContactingEngine(t *testing.T) {
	for _, host := range []string{
		"tcp://127.0.0.1:2375",
		"ssh://user@example.com",
		"http://docker.example.com",
		"https://docker.example.com",
		"docker.example.com:2375",
	} {
		t.Run(host, func(t *testing.T) {
			fake := fakeDocker(defaults(nil))
			report := check(t, fake, map[string]string{"DOCKER_HOST": host})
			d := onlyDiagnostic(t, report)
			if d.Category != core.CategoryRemoteEngine {
				t.Fatalf("category = %q, want %q (%s)", d.Category, core.CategoryRemoteEngine, d.Message)
			}
			if !strings.Contains(d.Detail, host) {
				t.Fatalf("detail %q should include endpoint %q", d.Detail, host)
			}
			for _, call := range fake.Calls() {
				if call.Name == "docker" && len(call.Args) > 0 && call.Args[0] == "info" {
					t.Fatalf("docker info was called for a remote endpoint: %v", call.Args)
				}
			}
		})
	}
}

func TestPreflightAcceptsLocalUnixDockerHost(t *testing.T) {
	fake := fakeDocker(defaults(nil))
	report := check(t, fake, map[string]string{"DOCKER_HOST": "unix:///Users/me/.docker/run/docker.sock"})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none", report.Diagnostics)
	}
	if report.Tools.EndpointKind != "unix" {
		t.Fatalf("EndpointKind = %q, want unix", report.Tools.EndpointKind)
	}
	for _, call := range fake.Calls() {
		if len(call.Args) > 0 && call.Args[0] == "context" {
			t.Fatalf("context inspected even though DOCKER_HOST was set: %v", call.Args)
		}
	}
}

func TestPreflightRemoteContextIsRefused(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"context ls --format json": {Stdout: []byte(`{"Current":true,"DockerEndpoint":"tcp://10.0.0.5:2376","Name":"remote"}` + "\n")},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryRemoteEngine {
		t.Fatalf("category = %q, want remote_engine", d.Category)
	}
	if !strings.Contains(d.Detail, "tcp://10.0.0.5:2376") {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestPreflightNamedDockerContextIsResolved(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"context ls --format json": {Stdout: []byte(
			`{"Current":true,"DockerEndpoint":"unix:///local.sock","Name":"desktop-linux"}` + "\n" +
				`{"Current":false,"DockerEndpoint":"ssh://user@host","Name":"buildhost"}` + "\n")},
	}))
	report := check(t, fake, map[string]string{"DOCKER_CONTEXT": "buildhost"})
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryRemoteEngine {
		t.Fatalf("category = %q, want remote_engine", d.Category)
	}
	if !strings.Contains(d.Detail, "ssh://user@host") {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestPreflightContextListFailureIsEndpointUnknown(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"context ls --format json": {Stderr: []byte("context not supported"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEngineEndpointUnknown {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEngineEndpointUnknown)
	}
}

func TestPreflightContextWithoutEndpointIsEndpointUnknown(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"context ls --format json": {Stdout: []byte(`{"Current":true,"DockerEndpoint":"","Name":"broken"}` + "\n")},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEngineEndpointUnknown {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEngineEndpointUnknown)
	}
}

func TestPreflightComposeMissingIsNotDockerMissing(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {Stderr: []byte("docker: unknown command: docker compose\n\nRun 'docker --help' for more information\n"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeMissing {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryComposeMissing)
	}
	if !strings.Contains(strings.Join(d.NextActions, " "), "Compose") {
		t.Fatalf("next actions = %v", d.NextActions)
	}
}

func TestPreflightComposeV1IsUnsupported(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {Stdout: []byte("1.29.2\n")},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeUnsupported {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryComposeUnsupported)
	}
}

func TestPreflightUnsupportedComposeFeature(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose -p dcraft-capability-probe -f - config --format json": {Stderr: []byte("unknown flag: --format"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeUnsupported {
		t.Fatalf("category = %q, want %q: %s", d.Category, core.CategoryComposeUnsupported, d.Message)
	}
	if !strings.Contains(d.Detail, "unknown flag") {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestPreflightUnsupportedPSJSON(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose -p dcraft-capability-probe -f - ps --all --format json": {Stderr: []byte("invalid value \"json\" for --format"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeUnsupported {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryComposeUnsupported)
	}
}

func TestPreflightCapabilityProbeEngineFailureStaysEngineFailure(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose -p dcraft-capability-probe -f - config --format json": {
			Stderr:   []byte("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"),
			ExitCode: 1,
		},
	}))
	// The engine check itself succeeds; only the later probe sees the engine
	// disappear, which must not be mislabelled as an unsupported Compose
	// feature.
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEngineUnreachable {
		t.Fatalf("category = %q, want %q: %s", d.Category, core.CategoryEngineUnreachable, d.Message)
	}
}

func TestPreflightEngineUnreachableUnix(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"info --format {{.ServerVersion}}": {Stderr: []byte("failed to connect to the docker API at unix:///nonexistent/docker.sock; check if the path is correct and if the daemon is running: dial unix /nonexistent/docker.sock: connect: no such file or directory\n"), ExitCode: 1},
	}))
	report := check(t, fake, map[string]string{"DOCKER_HOST": "unix:///nonexistent/docker.sock"})
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEngineUnreachable {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEngineUnreachable)
	}
}

func TestPreflightEngineUnreachableTCP(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"info --format {{.ServerVersion}}": {Stderr: []byte("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEngineUnreachable {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEngineUnreachable)
	}
}

func TestPreflightEnginePermissionDenied(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"info --format {{.ServerVersion}}": {Stderr: []byte("permission denied while trying to connect to the docker API at unix:///var/run/vpncontrol.sock\n"), ExitCode: 1},
	}))
	report := check(t, fake, nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryEnginePermission {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEnginePermission)
	}
	if !strings.Contains(strings.Join(d.NextActions, " "), "권한") {
		t.Fatalf("next actions should discuss permissions: %v", d.NextActions)
	}
}

func TestPreflightLocalDesktopVMIsNotRemote(t *testing.T) {
	// Docker Desktop exposes a VM-backed unix socket. Locality is decided by
	// endpoint scheme, not by docker info fields.
	report := check(t, fakeDocker(defaults(nil)), map[string]string{"DOCKER_HOST": "unix:///Users/me/.docker/run/docker.sock"})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none", report.Diagnostics)
	}
}

func TestPreflightProbeReadsNoHostFiles(t *testing.T) {
	// The capability probe feeds a minimal document on stdin and must not
	// depend on the target folder.
	fake := fakeDocker(defaults(nil))
	check(t, fake, nil)
	for _, call := range fake.Calls() {
		if strings.Contains(strings.Join(call.Args, " "), "-f -") && call.Stdin == nil {
			t.Fatalf("probe command without stdin: %v", call.Args)
		}
	}
}
