package e2e

import (
	"bytes"
	"context"
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

func runDcraftProcess(t *testing.T, dir string, env []string, timeout time.Duration, args ...string) processResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return processResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func assertNoSentinelIn(t *testing.T, label, text string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if strings.Contains(text, sentinel) {
			t.Fatalf("%s leaked sentinel %q:\n%s", label, sentinel, text)
		}
	}
}

// scanSecretMetadata checks dcraft state metadata and the project config. The
// materialized files under <stack>/secrets and <stack>/build-secrets are the
// intentional delivery artifacts and are excluded; every other state file must
// be value-free.
func scanSecretMetadata(t *testing.T, stateDir, projectDir string, sentinels ...string) {
	t.Helper()
	err := filepath.Walk(stateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"secrets"+string(filepath.Separator)) ||
			strings.Contains(path, string(filepath.Separator)+"build-secrets"+string(filepath.Separator)) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		assertNoSentinelIn(t, "state file "+path, string(data), sentinels...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(projectDir, ".dcraft.yaml"),
		filepath.Join(projectDir, "compose.yaml"),
	} {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			assertNoSentinelIn(t, path, string(data), sentinels...)
		}
	}
}

func materializedFiles(t *testing.T, stateDir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(filepath.Join(stateDir, "stacks"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"secrets"+string(filepath.Separator)) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func dockerExec(t *testing.T, env []string, container, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s: %v\n%s", script, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestIntegrationRuntimeSecretsDeliveryLifetimeCleanupAndRedaction(t *testing.T) {
	env := requireIntegration(t)
	projectDir := t.TempDir()
	name := "dcraft-test-secrets-" + randomHex(t, 6)
	stateDir := t.TempDir()
	envSecret := "sentinel-env-" + randomHex(t, 8)
	fileSecret := "sentinel-file-" + randomHex(t, 8)
	composePath := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
`, name)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(projectDir, "secrets", "token.txt")
	if err := os.WriteFile(sourcePath, []byte(fileSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	composeHash := sha256File(t, composePath)
	sentinels := []string{envSecret, fileSecret}

	dcraftEnv := buildEnv(map[string]string{
		"DCRAFT_STATE_DIR":       stateDir,
		"DCRAFT_TEST_ENV_SECRET": envSecret,
	})
	run := func(timeout time.Duration, args ...string) processResult {
		t.Helper()
		res := runDcraftProcess(t, projectDir, dcraftEnv, timeout, args...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stdout", res.stdout, sentinels...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stderr", res.stderr, sentinels...)
		return res
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		down := composeCommand(ctx, env, projectDir, name, []string{composePath}, "down", "-v", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			t.Logf("cleanup down for %s failed: %v\n%s", name, err, out)
		}
	})

	addEnv := run(30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "environment", "--source", "env", "--ref", "DCRAFT_TEST_ENV_SECRET",
		"--target", "APP_SECRET", "-p", name, "--json")
	if addEnv.code != 0 {
		t.Fatalf("add env exit = %d\n%s\n%s", addEnv.code, addEnv.stdout, addEnv.stderr)
	}
	addFile := run(30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "file", "--source", "file", "--ref", "secrets/token.txt",
		"--target", "app_token", "-p", name, "--json")
	if addFile.code != 0 {
		t.Fatalf("add file exit = %d\n%s\n%s", addFile.code, addFile.stdout, addFile.stderr)
	}
	listed := parseSecrets(t, addFile.stdout)
	overridePath := listed.Data.Secrets.OverrideFile
	if overridePath == "" {
		t.Fatalf("managed override path missing: %+v", listed.Data.Secrets)
	}
	if data, err := os.ReadFile(overridePath); err != nil {
		t.Fatalf("read override: %v", err)
	} else {
		assertNoSentinelIn(t, overridePath, string(data), sentinels...)
	}

	up := run(180*time.Second, "up", "-p", name, "--json")
	if up.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	if sha256File(t, composePath) != composeHash {
		t.Fatal("existing Compose file was modified")
	}
	ids := containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers = %v, want 1", ids)
	}
	container := ids[0]
	if got := dockerExec(t, env, container, `printf %s "$APP_SECRET"`); got != envSecret {
		t.Fatalf("environment secret in container = %q, want the env source", got)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecret {
		t.Fatalf("file secret in container = %q, want the file source", got)
	}
	if got := dockerExec(t, env, container, `stat -c '%a' /run/secrets/app_token`); got != "600" {
		t.Fatalf("file secret mode = %q, want 600", got)
	}

	// docker restart and a no-change compose up must survive: the file is a
	// live bind mount and must still exist.
	restartCtx, restartCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer restartCancel()
	if out, err := exec.CommandContext(restartCtx, "docker", "restart", container).CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v\n%s", err, out)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecret {
		t.Fatalf("file secret after docker restart = %q", got)
	}
	upNoChange := run(120*time.Second, "up", "-p", name, "--json")
	if upNoChange.code != 0 {
		t.Fatalf("no-change up exit = %d\n%s", upNoChange.code, upNoChange.stderr)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecret {
		t.Fatalf("file secret after no-change up = %q", got)
	}

	// Force-recreate with the documented effective inputs (user file + managed
	// override) and the Compose-process environment value.
	forceEnv := buildEnv(map[string]string{"DCRAFT_SECRET_1": envSecret})
	forceCtx, forceCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer forceCancel()
	force := composeCommand(forceCtx, forceEnv, projectDir, name, []string{composePath, overridePath}, "up", "-d", "--force-recreate")
	if out, err := force.CombinedOutput(); err != nil {
		t.Fatalf("force-recreate: %v\n%s", err, out)
	}
	ids = containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers after force-recreate = %v", ids)
	}
	container = ids[0]
	if got := dockerExec(t, env, container, `printf %s "$APP_SECRET"`); got != envSecret {
		t.Fatalf("environment secret after force-recreate = %q", got)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecret {
		t.Fatalf("file secret after force-recreate = %q", got)
	}

	scanSecretMetadata(t, stateDir, projectDir, sentinels...)
	beforeRemove := materializedFiles(t, stateDir)
	if len(beforeRemove) == 0 {
		t.Fatal("file delivery was not materialized under the state directory")
	}
	remove := run(30*time.Second, "secrets", "--remove", "-s", "web",
		"--delivery", "file", "--target", "app_token", "-p", name, "--json")
	if remove.code != 0 {
		t.Fatalf("remove file exit = %d\n%s", remove.code, remove.stderr)
	}
	afterRemove := materializedFiles(t, stateDir)
	if len(afterRemove) == 0 {
		t.Fatalf("removal deleted the file before a safe point; a running container could no longer restart")
	}
	// The removed reference must not break an existing container restart. The
	// file stays until a successful up recreates the service without it or the
	// stack is torn down.
	restartCtx2, restartCancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer restartCancel2()
	if out, err := exec.CommandContext(restartCtx2, "docker", "restart", container).CombinedOutput(); err != nil {
		t.Fatalf("docker restart after removal: %v\n%s", err, out)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecret {
		t.Fatalf("file secret after removal+restart = %q", got)
	}
	// A successful whole-stack up is the safe cleanup point: the service is
	// recreated without the removed secret and the stale file is removed.
	upAfterRemove := run(120*time.Second, "up", "-p", name, "--json")
	if upAfterRemove.code != 0 {
		t.Fatalf("up after removal exit = %d\n%s", upAfterRemove.code, upAfterRemove.stderr)
	}
	if files := materializedFiles(t, stateDir); len(files) != 0 {
		t.Fatalf("successful up left removed-reference files: %v", files)
	}
	ids = containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers after removal+up = %v", ids)
	}
	container = ids[0]
	if got := dockerExec(t, env, container, `printf %s "$APP_SECRET"`); got != envSecret {
		t.Fatalf("environment secret after removal+up = %q", got)
	}
	fileCtx, fileCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fileCancel()
	fileCheck := exec.CommandContext(fileCtx, "docker", "exec", container, "sh", "-c", "test ! -e /run/secrets/app_token")
	if out, err := fileCheck.CombinedOutput(); err != nil {
		t.Fatalf("recreated container still exposes the removed file: %v\n%s", err, out)
	}

	down := run(120*time.Second, "down", "-p", name, "--json")
	if down.code != 0 {
		t.Fatalf("down exit = %d\n%s", down.code, down.stderr)
	}
	if files := materializedFiles(t, stateDir); len(files) != 0 {
		t.Fatalf("stack teardown left materialized files: %v", files)
	}
	if _, err := os.Stat(overridePath); !os.IsNotExist(err) {
		t.Fatalf("stack teardown left the managed override: %v", err)
	}
	if ids := containerIDs(t, env, projectDir, name, []string{composePath}); len(ids) != 0 {
		t.Fatalf("stack teardown left containers: %v", ids)
	}
	scanSecretMetadata(t, stateDir, projectDir, sentinels...)
}

func TestIntegrationRuntimeSecretsMissingSourceLeavesNoOrphan(t *testing.T) {
	env := requireIntegration(t)
	projectDir := t.TempDir()
	name := "dcraft-test-secrets-missing-" + randomHex(t, 6)
	stateDir := t.TempDir()
	composePath := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 60"]
`, name)), 0o644); err != nil {
		t.Fatal(err)
	}
	composeHash := sha256File(t, composePath)
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		down := composeCommand(ctx, env, projectDir, name, []string{composePath}, "down", "-v", "--remove-orphans")
		_, _ = down.CombinedOutput()
	})

	add := runDcraftProcess(t, projectDir, dcraftEnv, 30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "file", "--source", "file", "--ref", "missing-token.txt",
		"--target", "missing_token", "-p", name, "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d\n%s\n%s", add.code, add.stdout, add.stderr)
	}
	up := runDcraftProcess(t, projectDir, dcraftEnv, 120*time.Second, "up", "-p", name, "--json")
	if up.code != 1 {
		t.Fatalf("up exit = %d, want 1\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	parsed := parseStatus(t, up.stdout)
	if !hasCategory(parsed, core.CategorySecretSource) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	if files := materializedFiles(t, stateDir); len(files) != 0 {
		t.Fatalf("failed run left orphan materialized files: %v", files)
	}
	if ids := containerIDs(t, env, projectDir, name, []string{composePath}); len(ids) != 0 {
		t.Fatalf("failed run left containers: %v", ids)
	}
	if sha256File(t, composePath) != composeHash {
		t.Fatal("Compose file changed on failure")
	}
	scanSecretMetadata(t, stateDir, projectDir)
}

// Restart is not a configuration update: it must reuse the materialized file
// bytes instead of applying a rotated source. A missing required file fails
// before the containers are touched and points to `up`.
func TestIntegrationRestartReusesFileSecretAndFailsWhenMissing(t *testing.T) {
	env := requireIntegration(t)
	projectDir := t.TempDir()
	name := "dcraft-test-restart-secret-" + randomHex(t, 6)
	stateDir := t.TempDir()
	fileSecretV1 := "sentinel-restart-v1-" + randomHex(t, 8)
	fileSecretV2 := "sentinel-restart-v2-" + randomHex(t, 8)
	composePath := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
`, name)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(projectDir, "secrets", "token.txt")
	if err := os.WriteFile(sourcePath, []byte(fileSecretV1+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	sentinels := []string{fileSecretV1, fileSecretV2}
	run := func(timeout time.Duration, args ...string) processResult {
		t.Helper()
		res := runDcraftProcess(t, projectDir, dcraftEnv, timeout, args...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stdout", res.stdout, sentinels...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stderr", res.stderr, sentinels...)
		return res
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		down := composeCommand(ctx, env, projectDir, name, []string{composePath}, "down", "-v", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			t.Logf("cleanup down for %s failed: %v\n%s", name, err, out)
		}
	})

	addFile := run(30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "file", "--source", "file", "--ref", "secrets/token.txt",
		"--target", "app_token", "-p", name, "--json")
	if addFile.code != 0 {
		t.Fatalf("add file exit = %d\n%s\n%s", addFile.code, addFile.stdout, addFile.stderr)
	}
	if up := run(180*time.Second, "up", "-p", name, "--json"); up.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	ids := containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers = %v, want 1", ids)
	}
	container := ids[0]
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecretV1 {
		t.Fatalf("file secret before restart = %q", got)
	}

	if err := os.WriteFile(sourcePath, []byte(fileSecretV2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restart := run(60*time.Second, "restart", "-p", name, "--json")
	if restart.code != 0 {
		t.Fatalf("restart exit = %d\n%s\n%s", restart.code, restart.stdout, restart.stderr)
	}
	if got := containerIDs(t, env, projectDir, name, []string{composePath}); len(got) != 1 || got[0] != container {
		t.Fatalf("restart recreated containers: %v -> %v", ids, got)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecretV1 {
		t.Fatalf("restart applied the rotated source: %q, want %q", got, fileSecretV1)
	}

	// `up` is the configuration path and does apply the rotated value.
	if up := run(120*time.Second, "up", "-p", name, "--json"); up.code != 0 {
		t.Fatalf("second up exit = %d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	if got := dockerExec(t, env, container, `cat /run/secrets/app_token`); got != fileSecretV2 {
		t.Fatalf("up did not apply the rotated source: %q", got)
	}

	files := materializedFiles(t, stateDir)
	if len(files) != 1 {
		t.Fatalf("materialized files = %v, want 1", files)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	failed := run(60*time.Second, "restart", "-p", name, "--json")
	if failed.code != 1 {
		t.Fatalf("restart without the required file exit = %d, want 1\n%s\n%s", failed.code, failed.stdout, failed.stderr)
	}
	if !strings.Contains(failed.stdout, "dcraft up") && !strings.Contains(failed.stderr, "dcraft up") {
		t.Fatalf("missing-file guidance does not point to up:\n%s\n%s", failed.stdout, failed.stderr)
	}
	if got := containerIDs(t, env, projectDir, name, []string{composePath}); len(got) != 1 || got[0] != container {
		t.Fatalf("failed restart touched containers: %v -> %v", container, got)
	}
}
