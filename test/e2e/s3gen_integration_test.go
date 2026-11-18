package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// This integration test uses a PATH "aws" shim, not real S3 and not real AWS
// credentials: the shim copies a local fixture tree on `s3 sync`. The rest of
// the journey (hook registration, independent script trust, Compose build and
// container) is a real isolated Docker stack.
func TestIntegrationGeneratedS3HookFeedsTheBuildInputAndRequiresTrust(t *testing.T) {
	env := requireIntegration(t)
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "app.txt"), []byte("s3-sync\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM busybox:1.36\nCOPY app.txt /app/app.txt\nCMD [\"sleep\", \"600\"]\n"
	if err := os.WriteFile(filepath.Join(fixture, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}

	shimDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "aws.log")
	shim := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${AWS_SHIM_LOG:?}"
case "$1 $2" in
  "s3 sync")
    mkdir -p "$4"
    cp -R "${AWS_SHIM_SYNC_FIXTURE:?}"/. "$4"/
    ;;
  *)
    echo "aws shim: unexpected invocation: $*" >&2
    exit 9
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	projectName := "dcraft-s3gen-" + randomHex(t, 6)
	compose := fmt.Sprintf(`name: %s
services:
  app:
    build:
      context: ./src
    image: %s:test
    command: ["sleep", "600"]
`, projectName, projectName)
	composePath := filepath.Join(project, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupLifecycleCompose(t, env, project, projectName, composePath, true) })
	state := t.TempDir()
	dcraftEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":      state,
		"PATH":                  shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"AWS_SHIM_LOG":          log,
		"AWS_SHIM_SYNC_FIXTURE": fixture,
	})

	generated := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"generate-s3-hook",
		"--source", "s3://bucket/deploy",
		"--mode", "sync",
		"--dest", "src",
		"--output", "scripts/s3-sync.sh",
		"--approve", "file_overwrite",
		"--json",
	)
	if generated.code != 0 {
		t.Fatalf("generate exit = %d stderr=%q stdout=%s", generated.code, generated.stderr, generated.stdout)
	}
	scriptPath := filepath.Join(project, "scripts", "s3-sync.sh")
	if info, err := os.Stat(scriptPath); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("generated script = %v, %v", info, err)
	}

	register := runProcess(t, project, dcraftEnv, strings.NewReader(""),
		"hooks", "--add", "--stage", "pre", "--script", "scripts/s3-sync.sh", "--json")
	if register.code != 0 {
		t.Fatalf("hooks add exit = %d stderr=%q stdout=%s", register.code, register.stderr, register.stdout)
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); err != nil {
		t.Fatalf("registration missing: %v", err)
	}

	// Registration is not trust: the pre-hook must not run and no stack or
	// destination may be created.
	untrusted := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json")
	if untrusted.code != 2 {
		t.Fatalf("untrusted up exit = %d stderr=%q stdout=%s", untrusted.code, untrusted.stderr, untrusted.stdout)
	}
	untrustedRes := parseAnyResult(t, untrusted.stdout)
	if !hasResultCategory(untrustedRes, core.CategoryApprovalRequired) {
		t.Fatalf("untrusted diagnostics = %+v", untrustedRes.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(project, "src")); !os.IsNotExist(err) {
		t.Fatalf("untrusted hook created the destination: %v", err)
	}
	if ids := strings.TrimSpace(composeInspectAll(t, env, projectName, composePath)); ids != "" {
		t.Fatalf("untrusted up changed the stack: %s", ids)
	}

	// A file that only exists locally must survive the non-deleting sync.
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "stale.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	up := runProcess(t, project, dcraftEnv, strings.NewReader(""), "up", "--json", "--approve", "script_trust")
	if up.code != 0 {
		t.Fatalf("up exit = %d stderr=%q stdout=%s", up.code, up.stderr, up.stdout)
	}
	if data, err := os.ReadFile(filepath.Join(project, "src", "app.txt")); err != nil || string(data) != "s3-sync\n" {
		t.Fatalf("pre-hook sync = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(project, "src", "stale.txt")); err != nil || string(data) != "keep\n" {
		t.Fatalf("stale destination file was deleted: %q, %v", data, err)
	}
	idsAfterUp := strings.TrimSpace(composeInspectAll(t, env, projectName, composePath))
	if idsAfterUp == "" {
		t.Fatal("up created no containers")
	}
	if got := s3genExec(t, env, project, projectName, composePath); got != "s3-sync" {
		t.Fatalf("image was not built from the pre-hook destination: %q", got)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("aws shim was not called: %v", err)
	}
	joined := string(calls)
	if !strings.Contains(joined, "s3 sync s3://bucket/deploy") {
		t.Fatalf("sync call missing:\n%s", joined)
	}
	if strings.Contains(joined, "--delete") {
		t.Fatalf("sync used --delete:\n%s", joined)
	}
}

func s3genExec(t *testing.T, env []string, dir, project, file string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := composeCommand(ctx, env, dir, project, []string{file}, "exec", "-T", "app", "cat", "/app/app.txt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compose exec: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
