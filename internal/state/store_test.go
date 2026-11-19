package state_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

func getenv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestRootUsesDCRAFTStateDirOverride(t *testing.T) {
	dir := t.TempDir()
	root, err := state.Root(getenv(map[string]string{"DCRAFT_STATE_DIR": dir}))
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root != dir {
		t.Fatalf("root = %q, want %q", root, dir)
	}
}

func TestRootDefaultsUnderUserConfigDir(t *testing.T) {
	root, err := state.Root(getenv(nil))
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	userConfig, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(userConfig, "dcraft") {
		t.Fatalf("root = %q, want %q", root, filepath.Join(userConfig, "dcraft"))
	}
}

func TestStackDirIsStableAndEnvironmentSeparated(t *testing.T) {
	store := state.New(privateStateDir(t))
	dev := core.Target{ProjectDir: "/work/hello-world", Environment: ""}
	staging := core.Target{ProjectDir: "/work/hello-world", Environment: "staging"}

	devDir, err := store.StackDir(dev)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := store.StackDir(dev)
	if devDir != again {
		t.Fatalf("stack dir not stable: %q vs %q", devDir, again)
	}
	stagingDir, _ := store.StackDir(staging)
	if devDir == stagingDir {
		t.Fatalf("environments share state dir %q", devDir)
	}
	for _, dir := range []string{devDir, stagingDir} {
		rel, err := filepath.Rel(store.Root(), dir)
		if err != nil || strings.Contains(rel, "..") {
			t.Fatalf("stack dir %q outside root %q", dir, store.Root())
		}
	}
	if !strings.Contains(filepath.Base(devDir), "hello-world") {
		t.Fatalf("stack dir %q should stay recognizable", devDir)
	}
}

func TestDifferentFoldersWithSameNameDoNotShareState(t *testing.T) {
	store := state.New(privateStateDir(t))
	a, _ := store.StackDir(core.Target{ProjectDir: "/work/a/hello"})
	b, _ := store.StackDir(core.Target{ProjectDir: "/work/b/hello"})
	if a == b {
		t.Fatalf("same-named folders share state: %q", a)
	}
	if !strings.Contains(a, "hello") || !strings.Contains(b, "hello") {
		t.Fatalf("keys not readable: %q %q", a, b)
	}
}

func TestStackDirDoesNotCreateAnything(t *testing.T) {
	root := privateStateDir(t)
	store := state.New(root)
	dir, err := store.StackDir(core.Target{ProjectDir: "/work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("StackDir created %q: %v", dir, err)
	}
}

func TestStackDirRequiresProjectDir(t *testing.T) {
	store := state.New(privateStateDir(t))
	if _, err := store.StackDir(core.Target{}); err == nil {
		t.Fatal("empty target accepted")
	}
}

func TestAppendOperationWritesSeparateJSONLinesWithPrivatePermissions(t *testing.T) {
	root := privateStateDir(t)
	store := state.New(root)
	target := core.Target{ProjectDir: "/work/app", Environment: "prod"}
	started := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	rec := core.OperationRecord{
		OperationID: "op_1",
		Action:      "update",
		Outcome:     core.OutcomeSuccess,
		StartedAt:   started,
		FinishedAt:  started.Add(time.Minute),
		Stages: []core.Stage{{
			Name: "execute", Title: "작업 실행", Status: core.StageSucceeded, StartedAt: started,
		}},
		Diagnostics: []core.Diagnostic{},
	}
	if err := store.AppendOperation(target, rec); err != nil {
		t.Fatalf("AppendOperation: %v", err)
	}
	rec.OperationID = "op_2"
	rec.Outcome = core.OutcomeFailure
	if err := store.AppendOperation(target, rec); err != nil {
		t.Fatalf("AppendOperation second: %v", err)
	}

	dir, _ := store.StackDir(target)
	path := filepath.Join(dir, "history.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat history: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("history permissions = %v, want private", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state dir permissions = %v, want private", dirInfo.Mode().Perm())
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records []core.OperationRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var got core.OperationRecord
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		records = append(records, got)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].OperationID != "op_1" || records[1].Outcome != core.OutcomeFailure {
		t.Fatalf("records = %+v", records)
	}
}
