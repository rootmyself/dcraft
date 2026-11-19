package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

func bindSource(path string, services ...string) data.DataSource {
	return data.DataSource{
		Kind:         data.KindBind,
		Name:         path,
		MountTargets: []string{"/bind"},
		Services:     services,
	}
}

func TestNormalizeBindSourceResolvesRelativeHomeAndSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}

	relative := NormalizeBindSource(bindSource("./data"), base)
	if relative.Declared != filepath.Join(base, "data") {
		t.Fatalf("relative declared = %q", relative.Declared)
	}
	if relative.Name != filepath.Join(base, "data") {
		t.Fatalf("relative resolved = %q", relative.Name)
	}

	parent := NormalizeBindSource(bindSource("../data"), filepath.Join(base, "nested"))
	if parent.Declared != filepath.Join(base, "data") {
		t.Fatalf("parent declared = %q", parent.Declared)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	homeSource := NormalizeBindSource(bindSource("~/app-data"), base)
	if homeSource.Declared != filepath.Join(home, "app-data") {
		t.Fatalf("home declared = %q", homeSource.Declared)
	}

	symlinked := NormalizeBindSource(bindSource(link), base)
	if symlinked.Declared != link {
		t.Fatalf("symlink declared = %q, want the Compose spelling %q", symlinked.Declared, link)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if symlinked.Name != realResolved {
		t.Fatalf("symlink resolved = %q, want %q", symlinked.Name, realResolved)
	}

	// A relative source need not exist; it is still normalized for display and
	// rejected later with a clear read error.
	missing := NormalizeBindSource(bindSource("./missing"), base)
	if missing.Name != filepath.Join(base, "missing") {
		t.Fatalf("missing resolved = %q", missing.Name)
	}
}

func TestSelectSourcesMixesVolumesAndDeclaredBinds(t *testing.T) {
	base := t.TempDir()
	bindA := NormalizeBindSource(bindSource(filepath.Join(base, "a"), "app"), base)
	bindB := NormalizeBindSource(bindSource(filepath.Join(base, "b"), "worker"), base)
	sources := []data.DataSource{
		volumeSource("data", "proj_data", "app"),
		bindA,
		bindB,
	}

	selected, err := SelectSources(sources, []string{"data"}, []string{"a"}, base)
	if err != nil {
		t.Fatalf("SelectSources: %v", err)
	}
	if len(selected) != 2 || selected[0].Kind != data.KindVolume || selected[1].Kind != data.KindBind {
		t.Fatalf("selection = %+v", selected)
	}
	if selected[1].Name != bindA.Name {
		t.Fatalf("selected bind = %+v, want %+v", selected[1], bindA)
	}

	// The mount target is also an accepted key when it is unique among binds.
	selected, err = SelectSources(sources, nil, []string{"/bind"}, base)
	if err == nil {
		// Both binds use /bind, so the key is ambiguous.
		t.Fatalf("ambiguous mount target was accepted: %+v", selected)
	}
	var ambiguous *AmbiguousBindError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v, want AmbiguousBindError", err)
	}

	// Only a bind is a complete selection.
	selected, err = SelectSources([]data.DataSource{bindA, bindB}, nil, []string{filepath.Join(base, "a")}, base)
	if err != nil || len(selected) != 1 || selected[0].Name != bindA.Name {
		t.Fatalf("bind-only selection = %+v, %v", selected, err)
	}

	// A volume key passed through --bind is not silently reinterpreted.
	if _, err := SelectSources(sources, nil, []string{"data"}, base); err == nil {
		t.Fatal("volume key was accepted as a bind reference")
	}

	// Duplicates collapse to one source.
	selected, err = SelectSources(sources, []string{"data", "data"}, []string{"a", "a"}, base)
	if err != nil || len(selected) != 2 {
		t.Fatalf("deduplicated selection = %+v, %v", selected, err)
	}
}

func TestSelectSourcesRejectsArbitraryHostPaths(t *testing.T) {
	base := t.TempDir()
	declared := NormalizeBindSource(bindSource(filepath.Join(base, "declared")), base)
	arbitrary := filepath.Join(t.TempDir(), "unrelated")

	_, err := SelectSources([]data.DataSource{declared}, nil, []string{arbitrary}, base)
	if err == nil {
		t.Fatal("arbitrary host path was accepted")
	}
	var unknown *UnknownBindError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v, want UnknownBindError", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(base, "declared")) {
		t.Fatalf("available bind paths not named: %v", err)
	}
}

func TestCheckBindSourcesRejectsOutputInsideTheSourceBeforeAnyWrite(t *testing.T) {
	source := t.TempDir()
	output := filepath.Join(source, "backups")
	sources := []data.DataSource{NormalizeBindSource(bindSource(source), source)}

	err := CheckBindSources(sources, output)
	if err == nil {
		t.Fatal("output inside the bind source was accepted")
	}
	if !errors.Is(err, ErrBindUnsafe) {
		t.Fatalf("error = %v, want ErrBindUnsafe", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the check created the output directory inside the source")
	}
}

func TestCheckBindSourcesRejectsMissingFilesRootsAndUnreadableFolders(t *testing.T) {
	base := t.TempDir()
	missing := NormalizeBindSource(bindSource(filepath.Join(base, "missing")), base)
	var pathErr *BindPathError
	if err := CheckBindSources([]data.DataSource{missing}, filepath.Join(base, "out")); !errors.As(err, &pathErr) || pathErr.Reason != BindReasonMissing {
		t.Fatalf("missing path error = %v", err)
	}

	file := filepath.Join(base, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckBindSources([]data.DataSource{bindSource(file)}, filepath.Join(base, "out")); !errors.As(err, &pathErr) || pathErr.Reason != BindReasonNotDirectory {
		t.Fatalf("file path error = %v", err)
	}

	root := bindSource(string(filepath.Separator))
	if err := CheckBindSources([]data.DataSource{root}, filepath.Join(base, "out")); !errors.Is(err, ErrBindUnsafe) {
		t.Fatalf("filesystem root error = %v", err)
	}

	unreadable := filepath.Join(base, "unreadable")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 folder")
	}
	if err := CheckBindSources([]data.DataSource{bindSource(unreadable)}, filepath.Join(base, "out")); !errors.As(err, &pathErr) || pathErr.Reason != BindReasonUnreadable {
		t.Fatalf("unreadable path error = %v", err)
	}
}

func TestResolveForContainmentFollowsExistingAncestorSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveForContainment(filepath.Join(link, "not-created-yet", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(realResolved, "not-created-yet", "nested"); resolved != want {
		t.Fatalf("resolved = %q, want %q", resolved, want)
	}
}

func TestDiscoverMergesBindSpellingsThatResolveToTheSameFolder(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{
		projectName: "proj",
		sources: []data.DataSource{
			bindSource(link, "app"),
			bindSource(real, "worker"),
		},
	}
	discovery, err := Discover(context.Background(), backend, core.Target{ProjectDir: base, Files: []string{filepath.Join(base, "compose.yaml")}}, "proj")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var binds []data.DataSource
	for _, src := range discovery.Sources {
		if src.Kind == data.KindBind {
			binds = append(binds, src)
		}
	}
	if len(binds) != 1 {
		t.Fatalf("merged binds = %+v", binds)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if binds[0].Name != resolved {
		t.Fatalf("resolved path = %q, want %q", binds[0].Name, resolved)
	}
	if strings.Join(binds[0].Services, ",") != "app,worker" {
		t.Fatalf("services = %v", binds[0].Services)
	}
	if len(binds[0].DeclaredPaths) != 2 {
		t.Fatalf("declared spellings = %v", binds[0].DeclaredPaths)
	}
	selected, err := SelectSources(discovery.Sources, nil, []string{link}, base)
	if err != nil || len(selected) != 1 {
		t.Fatalf("selection by the symlink spelling = %+v, %v", selected, err)
	}
}
