package composeinstall

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

const pluginBinaryName = "docker-compose"

// defaultPluginDir is the documented default Docker CLI plugin directory. It is
// used in guidance before the real destination is resolved.
func defaultPluginDir() string { return "~/.docker/cli-plugins" }

// pluginDestination resolves where the Compose CLI plugin belongs. The target
// is always a user-writable Docker CLI plugin directory:
//
//  1. DCRAFT_COMPOSE_PLUGIN_DIR (tests and explicit overrides; absolute only);
//  2. DOCKER_CONFIG/cli-plugins when DOCKER_CONFIG is set;
//  3. ~/.docker/cli-plugins.
//
// dcraft never targets system directories, never asks for administrator
// rights, and never writes to a Docker Desktop installation inside an
// application bundle.
func pluginDestination(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if dir := strings.TrimSpace(getenv("DCRAFT_COMPOSE_PLUGIN_DIR")); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeInstallManual,
				Severity:    core.SeverityError,
				Message:     msgPlatformUnsupported,
				Detail:      "DCRAFT_COMPOSE_PLUGIN_DIR must be an absolute path",
				NextActions: manualInstallActions(defaultPluginDir()),
			})
		}
		return filepath.Join(filepath.Clean(dir), pluginBinaryName), nil
	}
	if config := strings.TrimSpace(getenv("DOCKER_CONFIG")); config != "" {
		if !filepath.IsAbs(config) {
			return "", core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeInstallManual,
				Severity:    core.SeverityError,
				Message:     msgPlatformUnsupported,
				Detail:      "DOCKER_CONFIG must be an absolute path",
				NextActions: manualInstallActions(defaultPluginDir()),
			})
		}
		return filepath.Join(filepath.Clean(config), "cli-plugins", pluginBinaryName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryComposeInstallManual,
			Severity:    core.SeverityError,
			Message:     msgPlatformUnsupported,
			Detail:      "user home directory is unknown",
			NextActions: manualInstallActions(defaultPluginDir()),
		})
	}
	return filepath.Join(home, ".docker", "cli-plugins", pluginBinaryName), nil
}
