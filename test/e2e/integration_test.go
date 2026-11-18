package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

func requireIntegration(t *testing.T) []string {
	t.Helper()
	if os.Getenv("DCRAFT_INTEGRATION") != "1" {
		t.Skip("set DCRAFT_INTEGRATION=1 to run real Docker integration tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	env := buildEnv(nil)
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker engine not reachable, skipping real integration: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose not available: %v\n%s", err, out)
	}
	return env
}

func TestIntegrationRealStackStatusAndTUI(t *testing.T) {
	env := requireIntegration(t)
	projectDir := t.TempDir()
	name := "dcraft-test-" + randomHex(t, 6)
	stateDir := t.TempDir()

	base := fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "180"]
    ports:
      - "127.0.0.1::80"
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
      timeout: 1s
      retries: 5
  worker:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "180"]
  probe:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "180"]
    profiles: ["tools"]
`, name)
	override := `services:
  extra:
    image: busybox:${DCRAFT_TEST_TAG:-1.36}
    command: ["sleep", "180"]
`
	basePath := filepath.Join(projectDir, "compose.yaml")
	overridePath := filepath.Join(projectDir, "compose.override.yaml")
	envPath := filepath.Join(projectDir, ".env")
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("DCRAFT_TEST_TAG=1.36\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseHash := sha256File(t, basePath)
	overrideHash := sha256File(t, overridePath)
	files := []string{basePath, overridePath}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		down := composeCommand(ctx, env, projectDir, name, files, "--profile", "tools", "down", "-v", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			t.Logf("cleanup down for %s failed: %v\n%s", name, err, out)
		} else if out := strings.TrimSpace(string(out)); out != "" {
			t.Logf("cleanup down for %s: %s", name, out)
		}
	})

	upCtx, cancelUp := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancelUp()
	up := composeCommand(upCtx, env, projectDir, name, files, "--profile", "tools", "up", "-d", "--wait", "--wait-timeout", "90")
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("compose up failed: %v\n%s", err, out)
	}

	idsBefore := containerIDs(t, env, projectDir, name, files)
	if len(idsBefore) != 4 {
		t.Fatalf("containers = %v, want 4", idsBefore)
	}
	for _, id := range idsBefore {
		assertRunning(t, id)
	}

	dcraftEnv := buildEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir})
	res := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--json")
	if res.code != 0 {
		t.Fatalf("dcraft status exit = %d, stderr=%q", res.code, res.stderr)
	}
	parsed := parseStatus(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q diagnostics=%+v", parsed.Outcome, parsed.Diagnostics)
	}
	if parsed.Data.Stack.ProjectName != name {
		t.Fatalf("project name = %q, want %q", parsed.Data.Stack.ProjectName, name)
	}
	if len(parsed.Target.Files) != 2 ||
		!samePath(t, parsed.Target.Files[0], basePath) ||
		!samePath(t, parsed.Target.Files[1], overridePath) {
		t.Fatalf("discovered files = %v, want base then override", parsed.Target.Files)
	}
	if !samePath(t, parsed.Target.DefaultEnvFile, envPath) {
		t.Fatalf("default env file = %q, want %q", parsed.Target.DefaultEnvFile, envPath)
	}
	if len(parsed.Target.Profiles) != 0 {
		t.Fatalf("unexpected profiles: %v", parsed.Target.Profiles)
	}

	snap := parsed.Data.Stack
	if snap.Summary.Services != 4 || snap.Summary.Running != 4 || snap.Summary.Instances != 4 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
	web := findService(t, snap, "web")
	worker := findService(t, snap, "worker")
	extra := findService(t, snap, "extra")
	probe := findService(t, snap, "probe")
	if web.Image != "busybox:1.36" {
		t.Fatalf("web image = %q, want env-file-resolved busybox:1.36", web.Image)
	}
	if web.State != stack.StateRunning || !web.AppVerified || web.Health != stack.HealthHealthy {
		t.Fatalf("web = %+v, want running healthy verified", web)
	}
	if worker.AppVerified || worker.Health != stack.HealthNone {
		t.Fatalf("worker = %+v, want running unverified", worker)
	}
	if extra.State != stack.StateRunning || extra.AppVerified {
		t.Fatalf("override service extra = %+v", extra)
	}
	if probe.Configured {
		t.Fatalf("profile service probe should be unconfigured without --profile: %+v", probe)
	}
	if probe.State != stack.StateRunning {
		t.Fatalf("probe container should still be visible: %+v", probe)
	}
	var published int
	for _, c := range web.Connections {
		if c.Source == stack.SourceRuntime && c.TargetPort == 80 {
			published = c.PublishedPort
		}
	}
	if published <= 0 {
		t.Fatalf("web connections = %+v, want a published runtime port", web.Connections)
	}

	profileRes := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""), "status", "--profile", "tools", "--json")
	if profileRes.code != 0 {
		t.Fatalf("profile status exit = %d, stderr=%q", profileRes.code, profileRes.stderr)
	}
	profileParsed := parseStatus(t, profileRes.stdout)
	if len(profileParsed.Target.Profiles) != 1 || profileParsed.Target.Profiles[0] != "tools" {
		t.Fatalf("target profiles = %v", profileParsed.Target.Profiles)
	}
	if !findService(t, profileParsed.Data.Stack, "probe").Configured {
		t.Fatal("probe is not configured with --profile tools")
	}

	explicitRes := runProcess(t, projectDir, dcraftEnv, strings.NewReader(""),
		"status", "-f", basePath, "-f", overridePath, "--json")
	if explicitRes.code != 0 {
		t.Fatalf("explicit file status exit = %d, stderr=%q", explicitRes.code, explicitRes.stderr)
	}
	explicitParsed := parseStatus(t, explicitRes.stdout)
	if len(explicitParsed.Target.Files) != 2 ||
		!samePath(t, explicitParsed.Target.Files[0], basePath) ||
		!samePath(t, explicitParsed.Target.Files[1], overridePath) {
		t.Fatalf("explicit file order = %v", explicitParsed.Target.Files)
	}

	if after := sha256File(t, basePath); after != baseHash {
		t.Fatal("compose.yaml was modified by status")
	}
	if after := sha256File(t, overridePath); after != overrideHash {
		t.Fatal("compose.override.yaml was modified by status")
	}

	output, code := runPTY(t, projectDir, dcraftEnv, name)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"dcraft", "스택 상태", "web", "worker", "extra", "probe", "q 종료"} {
		if !strings.Contains(output, want) {
			t.Fatalf("TUI output missing %q:\n%s", want, output)
		}
	}

	idsAfter := containerIDs(t, env, projectDir, name, files)
	if strings.Join(idsBefore, ",") != strings.Join(idsAfter, ",") {
		t.Fatalf("containers changed after TUI: before=%v after=%v", idsBefore, idsAfter)
	}
	for _, id := range idsAfter {
		assertRunning(t, id)
	}
	if after := sha256File(t, basePath); after != baseHash {
		t.Fatal("compose.yaml was modified by the TUI")
	}
	if after := sha256File(t, overridePath); after != overrideHash {
		t.Fatal("compose.override.yaml was modified by the TUI")
	}

	// Automatic discovery must not walk up into the running stack.
	child := filepath.Join(projectDir, "nested", "deeper")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	parentRes := runProcess(t, child, dcraftEnv, strings.NewReader(""), "status", "--json")
	if parentRes.code != 1 {
		t.Fatalf("nested status exit = %d, want 1", parentRes.code)
	}
	parentParsed := parseStatus(t, parentRes.stdout)
	if !hasCategory(parentParsed, core.CategoryNoConfiguration) {
		t.Fatalf("nested diagnostics = %+v", parentParsed.Diagnostics)
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func composeCommand(ctx context.Context, env []string, dir, name string, files []string, args ...string) *exec.Cmd {
	cmdArgs := []string{"compose", "-p", name}
	for _, f := range files {
		cmdArgs = append(cmdArgs, "-f", f)
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

func containerIDs(t *testing.T, env []string, dir, name string, files []string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := composeCommand(ctx, env, dir, name, files, "ps", "--all", "-q")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose ps: %v", err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

func assertRunning(t *testing.T, id string) {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", id, err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("container %s is not running", id)
	}
}

func findService(t *testing.T, snap stack.Snapshot, name string) stack.Service {
	t.Helper()
	for _, s := range snap.Services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("service %q not found in %+v", name, snap.Services)
	return stack.Service{}
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}
