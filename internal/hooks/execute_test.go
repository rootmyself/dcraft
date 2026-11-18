package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/state"
)

func planFor(t *testing.T, svc *hooks.Service, dir string) core.HookPlan {
	t.Helper()
	plan, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) == 0 {
		t.Fatal("plan has no entries")
	}
	return plan
}

func execute(t *testing.T, svc *hooks.Service, dir string, entry core.HookEntry, action string) (core.HookExecution, error) {
	t.Helper()
	return svc.Execute(context.Background(), core.HookRequest{
		Action: action, OperationID: "op_test", Target: target(dir), Entry: entry,
	})
}

func TestExecuteRunsInTheProjectFolderWithDocumentedContext(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "scripts/pre.sh"), `#!/bin/sh
printf 'pwd=%s\n' "$PWD"
printf 'action=%s\n' "$DCRAFT_ACTION"
printf 'stage=%s\n' "$DCRAFT_HOOK_STAGE"
printf 'sha=%s\n' "$DCRAFT_HOOK_SHA256"
printf 'path=%s\n' "$DCRAFT_HOOK_PATH"
printf 'project=%s\n' "$DCRAFT_PROJECT_DIR"
printf 'operation=%s\n' "$DCRAFT_OPERATION_ID"
printf 'inherit=%s\n' "$HOOK_INHERITED"
printf 'stdout-line\n'
printf 'stderr-line\n' >&2
`)
	if err := os.Chmod(filepath.Join(dir, "scripts/pre.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	t.Setenv("HOOK_INHERITED", "inherited-value")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	plan := planFor(t, svc, dir)
	if err := svc.Approve(context.Background(), target(dir), plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	exec, err := execute(t, svc, dir, plan.Entries[0], "up")
	if err != nil {
		t.Fatal(err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exit = %d", exec.ExitCode)
	}
	resolved := filepath.Join(dir, "scripts/pre.sh")
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalScript, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pwd=" + canonicalDir,
		"action=up",
		"stage=pre",
		"sha=" + plan.Entries[0].SHA256,
		"path=" + canonicalScript,
		"project=" + dir,
		"operation=op_test",
		"inherit=inherited-value",
		"stdout-line",
		"stderr-line",
	} {
		if !strings.Contains(exec.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, exec.Output)
		}
	}
}

func TestExecuteRefusesContentChangedAfterApproval(t *testing.T) {
	dir, path := projectWithHook(t, "scripts/pre.sh", "pre")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	plan := planFor(t, svc, dir)
	if err := svc.Approve(context.Background(), target(dir), plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	write(t, path, "#!/bin/sh\n touch "+filepath.Join(dir, "ran")+"\n")
	_, err := execute(t, svc, dir, plan.Entries[0], "up")
	hasCategory(t, err, core.CategoryHookContentChanged)
	if _, statErr := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(statErr) {
		t.Fatal("changed script executed")
	}
}

func TestExecuteRefusesNonExecutableScriptWithGuidance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripts/pre.sh")
	write(t, path, "#!/bin/sh\nexit 0\n")
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	sha := shaOf(t, path)
	entry := core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: sha}
	_, err := execute(t, svc, dir, entry, "up")
	hasCategory(t, err, core.CategoryHookInvalid)
	assertMentionsGuidance(t, err)
}

func TestExecuteRefusesAFileWithoutAWorkingInterpreter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripts/pre.sh")
	write(t, path, "not an executable format\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	entry := core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: shaOf(t, path)}
	_, err := execute(t, svc, dir, entry, "up")
	hasCategory(t, err, core.CategoryHookInvalid)
	assertMentionsGuidance(t, err)
}

func assertMentionsGuidance(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	text := err.Error()
	if !strings.Contains(text, "권한") && !strings.Contains(text, "셔뱅") && !strings.Contains(text, "해석기") {
		t.Fatalf("error has no executable/shebang guidance: %v", err)
	}
}

func TestExecuteReturnsNonZeroExitWithoutRunningTheRest(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "scripts/pre.sh"), "#!/bin/sh\necho before-failure\nexit 7\n")
	if err := os.Chmod(filepath.Join(dir, "scripts/pre.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	plan := planFor(t, svc, dir)
	exec, err := execute(t, svc, dir, plan.Entries[0], "update")
	if err != nil {
		t.Fatal(err)
	}
	if exec.ExitCode != 7 {
		t.Fatalf("exit = %d", exec.ExitCode)
	}
	if !strings.Contains(exec.Output, "before-failure") {
		t.Fatalf("output = %q", exec.Output)
	}
}

func TestExecuteCancellationKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "finished")
	write(t, filepath.Join(dir, "scripts/pre.sh"), "#!/bin/sh\nsleep 5\ntouch "+marker+"\n")
	if err := os.Chmod(filepath.Join(dir, "scripts/pre.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	plan := planFor(t, svc, dir)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := svc.Execute(ctx, core.HookRequest{
			Action: "up", OperationID: "op", Target: target(dir), Entry: plan.Entries[0],
		})
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled hook returned nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled hook did not return; process group was not terminated")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("script continued after cancellation")
	}
}

func TestExecuteRedactsKnownEnvironmentSecretValues(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".dcraft.yaml"), `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: DCRAFT_HOOK_TEST_SECRET
hooks:
  - path: scripts/pre.sh
    stage: pre
`)
	write(t, filepath.Join(dir, "scripts/pre.sh"), "#!/bin/sh\necho value=$DCRAFT_HOOK_TEST_SECRET\n")
	if err := os.Chmod(filepath.Join(dir, "scripts/pre.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCRAFT_HOOK_TEST_SECRET", "super-secret-value-42")
	svc := hooks.New(state.New(t.TempDir()), os.Getenv)
	plan := planFor(t, svc, dir)
	exec, err := execute(t, svc, dir, plan.Entries[0], "up")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exec.Output, "super-secret-value-42") {
		t.Fatalf("secret value leaked: %q", exec.Output)
	}
	if !strings.Contains(exec.Output, "[REDACTED]") {
		t.Fatalf("output = %q", exec.Output)
	}
}
