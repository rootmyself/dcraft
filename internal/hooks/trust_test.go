package hooks_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/state"
)

func projectWithHook(t *testing.T, name, stage string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := script(t, dir, name)
	content := "hooks:\n  - path: " + name + "\n    stage: " + stage + "\n"
	write(t, filepath.Join(dir, ".dcraft.yaml"), content)
	return dir, path
}

func target(dir string) core.Target {
	return core.Target{ProjectDir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}}
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestPlanMarksFirstRunAndApprovalMakesItTrusted(t *testing.T) {
	dir, path := projectWithHook(t, "scripts/pre.sh", "pre")
	store := state.New(privateStateDir(t))
	svc := hooks.New(store, os.Getenv)

	plan, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("entries = %+v", plan.Entries)
	}
	entry := plan.Entries[0]
	if entry.Trusted || !plan.RequiresApproval || entry.Changed {
		t.Fatalf("first run was trusted: %+v / %+v", plan, entry)
	}
	if entry.SHA256 != shaOf(t, path) {
		t.Fatalf("sha = %q, want %q", entry.SHA256, shaOf(t, path))
	}
	if entry.Stage != core.HookStagePre || entry.Path != "scripts/pre.sh" {
		t.Fatalf("entry = %+v", entry)
	}

	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	if err := svc.Approve(context.Background(), target(dir), plan, at); err != nil {
		t.Fatal(err)
	}
	again, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !again.Entries[0].Trusted || again.RequiresApproval {
		t.Fatalf("approval was not recorded: %+v", again)
	}
}

func TestContentChangeInvalidatesThePreviousApproval(t *testing.T) {
	dir, path := projectWithHook(t, "scripts/pre.sh", "pre")
	svc := hooks.New(state.New(privateStateDir(t)), os.Getenv)
	plan, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(context.Background(), target(dir), plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	write(t, path, "#!/bin/sh\necho changed\n")
	changed, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Entries[0].Trusted || !changed.RequiresApproval {
		t.Fatalf("changed content reused the old approval: %+v", changed)
	}
	if !changed.Entries[0].Changed {
		t.Fatalf("changed flag not set: %+v", changed.Entries[0])
	}
	if changed.Entries[0].SHA256 == plan.Entries[0].SHA256 {
		t.Fatal("content identity did not change")
	}
}

func TestTrustIsKeyedByTargetAndStage(t *testing.T) {
	dirA, _ := projectWithHook(t, "scripts/pre.sh", "pre")
	dirB, _ := projectWithHook(t, "scripts/pre.sh", "pre")
	store := state.New(privateStateDir(t))
	svc := hooks.New(store, os.Getenv)
	plan, err := svc.Plan(context.Background(), target(dirA))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(context.Background(), target(dirA), plan, time.Now()); err != nil {
		t.Fatal(err)
	}

	other, err := svc.Plan(context.Background(), target(dirB))
	if err != nil {
		t.Fatal(err)
	}
	if other.Entries[0].Trusted {
		t.Fatal("approval in one project applied to another project")
	}

	// The same project and script at a different stage is a different effect
	// and needs its own approval.
	write(t, filepath.Join(dirA, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: post\n")
	post, err := svc.Plan(context.Background(), target(dirA))
	if err != nil {
		t.Fatal(err)
	}
	if post.Entries[0].Trusted {
		t.Fatal("approval for pre applied to post")
	}
}

func TestTrustRecordIsMachineLocalNotProjectShared(t *testing.T) {
	dir, _ := projectWithHook(t, "scripts/pre.sh", "pre")
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("chmod state dir: %v", err)
	}
	svc := hooks.New(state.New(stateDir), os.Getenv)
	plan, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(context.Background(), target(dir), plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "script-trust.json")); !os.IsNotExist(err) {
		t.Fatal("trust record was written into the project")
	}
	found := false
	_ = filepath.Walk(stateDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && strings.HasSuffix(path, "script-trust.json") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("trust record missing from the machine-local state directory")
	}
	cfg, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "sha256") || strings.Contains(string(cfg), "approved") {
		t.Fatalf("trust metadata leaked into the shared config:\n%s", cfg)
	}
}

func TestPlanRejectsUnusableScripts(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/missing.sh\n    stage: pre\n")
	svc := hooks.New(state.New(privateStateDir(t)), os.Getenv)
	_, err := svc.Plan(context.Background(), target(dir))
	hasCategory(t, err, core.CategoryHookInvalid)

	dir2 := t.TempDir()
	write(t, filepath.Join(dir2, "scripts/pre.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir2, "scripts/pre.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir2, ".dcraft.yaml"), "hooks:\n  - path: scripts/pre.sh\n    stage: pre\n")
	_, err = hooks.New(state.New(privateStateDir(t)), os.Getenv).Plan(context.Background(), target(dir2))
	hasCategory(t, err, core.CategoryHookInvalid)
}

func TestCorruptTrustRecordFailsSafely(t *testing.T) {
	dir, _ := projectWithHook(t, "scripts/pre.sh", "pre")
	store := state.New(privateStateDir(t))
	stackDir, err := store.StackDir(target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stackDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stackDir, "script-trust.json"), "{not json")
	_, err = hooks.New(store, os.Getenv).Plan(context.Background(), target(dir))
	hasCategory(t, err, core.CategoryStateFailed)
}

func TestTrustRecordFileIsPrivate(t *testing.T) {
	dir, _ := projectWithHook(t, "scripts/pre.sh", "pre")
	store := state.New(privateStateDir(t))
	svc := hooks.New(store, os.Getenv)
	plan, err := svc.Plan(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(context.Background(), target(dir), plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	stackDir, err := store.StackDir(target(dir))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(stackDir, "script-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("trust file permissions = %04o, want 0600", perm)
	}
}

func TestDescribeShowsRegistrationsWithoutRunningOrFailingOnMissingScripts(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".dcraft.yaml"), "hooks:\n  - path: scripts/missing.sh\n    stage: pre\n")
	views, err := hooks.New(state.New(privateStateDir(t)), os.Getenv).Describe(context.Background(), target(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Present || views[0].Trusted {
		t.Fatalf("views = %+v", views)
	}
	if views[0].Problem == "" {
		t.Fatalf("missing script problem not reported: %+v", views[0])
	}
}
