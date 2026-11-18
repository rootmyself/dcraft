package composeinstall

import (
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func envGetter(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestPluginDestinationHonorsExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	dest, err := pluginDestination(envGetter(map[string]string{"DCRAFT_COMPOSE_PLUGIN_DIR": dir}))
	if err != nil {
		t.Fatalf("pluginDestination: %v", err)
	}
	if dest != filepath.Join(dir, "docker-compose") {
		t.Fatalf("dest = %q", dest)
	}
}

func TestPluginDestinationUsesDockerConfigPluginDirectory(t *testing.T) {
	config := t.TempDir()
	dest, err := pluginDestination(envGetter(map[string]string{"DOCKER_CONFIG": config}))
	if err != nil {
		t.Fatalf("pluginDestination: %v", err)
	}
	if dest != filepath.Join(config, "cli-plugins", "docker-compose") {
		t.Fatalf("dest = %q", dest)
	}
}

func TestPluginDestinationFallsBackToUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dest, err := pluginDestination(envGetter(nil))
	if err != nil {
		t.Fatalf("pluginDestination: %v", err)
	}
	if dest != filepath.Join(home, ".docker", "cli-plugins", "docker-compose") {
		t.Fatalf("dest = %q", dest)
	}
}

func TestPluginDestinationRejectsRelativeOverrides(t *testing.T) {
	for _, values := range []map[string]string{
		{"DCRAFT_COMPOSE_PLUGIN_DIR": "relative/plugins"},
		{"DOCKER_CONFIG": "relative/config"},
	} {
		_, err := pluginDestination(envGetter(values))
		ce := asCoreError(t, err)
		if ce.Diagnostics[0].Category != core.CategoryComposeInstallManual {
			t.Fatalf("values %v category = %q", values, ce.Diagnostics[0].Category)
		}
		if len(ce.Diagnostics[0].NextActions) == 0 {
			t.Fatalf("values %v has no manual steps", values)
		}
	}
}
