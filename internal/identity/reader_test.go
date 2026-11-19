package identity_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/identity"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

type fakeIdentity struct {
	config       stack.ConfigInfo
	configErr    error
	derived      string
	derivedErr   error
	configCalls  int
	derivedCalls int
}

func (f *fakeIdentity) ConfigInfo(ctx context.Context, t core.Target) (stack.ConfigInfo, error) {
	f.configCalls++
	return f.config, f.configErr
}

func (f *fakeIdentity) DerivedProjectName(ctx context.Context, t core.Target) (string, error) {
	f.derivedCalls++
	return f.derived, f.derivedErr
}

func (f *fakeIdentity) Projects(ctx context.Context) ([]stack.ProjectInfo, error) {
	return nil, nil
}

func testTarget(dir string) core.Target {
	return core.Target{
		ProjectDir: dir,
		Files:      []string{filepath.Join(dir, "compose.yaml")},
	}
}

func TestReadProjectNameUsesTheRecordedAllocationWithoutWriting(t *testing.T) {
	stateDir := privateStateDir(t)
	store := state.New(stateDir)
	tgt := testTarget("/work/hello-world")
	if _, err := store.AllocateName(context.Background(), state.NameRequest{
		Target: tgt, BaseName: "hello-world",
	}); err != nil {
		t.Fatal(err)
	}
	namesPath := filepath.Join(stateDir, "names.json")
	before, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}

	id := &fakeIdentity{config: stack.ConfigInfo{EffectiveName: "hello-world"}, derived: "hello-world"}
	got, err := identity.New(id, store).ReadProjectName(context.Background(), tgt)
	if err != nil {
		t.Fatalf("ReadProjectName: %v", err)
	}
	if got != "hello-world" {
		t.Fatalf("name = %q", got)
	}
	after, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("identity read rewrote names.json")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "stacks")); !os.IsNotExist(err) {
		t.Fatalf("identity read created stack state: %v", err)
	}
}

func TestReadProjectNameFallsBackToComposeWithoutAllocation(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	store := state.New(stateDir)
	id := &fakeIdentity{config: stack.ConfigInfo{EffectiveName: "folder-default"}, derived: "folder-default"}
	got, err := identity.New(id, store).ReadProjectName(context.Background(), testTarget("/work/app"))
	if err != nil {
		t.Fatalf("ReadProjectName: %v", err)
	}
	if got != "" {
		t.Fatalf("name = %q, want empty fallback", got)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("read-only identity check created state: %v", err)
	}
}

func TestReadProjectNamePrefersADeclaredNameOverTheAllocation(t *testing.T) {
	store := state.New(privateStateDir(t))
	tgt := testTarget("/work/hello-world")
	if _, err := store.AllocateName(context.Background(), state.NameRequest{
		Target: tgt, BaseName: "hello-world",
	}); err != nil {
		t.Fatal(err)
	}
	id := &fakeIdentity{config: stack.ConfigInfo{EffectiveName: "declared-name"}, derived: "hello-world"}
	got, err := identity.New(id, store).ReadProjectName(context.Background(), tgt)
	if err != nil {
		t.Fatalf("ReadProjectName: %v", err)
	}
	if got != "declared-name" {
		t.Fatalf("name = %q, want declared-name", got)
	}
}

func TestReadProjectNamePrefersAnExplicitNameWithoutComposeCalls(t *testing.T) {
	id := &fakeIdentity{}
	tgt := testTarget("/work/app")
	tgt.ProjectName = "explicit-name"
	got, err := identity.New(id, state.New(privateStateDir(t))).ReadProjectName(context.Background(), tgt)
	if err != nil {
		t.Fatalf("ReadProjectName: %v", err)
	}
	if got != "explicit-name" {
		t.Fatalf("name = %q", got)
	}
	if id.configCalls != 0 || id.derivedCalls != 0 {
		t.Fatalf("explicit name still called Compose: %d/%d", id.configCalls, id.derivedCalls)
	}
}

func TestReadProjectNamePropagatesErrors(t *testing.T) {
	id := &fakeIdentity{configErr: context.DeadlineExceeded}
	if _, err := identity.New(id, state.New(privateStateDir(t))).ReadProjectName(context.Background(), testTarget("/work/app")); err == nil {
		t.Fatal("identity error was swallowed")
	}
}
