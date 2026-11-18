package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

func target(dir string) core.Target {
	return core.Target{
		ProjectDir: dir,
		Files:      []string{filepath.Join(dir, "compose.yaml")},
	}
}

func allocate(t *testing.T, s *state.Store, req state.NameRequest) state.NameAllocation {
	t.Helper()
	req.Target.Files = append([]string{}, req.Target.Files...)
	if req.Target.Files == nil {
		req.Target.Files = []string{filepath.Join(req.Target.ProjectDir, "compose.yaml")}
	}
	got, err := s.AllocateName(context.Background(), req)
	if err != nil {
		t.Fatalf("AllocateName: %v", err)
	}
	return got
}

func TestAllocateNameUsesBaseForFirstTarget(t *testing.T) {
	s := state.New(privateStateDir(t))
	got := allocate(t, s, state.NameRequest{Target: target("/work/hello-world"), BaseName: "hello-world"})
	if got.Name != "hello-world" || got.Source != state.NameSourceAllocated {
		t.Fatalf("allocation = %+v", got)
	}
}

func TestAllocateNameChoosesSmallestFreeSuffix(t *testing.T) {
	s := state.New(privateStateDir(t))
	first := allocate(t, s, state.NameRequest{Target: target("/work/a/hello-world"), BaseName: "hello-world"})
	second := allocate(t, s, state.NameRequest{Target: target("/work/b/hello-world"), BaseName: "hello-world"})
	third := allocate(t, s, state.NameRequest{Target: target("/work/c/hello-world"), BaseName: "hello-world"})
	if first.Name != "hello-world" || second.Name != "hello-world-1" || third.Name != "hello-world-2" {
		t.Fatalf("names = %q, %q, %q", first.Name, second.Name, third.Name)
	}
	if strings.Contains(second.Name, "/") || strings.Contains(second.Name, "2f") {
		t.Fatalf("automatic name looks like a path hash: %q", second.Name)
	}
}

func TestAllocateNameReusesRecordedNameOnRerun(t *testing.T) {
	s := state.New(privateStateDir(t))
	first := allocate(t, s, state.NameRequest{Target: target("/work/b/hello-world"), BaseName: "hello-world"})
	allocate(t, s, state.NameRequest{Target: target("/work/a/hello-world"), BaseName: "hello-world"})
	again := allocate(t, s, state.NameRequest{Target: target("/work/b/hello-world"), BaseName: "hello-world"})
	if again.Name != first.Name {
		t.Fatalf("rerun allocated %q, want reused %q", again.Name, first.Name)
	}
}

func TestAllocateNameHonorsDeclaredNameAndRecordsIt(t *testing.T) {
	s := state.New(privateStateDir(t))
	got := allocate(t, s, state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
		DeclaredName: "shared-name", DeclaredSource: state.NameSourceExplicit,
	})
	if got.Name != "shared-name" || got.Source != state.NameSourceExplicit {
		t.Fatalf("allocation = %+v", got)
	}
	// The claim must protect the declared name from another target: an
	// automatic allocation must not reuse it, and an explicit request must
	// fail with a resolution request.
	other := allocate(t, s, state.NameRequest{Target: target("/work/other"), BaseName: "shared-name"})
	if other.Name != "shared-name-1" {
		t.Fatalf("other automatic allocation = %+v", other)
	}
	_, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: target("/work/third"), DeclaredName: "shared-name", DeclaredSource: state.NameSourceExplicit,
	})
	var conflict *state.NameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("declared name reuse err = %v, want NameConflictError", err)
	}
}

func TestAllocateNameRejectsDeclaredNameOwnedByAnotherTarget(t *testing.T) {
	s := state.New(privateStateDir(t))
	allocate(t, s, state.NameRequest{Target: target("/work/a/app"), BaseName: "app"})
	_, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: target("/work/b/app"), DeclaredName: "app", DeclaredSource: state.NameSourceExplicit,
	})
	var conflict *state.NameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want NameConflictError", err)
	}
	if conflict.Name != "app" || conflict.External {
		t.Fatalf("conflict = %+v", conflict)
	}
	if conflict.Target.ProjectDir != "/work/b/app" {
		t.Fatalf("conflict target = %+v", conflict.Target)
	}
	// The other target's allocation is untouched.
	got := allocate(t, s, state.NameRequest{Target: target("/work/a/app"), BaseName: "app"})
	if got.Name != "app" {
		t.Fatalf("other target renamed to %q", got.Name)
	}
}

func TestAllocateNameSkipsExternalProjectForAutomaticName(t *testing.T) {
	s := state.New(privateStateDir(t))
	got := allocate(t, s, state.NameRequest{
		Target: target("/work/b/hello-world"), BaseName: "hello-world",
		External: []state.ExternalProject{{Name: "hello-world", ProjectDir: "/work/a/hello-world"}},
	})
	if got.Name != "hello-world-1" {
		t.Fatalf("name = %q, want hello-world-1", got.Name)
	}
}

func TestAllocateNameReusesExternalProjectOwnedBySameFolder(t *testing.T) {
	s := state.New(privateStateDir(t))
	got := allocate(t, s, state.NameRequest{
		Target: target("/work/hello-world"), BaseName: "hello-world",
		External: []state.ExternalProject{{Name: "hello-world", ProjectDir: "/work/hello-world"}},
	})
	if got.Name != "hello-world" || got.Source != state.NameSourceExisting {
		t.Fatalf("allocation = %+v", got)
	}
}

func TestAllocateNameReusesExistingSuffixedProjectAfterStateLoss(t *testing.T) {
	s := state.New(privateStateDir(t))
	// The readable base belongs to another folder and this folder's project
	// already exists under a high suffix. Suffix allocation would pick the
	// smallest free name (app-1) and create a duplicate stack; the reuse scan
	// must choose the project that already belongs to this folder.
	got := allocate(t, s, state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
		External: []state.ExternalProject{
			{Name: "app-3", ProjectDir: "/work/app"},
			{Name: "app", ProjectDir: "/work/other/app"},
		},
	})
	if got.Name != "app-3" || got.Source != state.NameSourceExisting {
		t.Fatalf("allocation = %+v, want app-3/existing", got)
	}
	// The reuse is recorded, so a later run resolves the same stack without
	// needing the external project list at all.
	again := allocate(t, s, state.NameRequest{Target: target("/work/app"), BaseName: "app"})
	if again.Name != "app-3" {
		t.Fatalf("rerun allocated %q, want app-3", again.Name)
	}
}

func TestPlanStateFingerprintTracksOnlyThisTarget(t *testing.T) {
	s := state.New(privateStateDir(t))
	a := target("/work/a")
	b := target("/work/b")
	if got := s.PlanStateFingerprint(a); got != "none" {
		t.Fatalf("empty state fingerprint = %q, want none", got)
	}
	allocate(t, s, state.NameRequest{Target: a, BaseName: "a"})
	first := s.PlanStateFingerprint(a)
	if first == "" || first == "none" {
		t.Fatalf("allocated fingerprint = %q", first)
	}
	allocate(t, s, state.NameRequest{Target: b, BaseName: "b"})
	if got := s.PlanStateFingerprint(a); got != first {
		t.Fatalf("unrelated allocation changed the target fingerprint: %q -> %q", first, got)
	}
	allocate(t, s, state.NameRequest{
		Target: a, BaseName: "a", DeclaredName: "chosen", DeclaredSource: state.NameSourceExplicit,
	})
	if got := s.PlanStateFingerprint(a); got == first {
		t.Fatal("a changed claim kept the old fingerprint")
	}
}

func TestAllocateNameReportsConflictWhenSuffixedProjectNameBelongsToAnotherTarget(t *testing.T) {
	s := state.New(privateStateDir(t))
	allocate(t, s, state.NameRequest{Target: target("/work/other/app"), BaseName: "app"})
	_, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
		External: []state.ExternalProject{{Name: "app", ProjectDir: "/work/app"}},
	})
	var conflict *state.NameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want NameConflictError", err)
	}
	if conflict.Name != "app" || conflict.OwnerDir != "/work/other/app" {
		t.Fatalf("conflict = %+v", conflict)
	}
}

func TestAllocateNameRejectsDeclaredNameOwnedByAnotherFolderExternally(t *testing.T) {
	s := state.New(privateStateDir(t))
	_, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: target("/work/b/app"), DeclaredName: "shared", DeclaredSource: state.NameSourceExplicit,
		External: []state.ExternalProject{{Name: "shared", ProjectDir: "/work/a/app"}},
	})
	var conflict *state.NameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want NameConflictError", err)
	}
	if !conflict.External || conflict.OwnerDir != "/work/a/app" {
		t.Fatalf("conflict = %+v", conflict)
	}
}

func TestAllocateNameReportsRenameOfARecordedTarget(t *testing.T) {
	s := state.New(privateStateDir(t))
	allocate(t, s, state.NameRequest{Target: target("/work/app"), BaseName: "app"})
	got := allocate(t, s, state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
		DeclaredName: "chosen", DeclaredSource: state.NameSourceExplicit,
	})
	if got.Name != "chosen" || got.PreviousName != "app" {
		t.Fatalf("allocation = %+v", got)
	}
	// The old automatic name is free again.
	other := allocate(t, s, state.NameRequest{Target: target("/work/other/app"), BaseName: "app"})
	if other.Name != "app" {
		t.Fatalf("old name was not released: %+v", other)
	}
}

func TestAllocateNameIsConcurrencySafe(t *testing.T) {
	s := state.New(privateStateDir(t))
	const n = 20
	names := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := filepath.Join("/work", string(rune('a'+i)), "hello-world")
			got, err := s.AllocateName(context.Background(), state.NameRequest{
				Target: target(dir), BaseName: "hello-world",
			})
			names[i], errs[i] = got.Name, err
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("allocation %d: %v", i, err)
		}
		if seen[names[i]] {
			t.Fatalf("duplicate automatic name %q in %v", names[i], names)
		}
		seen[names[i]] = true
	}
	if !seen["hello-world"] {
		t.Fatalf("smallest name was not used: %v", names)
	}
	if !seen["hello-world-19"] {
		t.Fatalf("names were not allocated densely: %v", names)
	}
}

func TestAllocateNameIncludesEnvironmentInBase(t *testing.T) {
	s := state.New(privateStateDir(t))
	tgt := target("/work/app")
	tgt.Environment = "staging"
	got := allocate(t, s, state.NameRequest{Target: tgt, BaseName: "app"})
	if got.Name != "app-staging" {
		t.Fatalf("name = %q, want app-staging", got.Name)
	}
}

func TestAllocateNameRegistersFilePrivately(t *testing.T) {
	root := privateStateDir(t)
	s := state.New(root)
	allocate(t, s, state.NameRequest{Target: target("/work/app"), BaseName: "app"})
	path := filepath.Join(root, "names.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat names.json: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("names.json permissions = %v, want private", info.Mode().Perm())
	}
	var doc struct {
		Version int `json:"version"`
		Claims  map[string]struct {
			ProjectName string `json:"project_name"`
			Source      string `json:"source"`
			ProjectDir  string `json:"project_dir"`
		} `json:"claims"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("names.json is not JSON: %v\n%s", err, data)
	}
	if doc.Version != 1 || len(doc.Claims) != 1 {
		t.Fatalf("document = %+v", doc)
	}
	for _, claim := range doc.Claims {
		if claim.ProjectName != "app" || claim.Source != "allocated" || claim.ProjectDir != "/work/app" {
			t.Fatalf("claim = %+v", claim)
		}
	}
}

func TestAllocateNameRejectsCorruptRegistry(t *testing.T) {
	root := privateStateDir(t)
	if err := os.WriteFile(filepath.Join(root, "names.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := state.New(root)
	if _, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
	}); err == nil {
		t.Fatal("corrupt name registry was ignored")
	}
}

func TestAllocateNameStopsOnCanceledContext(t *testing.T) {
	s := state.New(privateStateDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AllocateName(ctx, state.NameRequest{
		Target: target("/work/app"), BaseName: "app",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestLookupNameIsReadOnlyAndReportsRecordedClaim(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "fresh-state")
	store := state.New(root)
	tgt := target("/work/hello-world")

	if _, ok, err := store.LookupName(tgt); err != nil || ok {
		t.Fatalf("lookup on missing state = %v, %v", ok, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("read-only lookup created the state root: %v", err)
	}

	allocate(t, store, state.NameRequest{Target: tgt, BaseName: "hello-world"})
	got, ok, err := store.LookupName(tgt)
	if err != nil || !ok {
		t.Fatalf("lookup = %v, %v", ok, err)
	}
	if got.Name != "hello-world" || got.Source != state.NameSourceAllocated {
		t.Fatalf("allocation = %+v", got)
	}

	namesPath := filepath.Join(root, "names.json")
	before, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}
	stackDir, err := store.StackDir(tgt)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := store.LookupName(tgt); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("read-only lookup rewrote names.json")
	}
	if _, err := os.Stat(filepath.Join(stackDir, ".lock")); !os.IsNotExist(err) {
		t.Fatalf("read-only lookup created or locked the stack lock: %v", err)
	}
	if _, err := os.Stat(stackDir); !os.IsNotExist(err) {
		t.Fatalf("read-only lookup created the stack directory: %v", err)
	}
}

func TestLookupNameSeparatesTargetsAndEnvironments(t *testing.T) {
	s := state.New(privateStateDir(t))
	dev := target("/work/hello-world")
	staging := target("/work/hello-world")
	staging.Environment = "staging"
	other := target("/work/other")

	allocate(t, s, state.NameRequest{Target: dev, BaseName: "hello-world"})
	if got, ok, err := s.LookupName(dev); err != nil || !ok || got.Name != "hello-world" {
		t.Fatalf("dev lookup = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := s.LookupName(staging); err != nil || ok {
		t.Fatalf("staging lookup = %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := s.LookupName(other); err != nil || ok {
		t.Fatalf("other lookup = %+v, %v, %v", got, ok, err)
	}
}

func TestLookupNameRejectsCorruptRegistry(t *testing.T) {
	root := privateStateDir(t)
	if err := os.WriteFile(filepath.Join(root, "names.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.New(root).LookupName(target("/work/app")); err == nil {
		t.Fatal("corrupt registry was ignored")
	}
}

func TestLookupNameRequiresAProjectFolder(t *testing.T) {
	if _, _, err := state.New(privateStateDir(t)).LookupName(core.Target{}); err == nil {
		t.Fatal("empty target was accepted")
	}
}

func TestStateIdentitySharesCaseVariantSpellings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "CaseProject")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(root, "caseproject")
	info, err := os.Stat(variant)
	if err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	base, _ := os.Stat(dir)
	if !os.SameFile(base, info) {
		t.Skip("case variants are not the same folder on this filesystem")
	}
	s := state.New(privateStateDir(t))
	allocate(t, s, state.NameRequest{Target: target(dir), BaseName: "case-project"})
	got, ok, err := s.LookupName(target(variant))
	if err != nil || !ok || got.Name != "case-project" {
		t.Fatalf("case variant lookup = %+v, %v, %v", got, ok, err)
	}
	if a, _ := s.StackDir(target(dir)); a != mustStackDir(t, s, target(variant)) {
		t.Fatalf("case variant has a separate state dir: %q vs %q", a, mustStackDir(t, s, target(variant)))
	}
}

func TestStateIdentitySharesUnicodeNormalizationSpellings(t *testing.T) {
	root := t.TempDir()
	nfc := filepath.Join(root, "한글")
	if err := os.Mkdir(nfc, 0o700); err != nil {
		t.Skipf("Unicode folder name unavailable: %v", err)
	}
	nfd := filepath.Join(root, "한글")
	info, err := os.Stat(nfd)
	if err != nil {
		t.Skip("filesystem distinguishes NFC/NFD names")
	}
	base, _ := os.Stat(nfc)
	if !os.SameFile(base, info) {
		t.Skip("NFC/NFD spellings are not the same folder on this filesystem")
	}
	s := state.New(privateStateDir(t))
	allocate(t, s, state.NameRequest{Target: target(nfc), BaseName: "korean"})
	got, ok, err := s.LookupName(target(nfd))
	if err != nil || !ok || got.Name != "korean" {
		t.Fatalf("NFC/NFD lookup = %+v, %v, %v", got, ok, err)
	}
	if a, _ := s.StackDir(target(nfc)); a != mustStackDir(t, s, target(nfd)) {
		t.Fatalf("NFC/NFD spellings have separate state dirs: %q vs %q", a, mustStackDir(t, s, target(nfd)))
	}
}

func mustStackDir(t *testing.T, s *state.Store, target core.Target) string {
	t.Helper()
	dir, err := s.StackDir(target)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStateIdentityCanonicalizesSymlinkedFolders(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s := state.New(privateStateDir(t))
	realTarget := target(realDir)
	linkTarget := target(link)
	realDirState, err := s.StackDir(realTarget)
	if err != nil {
		t.Fatal(err)
	}
	linkDirState, err := s.StackDir(linkTarget)
	if err != nil {
		t.Fatal(err)
	}
	if realDirState != linkDirState {
		t.Fatalf("symlinked folder has separate state: %q vs %q", realDirState, linkDirState)
	}
	if _, err := s.AllocateName(context.Background(), state.NameRequest{
		Target: realTarget, BaseName: "hello-world",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LookupName(linkTarget)
	if err != nil || !ok || got.Name != "hello-world" {
		t.Fatalf("lookup through symlink = %+v, %v, %v", got, ok, err)
	}
}
