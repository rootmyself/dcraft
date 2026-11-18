package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

func TestCheckDockerCLIOnlyDoesNotNeedComposeOrEngine(t *testing.T) {
	fake := fakeDocker(defaults(nil))
	report := compose.New(fake, nil).Check(context.Background(), core.Requirements{core.RequirementDockerCLI})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
	if report.Tools.DockerVersion != "29.10.0" {
		t.Fatalf("docker version = %q", report.Tools.DockerVersion)
	}
	for _, line := range fake.CallLines() {
		if strings.Contains(line, "compose") || strings.Contains(line, "info") || strings.Contains(line, "context") {
			t.Fatalf("docker_cli-only preflight ran extra command: %q", line)
		}
	}
}

func TestCheckDefaultStillRequiresEngineAndCompose(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {ExitCode: 1, Stderr: []byte("docker: unknown command: docker compose")},
	}))
	report := compose.New(fake, nil).Check(context.Background(), nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeMissing {
		t.Fatalf("category = %q", d.Category)
	}
}

func TestComposeMissingActionOffersInstallCommand(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {ExitCode: 1, Stderr: []byte("docker: unknown command: docker compose")},
	}))
	report := compose.New(fake, nil).Check(context.Background(), nil)
	d := onlyDiagnostic(t, report)
	if !hasAction(d, "dcraft install-compose") {
		t.Fatalf("compose_missing next actions = %v", d.NextActions)
	}
	for _, action := range d.NextActions {
		if strings.Contains(action, "sudo") {
			t.Fatalf("compose_missing suggests privilege escalation: %q", action)
		}
	}
}

func TestComposeMissingIsRecognizedFromShortFlagMangling(t *testing.T) {
	// Real Docker Desktop 29.10 with DOCKER_CONFIG pointing at a directory
	// without cli-plugins reports the missing plugin as an unknown top-level
	// flag for `compose version --short`, then unknown command for the full
	// command. This must still be classified as Compose missing, not
	// unsupported.
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {ExitCode: 1, Stderr: []byte("unknown flag: --short\n\nUsage:  docker [OPTIONS] COMMAND\n")},
		"compose version":         {ExitCode: 1, Stderr: []byte("docker: unknown command: docker compose version\n")},
	}))
	report := compose.New(fake, nil).Check(context.Background(), nil)
	d := onlyDiagnostic(t, report)
	if d.Category != core.CategoryComposeMissing {
		t.Fatalf("category = %q, detail=%q", d.Category, d.Detail)
	}
	if !hasAction(d, "dcraft install-compose") {
		t.Fatalf("next actions = %v", d.NextActions)
	}
}

func TestComposeWithoutShortFlagIsSupportedAndReported(t *testing.T) {
	fake := fakeDocker(defaults(map[string]execx.Result{
		"compose version --short": {ExitCode: 1, Stderr: []byte("unknown flag: --short")},
		"compose version":         {Stdout: []byte("Docker Compose version v5.7.0\n")},
	}))
	report := compose.New(fake, nil).Check(context.Background(), nil)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
	if report.Tools.ComposeVersion != "5.7.0" {
		t.Fatalf("compose version = %q", report.Tools.ComposeVersion)
	}
}

func TestOtherDiagnosticsDoNotOfferComposeInstall(t *testing.T) {
	cases := []struct {
		name   string
		runner execx.Runner
		env    map[string]string
		want   core.Category
	}{
		{
			name:   "docker missing",
			runner: &execxtest.Runner{LookPathErr: context.Canceled},
			want:   core.CategoryDockerMissing,
		},
		{
			name: "compose unsupported",
			runner: fakeDocker(defaults(map[string]execx.Result{
				"compose version --short": {ExitCode: 1, Stderr: []byte("unexpected failure from patched compose")},
			})),
			want: core.CategoryComposeUnsupported,
		},
		{
			name: "engine unreachable",
			runner: fakeDocker(defaults(map[string]execx.Result{
				"info --format {{.ServerVersion}}": {ExitCode: 1, Stderr: []byte("failed to connect to the docker API at unix:///nonexistent")},
			})),
			want: core.CategoryEngineUnreachable,
		},
		{
			name:   "remote endpoint",
			runner: fakeDocker(defaults(nil)),
			env:    map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"},
			want:   core.CategoryRemoteEngine,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := compose.New(tc.runner, envMap(tc.env).getenv).Check(context.Background(), nil)
			d := onlyDiagnostic(t, report)
			if d.Category != tc.want {
				t.Fatalf("category = %q, want %q", d.Category, tc.want)
			}
			if hasAction(d, "install-compose") {
				t.Fatalf("%s offers Compose install: %v", tc.want, d.NextActions)
			}
		})
	}
}

func hasAction(d core.Diagnostic, needle string) bool {
	for _, a := range d.NextActions {
		if strings.Contains(a, needle) {
			return true
		}
	}
	return false
}
