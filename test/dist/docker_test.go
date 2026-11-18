package dist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

const testImage = "alpine:3"

// requireDocker keeps these tests opt-in: they need an explicit flag and a
// working Docker engine. They create only short-lived --rm containers against
// a read-only bind mount of the freshly built artifact.
func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("DCRAFT_DOCKER_TEST") != "1" {
		t.Skip("set DCRAFT_DOCKER_TEST=1 to execute Linux artifacts in Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		t.Skip("Docker engine is not available")
	}
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", testImage).Run(); err != nil {
		t.Skipf("%s image is not present; run: docker pull %s", testImage, testImage)
	}
}

func dockerRun(t *testing.T, args ...string) processResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"run", "--rm"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("docker run: %v\nstderr: %s", err, stderr.String())
		}
		code = exitErr.ExitCode()
	}
	return processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func TestLinuxArtifactsExecuteInBareContainers(t *testing.T) {
	requireDocker(t)
	root := moduleRoot(t)
	dist := t.TempDir()
	buildDist(t, root, map[string]string{
		"VERSION":    testVersion,
		"COMMIT":     testCommit,
		"BUILD_DATE": testBuildDate,
		"DIST_DIR":   dist,
		"TARGETS":    "linux/amd64 linux/arm64",
	})

	empty := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(project, "compose.yaml"),
		[]byte("services:\n  web:\n    image: nginx:alpine\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	for _, arch := range []string{"amd64", "arm64"} {
		arch := arch
		t.Run("linux/"+arch, func(t *testing.T) {
			bin := filepath.Join(dist, "dcraft_"+testVersion+"_linux_"+arch)
			base := []string{
				"--platform", "linux/" + arch,
				"-v", bin + ":/dcraft:ro",
				"-v", empty + ":/empty:ro",
				"-v", project + ":/work:ro",
				testImage,
			}
			run := func(args ...string) processResult {
				return dockerRun(t, append(append([]string{}, base...), args...)...)
			}

			res := run("/dcraft", "--help")
			if res.code != 0 {
				t.Fatalf("--help exit = %d, stderr=%q", res.code, res.stderr)
			}
			for _, want := range []string{"사용법:", "사용 가능한 명령:", "status"} {
				if !strings.Contains(res.stdout, want) {
					t.Fatalf("--help missing %q:\n%s", want, res.stdout)
				}
			}

			wantVersion := fmt.Sprintf(
				"dcraft %s (commit %s, built %s, linux/%s, %s)\n",
				testVersion, testCommit, testBuildDate, arch, goToolchain(t),
			)
			res = run("/dcraft", "--version")
			if res.code != 0 || res.stdout != wantVersion || res.stderr != "" {
				t.Fatalf("--version: exit=%d stdout=%q stderr=%q, want %q",
					res.code, res.stdout, res.stderr, wantVersion)
			}

			res = run("/dcraft", "status", "--path", "/empty", "--json")
			if res.code != 1 {
				t.Fatalf("empty status exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
			}
			if res.stderr != "" {
				t.Fatalf("empty status JSON wrote stderr: %q", res.stderr)
			}
			var emptyStatus struct {
				Outcome     core.Outcome      `json:"outcome"`
				Diagnostics []core.Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal([]byte(res.stdout), &emptyStatus); err != nil {
				t.Fatalf("parse empty status JSON: %v\n%s", err, res.stdout)
			}
			if emptyStatus.Outcome != core.OutcomeFailure || !hasCategory(emptyStatus.Diagnostics, core.CategoryNoConfiguration) {
				t.Fatalf("empty status = %+v", emptyStatus)
			}

			// A real Compose file and no Docker CLI inside the container: the
			// diagnostic must name the external prerequisite, not a generic
			// failure, and must not invent an engine connection.
			res = run("/dcraft", "status", "--path", "/work", "--json")
			if res.code != 1 {
				t.Fatalf("configured status exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
			}
			var configuredStatus struct {
				Outcome     core.Outcome      `json:"outcome"`
				Diagnostics []core.Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal([]byte(res.stdout), &configuredStatus); err != nil {
				t.Fatalf("parse configured status JSON: %v\n%s", err, res.stdout)
			}
			if !hasCategory(configuredStatus.Diagnostics, core.CategoryDockerMissing) {
				t.Fatalf("configured status diagnostics = %+v", configuredStatus.Diagnostics)
			}

			res = run("/dcraft")
			if res.code != 2 {
				t.Fatalf("non-interactive no-args exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
			}
			if strings.Contains(res.stdout, "\x1b[") {
				t.Fatalf("non-interactive invocation rendered TUI escapes: %q", res.stdout)
			}
			if !strings.Contains(res.stderr, "대화형 터미널이 아니어서") {
				t.Fatalf("non-interactive stderr = %q", res.stderr)
			}

			// The base image deliberately has no Python, Node or Go. If any of
			// them appears, fail before running dcraft.
			res = run("/bin/sh", "-c",
				`for tool in python3 node go; do
					if command -v "$tool" >/dev/null 2>&1; then
						echo "unexpected runtime: $tool" >&2
						exit 1
					fi
				done
				/dcraft --version`)
			if res.code != 0 || res.stdout != wantVersion {
				t.Fatalf("bare-container check: exit=%d stdout=%q stderr=%q, want %q",
					res.code, res.stdout, res.stderr, wantVersion)
			}
		})
	}
}

func hasCategory(diagnostics []core.Diagnostic, category core.Category) bool {
	for _, d := range diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}
