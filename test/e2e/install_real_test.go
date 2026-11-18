package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealComposeInstall is the guarded end-to-end verification against the
// real Docker Compose release source. It installs into a temporary
// DOCKER_CONFIG plugin directory and never touches the user's real Compose.
// It is skipped in the default test run because it downloads the official
// binary from the network.
func TestRealComposeInstall(t *testing.T) {
	if os.Getenv("DCRAFT_COMPOSE_REAL_INSTALL") != "1" {
		t.Skip("set DCRAFT_COMPOSE_REAL_INSTALL=1 to download and verify the official Compose release")
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("real docker CLI is not available")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("user home directory is unknown")
	}
	homePlugin := filepath.Join(home, ".docker", "cli-plugins", "docker-compose")
	before := fileSignature(t, homePlugin)

	base := t.TempDir()
	configDir := filepath.Join(base, "docker-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(configDir, "cli-plugins")
	env := buildEnv(map[string]string{
		"PATH":                      filepath.Dir(dockerPath) + ":" + os.Getenv("PATH"),
		"DOCKER_CONFIG":             configDir,
		"DCRAFT_COMPOSE_PLUGIN_DIR": pluginDir,
		"DCRAFT_STATE_DIR":          filepath.Join(base, "state"),
	})
	project := t.TempDir() // install-compose must work without a Compose project
	res := runProcess(t, project, env, strings.NewReader(""), "install-compose", "--approve", "install", "--json")
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%q)\n%s", res.code, res.stderr, res.stdout)
	}
	parsed := parseInstallResult(t, res.stdout)
	info := parsed.Data.Install
	if !info.Changed || !info.ChecksumVerified || info.Version == "" {
		t.Fatalf("install result = %+v", info)
	}
	dest := filepath.Join(pluginDir, "docker-compose")
	if info.Destination != dest {
		t.Fatalf("destination = %q, want %q", info.Destination, dest)
	}
	fileInfo, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("installed plugin: %v", err)
	}
	if fileInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed plugin is not executable: %v", fileInfo.Mode())
	}
	if fileInfo.Size() != info.SizeBytes {
		t.Fatalf("installed size = %d, published %d", fileInfo.Size(), info.SizeBytes)
	}

	// Independent functional re-check through the real Docker CLI with the
	// temporary config, proving the installed plugin is what makes Compose
	// available in this isolated configuration.
	cmd := exec.Command(dockerPath, "compose", "version")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"DOCKER_CONFIG=" + configDir,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), info.Version) {
		t.Fatalf("docker compose version output %q does not match installed %q", out, info.Version)
	}

	after := fileSignature(t, homePlugin)
	if before != after {
		t.Fatalf("real user Compose was modified: before=%q after=%q", before, after)
	}
}

func fileSignature(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "absent"
		}
		t.Fatalf("lstat %s: %v", path, err)
	}
	sig := info.Mode().String()
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err == nil {
			sig += "->" + target
		}
	}
	return sig
}
