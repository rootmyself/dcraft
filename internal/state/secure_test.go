package state_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

func TestStateRootMustBePrivate(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := state.New(root).AllocateName(context.Background(), state.NameRequest{
		Target:   core.Target{ProjectDir: "/work/app"},
		BaseName: "app",
	})
	if err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("err = %v, want a 0700 requirement", err)
	}
}

func TestStateRootMustBeARealDirectory(t *testing.T) {
	realRoot := t.TempDir()
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := state.New(link).AllocateName(context.Background(), state.NameRequest{
		Target:   core.Target{ProjectDir: "/work/app"},
		BaseName: "app",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want a symlink refusal", err)
	}
}

func TestNormalStateRootStillWorks(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := state.New(root).AllocateName(context.Background(), state.NameRequest{
		Target:   core.Target{ProjectDir: "/work/app"},
		BaseName: "app",
	})
	if err != nil || got.Name != "app" {
		t.Fatalf("allocation = %+v, err=%v", got, err)
	}
}

func TestHistoryAndLocksRefuseSymlinkedTargets(t *testing.T) {
	root := privateStateDir(t)
	store := state.New(root)
	target := core.Target{ProjectDir: "/work/app"}
	dir, err := store.StackDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	history := filepath.Join(dir, "history.jsonl")
	if err := os.Symlink(victim, history); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := store.AppendOperation(target, core.OperationRecord{OperationID: "op"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("history symlink err = %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "original" {
		t.Fatalf("victim changed: %q, %v", data, err)
	}
	if err := os.Remove(history); err != nil {
		t.Fatal(err)
	}

	stackLock := filepath.Join(dir, ".lock")
	if err := os.Symlink(victim, stackLock); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.AcquireStackLock(context.Background(), target, state.LockHolder{Action: "up"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("stack lock symlink err = %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "original" {
		t.Fatalf("victim changed through the stack lock: %q, %v", data, err)
	}
}

func TestNameRegistryAndLockRefuseSymlinkedTargets(t *testing.T) {
	root := privateStateDir(t)
	store := state.New(root)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	namesLock := filepath.Join(root, "names.lock")
	if err := os.Symlink(victim, namesLock); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := store.AllocateName(context.Background(), state.NameRequest{
		Target:   core.Target{ProjectDir: "/work/app"},
		BaseName: "app",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("names lock symlink err = %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "original" {
		t.Fatalf("victim changed through names.lock: %q, %v", data, err)
	}
	if err := os.Remove(namesLock); err != nil {
		t.Fatal(err)
	}

	namesPath := filepath.Join(root, "names.json")
	if err := os.Symlink(victim, namesPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := store.LookupName(core.Target{ProjectDir: "/work/app"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("names.json symlink err = %v", err)
	}
	if _, err := store.AllocateName(context.Background(), state.NameRequest{
		Target:   core.Target{ProjectDir: "/work/app"},
		BaseName: "app",
	}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("names.json symlink write err = %v", err)
	}
}
