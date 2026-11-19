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

// A Compose-only declaration must never skip the Docker CLI lookup, the remote
// endpoint refusal or the Compose presence checks.
func TestCheckComposeOnlyStillLooksUpDockerAndRefusesRemote(t *testing.T) {
	t.Run("docker missing", func(t *testing.T) {
		fake := &execxtest.Runner{LookPathErr: context.Canceled}
		report := compose.New(fake, nil).Check(context.Background(), core.Requirements{core.RequirementCompose})
		d := onlyDiagnostic(t, report)
		if d.Category != core.CategoryDockerMissing {
			t.Fatalf("category = %q, want %q", d.Category, core.CategoryDockerMissing)
		}
	})

	t.Run("remote endpoint refused before Compose", func(t *testing.T) {
		fake := fakeDocker(defaults(nil))
		report := compose.New(fake, envMap{"DOCKER_HOST": "tcp://127.0.0.1:2375"}.getenv).
			Check(context.Background(), core.Requirements{core.RequirementCompose})
		d := onlyDiagnostic(t, report)
		if d.Category != core.CategoryRemoteEngine {
			t.Fatalf("category = %q, want %q", d.Category, core.CategoryRemoteEngine)
		}
		for _, line := range fake.CallLines() {
			if strings.Contains(line, "compose version") {
				t.Fatalf("Compose was checked before the remote refusal: %q", line)
			}
		}
	})

	t.Run("compose checked", func(t *testing.T) {
		fake := fakeDocker(defaults(nil))
		report := compose.New(fake, nil).Check(context.Background(), core.Requirements{core.RequirementCompose})
		if len(report.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v", report.Diagnostics)
		}
		if report.Tools.ComposeVersion != "5.7.0" {
			t.Fatalf("compose version = %q", report.Tools.ComposeVersion)
		}
		var sawCompose, sawInfo bool
		for _, line := range fake.CallLines() {
			if strings.Contains(line, "compose version --short") {
				sawCompose = true
			}
			if strings.Contains(line, "info --format") {
				sawInfo = true
			}
		}
		if !sawCompose {
			t.Fatalf("compose-only preflight did not check Compose: %v", fake.CallLines())
		}
		if sawInfo {
			t.Fatalf("compose-only preflight contacted the engine: %v", fake.CallLines())
		}
	})
}

// An engine-only declaration must look up Docker, refuse a remote endpoint and
// check the local engine, but it must not require an installed Compose.
func TestCheckEngineOnlyLooksUpDockerAndSkipsCompose(t *testing.T) {
	t.Run("docker missing", func(t *testing.T) {
		fake := &execxtest.Runner{LookPathErr: context.Canceled}
		report := compose.New(fake, nil).Check(context.Background(), core.Requirements{core.RequirementEngine})
		d := onlyDiagnostic(t, report)
		if d.Category != core.CategoryDockerMissing {
			t.Fatalf("category = %q, want %q", d.Category, core.CategoryDockerMissing)
		}
	})

	t.Run("remote endpoint refused", func(t *testing.T) {
		fake := fakeDocker(defaults(nil))
		report := compose.New(fake, envMap{"DOCKER_HOST": "ssh://user@example.com"}.getenv).
			Check(context.Background(), core.Requirements{core.RequirementEngine})
		d := onlyDiagnostic(t, report)
		if d.Category != core.CategoryRemoteEngine {
			t.Fatalf("category = %q, want %q", d.Category, core.CategoryRemoteEngine)
		}
	})

	t.Run("engine checked without Compose", func(t *testing.T) {
		fake := fakeDocker(defaults(map[string]execx.Result{
			"compose version --short": {ExitCode: 1, Stderr: []byte("docker: unknown command: docker compose")},
		}))
		report := compose.New(fake, nil).Check(context.Background(), core.Requirements{core.RequirementEngine})
		if len(report.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v", report.Diagnostics)
		}
		if report.Tools.EngineVersion != "29.9.3" {
			t.Fatalf("engine version = %q", report.Tools.EngineVersion)
		}
		for _, line := range fake.CallLines() {
			if strings.Contains(line, "compose version") {
				t.Fatalf("engine-only preflight checked Compose: %q", line)
			}
		}
	})
}
