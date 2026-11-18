package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// installComposeEnvironmentAliases creates a project whose .dcraft.yaml
// registers environment aliases (or is deliberately broken) and returns the
// project folder plus an environment where Compose is already available.
func installComposeEnvironmentAliases(t *testing.T, configBody string) (string, []string) {
	t.Helper()
	fake := newInstallFakeDocker(t)
	configDir := t.TempDir()
	pluginDir := filepath.Join(configDir, "cli-plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(pluginDir, "docker-compose")
	if err := os.WriteFile(plugin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	writeProject(t, project)
	if configBody != "" {
		if err := os.WriteFile(filepath.Join(project, ".dcraft.yaml"), []byte(configBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := buildEnv(map[string]string{
		"PATH":                      fake.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":           fake.logPath,
		"DCRAFT_STATE_DIR":          t.TempDir(),
		"DCRAFT_COMPOSE_PLUGIN_DIR": pluginDir,
		"DOCKER_CONFIG":             configDir,
	})
	return project, env
}

func assertInstallComposeAlreadyAvailable(t *testing.T, project string, env []string) {
	t.Helper()
	res := runProcess(t, project, env, strings.NewReader(""),
		"install-compose", "--approve", "install", "--json")
	if res.code != 0 {
		t.Fatalf("install-compose exit = %d, want 0\nstdout=%s\nstderr=%s", res.code, res.stdout, res.stderr)
	}
	parsed := parseInstallResult(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q diagnostics=%+v", parsed.Outcome, parsed.Diagnostics)
	}
	if !parsed.Data.Install.AlreadyAvailable || parsed.Data.Install.Changed {
		t.Fatalf("install payload = %+v, want already available without change", parsed.Data.Install)
	}
	if _, ok := findDiagnostic(parsed, core.CategoryEnvironmentRequired); ok {
		t.Fatalf("target-optional feature required an environment alias: %+v", parsed.Diagnostics)
	}
	if _, ok := findDiagnostic(parsed, core.CategoryEnvironmentInvalid); ok {
		t.Fatalf("target-optional feature rejected the environment catalog: %+v", parsed.Diagnostics)
	}
}

// A project that registers environment aliases must not make the suggested
// `dcraft install-compose` fail before it can report that Compose is present.
func TestInstallComposeIgnoresRegisteredEnvironmentAliases(t *testing.T) {
	project, env := installComposeEnvironmentAliases(t, `environments:
  dev:
    compose_files: [compose.yaml]
  prod:
    compose_files: [compose.yaml]
`)
	assertInstallComposeAlreadyAvailable(t, project, env)
}

// Even a broken .dcraft.yaml cannot block a target-optional feature; the file
// belongs to the project's Compose selection, which install-compose never
// reads.
func TestInstallComposeIgnoresBrokenEnvironmentCatalog(t *testing.T) {
	project, env := installComposeEnvironmentAliases(t, "environments: [broken\n")
	assertInstallComposeAlreadyAvailable(t, project, env)
}
