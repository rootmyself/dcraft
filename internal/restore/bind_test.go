package restore

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

func bindSourceFixture(hostPath string) backup.Source {
	return backup.Source{
		Kind: data.KindBind, Project: "proj", Environment: "",
		ProjectDir: "/work/proj", HostPath: hostPath,
		MountTargets: []string{"/data"}, Services: []string{"app"},
	}
}

// buildBindArtifact archives dir into a complete v1 bind artifact whose
// recorded source path can be overridden (to model a backup taken elsewhere).
func buildBindArtifact(t *testing.T, dir, recordedPath string, mutate func(*backup.Source)) (string, backup.Manifest) {
	t.Helper()
	artifact := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backup.ArchiveHostDir(context.Background(), dir, filepath.Join(artifact, backup.DataFile)); err != nil {
		t.Fatalf("ArchiveHostDir: %v", err)
	}
	src := bindSourceFixture(recordedPath)
	if mutate != nil {
		mutate(&src)
	}
	m, err := backup.BuildArtifact(artifact, src, backup.StackState{ProjectName: src.Project}, backup.Tool{Version: "test"}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	return artifact, m
}

func newBindDest(source data.DataSource, projectDir, project string, explicitPath bool) *BindDestination {
	target := core.Target{ProjectDir: projectDir, Environment: "", Files: []string{filepath.Join(projectDir, "compose.yaml")}}
	dest := NewBindDestination(nil, source, target, project)
	dest.explicitPath = explicitPath
	return dest
}

func seedBindV1(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "nested", "dir750"), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "file.txt"), "hello", 0o640)
	write(filepath.Join(dir, ".hidden"), "dot", 0o600)
	write(filepath.Join(dir, "run.sh"), "#!/bin/sh\n", 0o750)
	write(filepath.Join(dir, "nested", "child.txt"), "child", 0o644)
	write(filepath.Join(dir, "nested", "dir750", "deep.txt"), "deep", 0o600)
	if err := os.Symlink("file.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "abs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "file.txt"), filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func bindTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "symlink|" + target
		case info.IsDir():
			out[rel] = "dir|" + info.Mode().Perm().String()
		default:
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			out[rel] = "file|" + info.Mode().Perm().String() + "|" + hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func TestBindDestinationMatchRequiresTheDeclaredPathUnlessMapped(t *testing.T) {
	projectDir := t.TempDir()
	current := t.TempDir()
	archived := t.TempDir()
	src := data.DataSource{Kind: data.KindBind, Declared: current, Name: current, Services: []string{"app"}}
	dest := newBindDest(src, projectDir, "proj", false)

	same := backup.Manifest{Source: bindSourceFixture(current)}
	if warnings, err := dest.Match(same, false); err != nil || len(warnings) != 0 {
		t.Fatalf("same path = %v, %v", warnings, err)
	}

	other := backup.Manifest{Source: bindSourceFixture(archived)}
	if _, err := dest.Match(other, false); err == nil || !isErr(err, ErrTargetMismatch) {
		t.Fatalf("different path accepted by default: %v", err)
	}
	if _, err := dest.Match(other, true); err == nil || !isErr(err, ErrTargetMismatch) {
		t.Fatalf("source-mismatch flag silently allowed a path mapping: %v", err)
	}
	dest.explicitPath = true
	warnings, err := dest.Match(other, false)
	if err != nil {
		t.Fatalf("explicit path mapping was rejected: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "경로") {
		t.Fatalf("mapping warning = %v", warnings)
	}

	// Project/environment mapping keeps its own independent flag.
	renamed := backup.Manifest{Source: bindSourceFixture(current)}
	renamed.Source.Project = "other"
	if _, err := dest.Match(renamed, false); err == nil {
		t.Fatal("other project was accepted")
	}
	if warnings, err := dest.Match(renamed, true); err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "project") {
		t.Fatalf("explicit project mapping = %v, %v", warnings, err)
	}

	volume := backup.Manifest{Source: volumeSource()}
	if _, err := dest.Match(volume, true); err == nil || !isErr(err, ErrTargetMismatch) {
		t.Fatalf("volume artifact accepted by a bind target: %v", err)
	}
}

func TestValidateBindManifestRefusesUnsupportedAndUnrepresentableShapes(t *testing.T) {
	base := backup.Manifest{Source: bindSourceFixture("/host/folder")}

	cases := []struct {
		name    string
		entries []backup.Entry
		want    error
	}{
		{
			name:    "special file",
			entries: []backup.Entry{{Path: "./pipe", Type: backup.EntryOther}},
			want:    ErrTargetUnsupported,
		},
		{
			name:    "empty symlink target",
			entries: []backup.Entry{{Path: "./link", Type: backup.EntrySymlink, LinkTarget: ""}},
			want:    ErrArtifactInvalid,
		},
		{
			name: "hardlink target missing",
			entries: []backup.Entry{
				{Path: "./hard", Type: backup.EntryHardlink, LinkTarget: "./missing"},
			},
			want: ErrArtifactInvalid,
		},
		{
			name: "parent is a file",
			entries: []backup.Entry{
				{Path: "./parent", Type: backup.EntryFile},
				{Path: "./parent/child", Type: backup.EntryFile},
			},
			want: ErrArtifactInvalid,
		},
		{
			name: "relative archived path",
			entries: []backup.Entry{
				{Path: "./file", Type: backup.EntryFile},
			},
			want: ErrArtifactInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Entries = tc.entries
			if tc.name == "relative archived path" {
				m.Source.HostPath = "relative/folder"
			}
			err := validateBindManifest(m)
			if err == nil || !isErr(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	valid := base
	valid.Entries = []backup.Entry{
		{Path: "./", Type: backup.EntryDir},
		{Path: "./dir", Type: backup.EntryDir},
		{Path: "./dir/file", Type: backup.EntryFile, SizeBytes: 1, SHA256: "abc"},
		{Path: "./link", Type: backup.EntrySymlink, LinkTarget: "/etc/hosts"},
		{Path: "./hard", Type: backup.EntryHardlink, LinkTarget: "./dir/file"},
	}
	if err := validateBindManifest(valid); err != nil {
		t.Fatalf("valid bind manifest rejected: %v", err)
	}
}

func TestBindDestinationRoundTripReplacesContentExactly(t *testing.T) {
	sourceDir := t.TempDir()
	seedBindV1(t, sourceDir)
	fixedTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, path := range []string{
		filepath.Join(sourceDir, "file.txt"),
		filepath.Join(sourceDir, "nested"),
		filepath.Join(sourceDir, "nested", "dir750"),
	} {
		if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
			t.Fatal(err)
		}
	}
	artifact, manifest := buildBindArtifact(t, sourceDir, sourceDir, nil)

	targetDir := t.TempDir()
	seedBindV1(t, targetDir)
	if err := os.WriteFile(filepath.Join(targetDir, "stale.txt"), []byte("v2 leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("changed in v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := bindTree(t, sourceDir)

	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: targetDir, Name: targetDir, Services: []string{"app"}}, t.TempDir(), "proj", false)
	if err := dest.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	usage, err := dest.Usage(context.Background())
	if err != nil || usage.Entries == 0 {
		t.Fatalf("Usage = %+v, %v", usage, err)
	}
	safety, err := CreateSafetyBackup(context.Background(), dest, t.TempDir(), backup.StackState{ProjectName: "proj"}, backup.Tool{Version: "test"}, fixedClock{now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("safety backup: %v", err)
	}
	safetyManifest, err := backup.Verify(safety.Path)
	if err != nil {
		t.Fatalf("safety did not verify: %v", err)
	}
	if safetyManifest.Source.Kind != data.KindBind || safetyManifest.Source.HostPath != targetDir {
		t.Fatalf("safety source = %+v", safetyManifest.Source)
	}
	safetyPaths := map[string]bool{}
	for _, entry := range safetyManifest.Entries {
		safetyPaths[entry.Path] = true
	}
	if !safetyPaths["./stale.txt"] {
		t.Fatalf("safety backup does not contain the pre-restore content: %+v", safetyManifest.Entries)
	}

	if _, err := dest.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if entries, err := os.ReadDir(targetDir); err != nil || len(entries) != 0 {
		t.Fatalf("target not empty: %v, %v", entries, err)
	}
	if _, err := dest.Extract(context.Background(), artifact); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := dest.Verify(context.Background(), manifest); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got := bindTree(t, targetDir)
	if len(got) != len(want) {
		t.Fatalf("tree size = %d, want %d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for path, fact := range want {
		if got[path] != fact {
			t.Fatalf("%s = %q, want %q", path, got[path], fact)
		}
	}
	if _, ok := got["stale.txt"]; ok {
		t.Fatal("stale file survived the restore")
	}
	hard1, err := os.Stat(filepath.Join(targetDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hard2, err := os.Stat(filepath.Join(targetDir, "hard"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(hard1, hard2) {
		t.Fatal("hardlink was not restored")
	}
	for _, path := range []string{
		filepath.Join(targetDir, "file.txt"),
		filepath.Join(targetDir, "nested"),
		filepath.Join(targetDir, "nested", "dir750"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.ModTime().Unix(); got != fixedTime.Unix() {
			t.Fatalf("%s mtime = %d, want %d", path, got, fixedTime.Unix())
		}
	}
}

func TestBindDestinationEmptyRemovesDotfilesAndSymlinksWithoutEscaping(t *testing.T) {
	target := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden", "..double", "normal.txt"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(target, ".hidden-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".hidden-dir", "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "escape")); err != nil {
		t.Fatal(err)
	}
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	if _, err := dest.Empty(context.Background()); err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("target not empty: %v, %v", entries, err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "keep" {
		t.Fatalf("empty followed an escape symlink: %q, %v", raw, err)
	}
}

func TestBindDestinationExtractRefusesAPreexistingEscapeSymlink(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceDir, "pre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pre", "pwned.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact, manifest := buildBindArtifact(t, sourceDir, sourceDir, nil)

	outsideDir := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(target, "pre")); err != nil {
		t.Fatal(err)
	}
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	if _, err := dest.Extract(context.Background(), artifact); err == nil {
		t.Fatal("extraction through a pre-existing symlink was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outsideDir, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("extraction escaped the target: %v", err)
	}
	_ = manifest
}

func TestBindDestinationVerifyDetectsStaleAndChangedContent(t *testing.T) {
	sourceDir := t.TempDir()
	seedBindV1(t, sourceDir)
	artifact, manifest := buildBindArtifact(t, sourceDir, sourceDir, nil)

	target := t.TempDir()
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	if _, err := dest.Empty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Extract(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Verify(context.Background(), manifest); err != nil {
		t.Fatalf("valid restore failed verification: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Verify(context.Background(), manifest); err == nil {
		t.Fatal("stale file passed verification")
	}
	if err := os.Remove(filepath.Join(target, "stale.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file.txt"), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Verify(context.Background(), manifest); err == nil {
		t.Fatal("changed content passed verification")
	}
}

func TestBindDestinationPrepareRefusesMissingAndNonDirectoryTargets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: file, Name: file}, t.TempDir(), "proj", false)
	if err := dest.Prepare(context.Background()); err == nil || !isErr(err, ErrTargetInvalid) {
		t.Fatalf("file target err = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	dest = newBindDest(data.DataSource{Kind: data.KindBind, Declared: missing, Name: missing}, t.TempDir(), "proj", false)
	if err := dest.Prepare(context.Background()); err == nil || !isErr(err, ErrTargetMissing) {
		t.Fatalf("missing target err = %v", err)
	}
}

func TestBindDestinationOutsideRecognizesContainmentAndSymlinks(t *testing.T) {
	target := t.TempDir()
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	for _, tc := range []struct {
		path    string
		outside bool
	}{
		{filepath.Join(target, "artifact"), false},
		{filepath.Join(t.TempDir(), "artifact"), true},
		{filepath.Join(target, "sub", "deeper"), false},
	} {
		got, err := dest.Outside(tc.path)
		if err != nil || got != tc.outside {
			t.Fatalf("Outside(%s) = %v, %v", tc.path, got, err)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if outside, err := dest.Outside(filepath.Join(link, "artifact")); err != nil || outside {
		t.Fatalf("symlinked containment = %v, %v", outside, err)
	}
}

// Extract must notice that the data payload changed after artifact validation.
func TestBindDestinationExtractDetectsAChangedArchive(t *testing.T) {
	sourceDir := t.TempDir()
	seedBindV1(t, sourceDir)
	artifact, _ := buildBindArtifact(t, sourceDir, sourceDir, nil)
	path := filepath.Join(artifact, backup.DataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	if _, err := dest.Extract(context.Background(), artifact); err == nil {
		t.Fatal("changed archive was extracted")
	}
}

// The bind manifest validation is the storage-specific half of preflight; it
// must not require uid/gid to be restorable on this host.
func TestBindDestinationRoundTripIgnoresHostOwnershipMismatch(t *testing.T) {
	sourceDir := t.TempDir()
	seedBindV1(t, sourceDir)
	artifact, manifest := buildBindArtifact(t, sourceDir, sourceDir, nil)
	for i := range manifest.Entries {
		if manifest.Entries[i].Type == backup.EntryFile {
			manifest.Entries[i].UID = 1234
			manifest.Entries[i].GID = 5678
		}
	}
	if err := backup.WriteManifest(artifact, manifest); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: target, Name: target}, t.TempDir(), "proj", false)
	if _, err := dest.Empty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.Extract(context.Background(), artifact); err != nil {
		t.Fatalf("Extract with foreign ownership: %v", err)
	}
	if _, err := dest.Verify(context.Background(), manifest); err != nil {
		t.Fatalf("Verify rejected a host ownership mismatch: %v", err)
	}
}

// The archive type shape check must refuse a FIFO even though the v1 format can
// record it, because a host bind restore cannot recreate special files safely.
func TestBindDestinationMatchRefusesSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTarGz(t, filepath.Join(artifact, backup.DataFile), []fixtureEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./pipe", typeflag: tar.TypeFifo, mode: 0o644},
	})
	if _, err := backup.BuildArtifact(artifact, bindSourceFixture(dir), backup.StackState{}, backup.Tool{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manifest, err := backup.Verify(artifact)
	if err != nil {
		t.Fatal(err)
	}
	dest := newBindDest(data.DataSource{Kind: data.KindBind, Declared: dir, Name: dir}, t.TempDir(), "proj", false)
	if _, err := dest.Match(manifest, false); err == nil || !isErr(err, ErrTargetUnsupported) {
		t.Fatalf("special file match err = %v", err)
	}
}
