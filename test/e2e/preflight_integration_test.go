package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// Narrow requirement declarations must still perform their implied checks
// against a real Docker installation. This is the real-boundary counterpart of
// the fake-runner requirement tests.
func TestIntegrationNarrowPreflightRequirementsAgainstRealDocker(t *testing.T) {
	_ = requireIntegration(t)
	preflight := compose.New(execx.NewOSRunner(), os.Getenv)
	cases := []struct {
		name     string
		requires core.Requirements
	}{
		{"compose only", core.Requirements{core.RequirementCompose}},
		{"engine only", core.Requirements{core.RequirementEngine}},
		{"docker cli only", core.Requirements{core.RequirementDockerCLI}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := preflight.Check(context.Background(), tc.requires)
			if core.HasError(report.Diagnostics) {
				t.Fatalf("diagnostics = %+v", report.Diagnostics)
			}
			if report.Tools.DockerVersion == "" {
				t.Fatal("Docker CLI was not looked up")
			}
			switch {
			case tc.requires.Has(core.RequirementCompose):
				if report.Tools.ComposeVersion == "" {
					t.Fatal("Compose was not checked")
				}
			case tc.requires.Has(core.RequirementEngine):
				if report.Tools.EngineVersion == "" {
					t.Fatal("Docker Engine was not checked")
				}
			}
		})
	}

	// A remote endpoint must be refused even when only Compose was declared.
	remote := compose.New(execx.NewOSRunner(), func(key string) string {
		if key == "DOCKER_HOST" {
			return "tcp://127.0.0.1:2375"
		}
		return os.Getenv(key)
	})
	report := remote.Check(context.Background(), core.Requirements{core.RequirementCompose})
	found := false
	for _, d := range report.Diagnostics {
		if d.Category == core.CategoryRemoteEngine {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want remote_engine", report.Diagnostics)
	}
}
