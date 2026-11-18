package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	link     string
}

func writeFixtureArchive(t *testing.T, path string, entries []fixtureEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Linkname: e.link,
			ModTime:  time.Unix(1700000000, 0),
		}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func referenceEntries() []fixtureEntry {
	return []fixtureEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./.hidden", typeflag: tar.TypeReg, mode: 0o600, body: "dot"},
		{name: "./nested/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./nested/file.txt", typeflag: tar.TypeReg, mode: 0o644, body: "hello"},
		{name: "./link", typeflag: tar.TypeSymlink, mode: 0o777, link: "nested/file.txt"},
		{name: "./hard", typeflag: tar.TypeLink, mode: 0o644, link: "./nested/file.txt"},
	}
}

func TestInspectArchiveRecordsContentHashesAndSpecialEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.tar.gz")
	writeFixtureArchive(t, path, referenceEntries())

	got, err := InspectArchive(path)
	if err != nil {
		t.Fatalf("InspectArchive: %v", err)
	}
	byPath := map[string]Entry{}
	for _, e := range got.Entries {
		byPath[e.Path] = e
	}
	sum := sha256.Sum256([]byte("hello"))
	if byPath["./nested/file.txt"].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("file hash = %q", byPath["./nested/file.txt"].SHA256)
	}
	if byPath["./nested/file.txt"].SizeBytes != 5 {
		t.Fatalf("size = %d", byPath["./nested/file.txt"].SizeBytes)
	}
	if byPath["./link"].Type != EntrySymlink || byPath["./link"].LinkTarget != "nested/file.txt" {
		t.Fatalf("symlink = %+v", byPath["./link"])
	}
	if byPath["./hard"].Type != EntryHardlink || byPath["./hard"].LinkTarget != "./nested/file.txt" {
		t.Fatalf("hardlink = %+v", byPath["./hard"])
	}
	if got.Totals.Files != 2 || got.Totals.Dirs != 2 || got.Totals.Symlinks != 1 || got.Totals.Hardlinks != 1 {
		t.Fatalf("totals = %+v", got.Totals)
	}
	if got.Totals.Bytes != 8 {
		t.Fatalf("bytes = %d", got.Totals.Bytes)
	}
}

func TestInspectArchiveRejectsATruncatedGzipEvenWhenAllNamesList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.tar.gz")
	writeFixtureArchive(t, path, referenceEntries())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	truncated := data[:len(data)-8]
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectArchive(path); err == nil {
		t.Fatal("truncated archive was accepted")
	}
}

func TestInspectArchiveRejectsPathEscapeAndAbsoluteNames(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/etc/passwd", "a/../../b"} {
		path := filepath.Join(t.TempDir(), "data.tar.gz")
		writeFixtureArchive(t, path, []fixtureEntry{{name: name, typeflag: tar.TypeReg, mode: 0o644, body: "x"}})
		if _, err := InspectArchive(path); err == nil {
			t.Fatalf("unsafe name %q was accepted", name)
		}
	}
}

func TestVerifyAcceptsACompleteManifestAndDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
	m := manifestForTest(t, dir)
	if err := WriteManifest(dir, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	verified, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verified.Complete || verified.FormatVersion != FormatVersion {
		t.Fatalf("verified manifest = %+v", verified)
	}

	// Flip one byte of the data payload. The archive hash must catch it before
	// any restore extracts it.
	path := filepath.Join(dir, DataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-6] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("modified archive was accepted")
	}
}

func TestVerifyRequiresTheCompletionMarker(t *testing.T) {
	dir := t.TempDir()
	writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
	m := manifestForTest(t, dir)
	m.Complete = false
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("incomplete manifest was accepted")
	}
}

func TestVerifyRejectsAnUnsupportedFormatVersion(t *testing.T) {
	dir := t.TempDir()
	writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
	m := manifestForTest(t, dir)
	m.FormatVersion = 99
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("unsupported format version was accepted")
	}
}

func TestVerifyRejectsAnEntryThatDoesNotMatchTheManifest(t *testing.T) {
	dir := t.TempDir()
	writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
	m := manifestForTest(t, dir)
	for i := range m.Entries {
		if m.Entries[i].Path == "./nested/file.txt" {
			m.Entries[i].SHA256 = strings.Repeat("0", 64)
		}
	}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("tampered manifest entry was accepted")
	}
}

func TestListOnlyReturnsCompleteBackupsAndIgnoresPartialOutput(t *testing.T) {
	root := t.TempDir()
	complete := filepath.Join(root, "complete")
	partial := filepath.Join(root, "partial.partial")
	incomplete := filepath.Join(root, "incomplete")
	for _, dir := range []string{complete, partial, incomplete} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
		m := manifestForTest(t, dir)
		if dir == incomplete {
			m.Complete = false
		}
		if err := WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(artifacts) != 1 || filepath.Base(artifacts[0].Path) != "complete" {
		t.Fatalf("artifacts = %+v", artifacts)
	}
}

func TestArtifactNameIsSafeAndStaysInsideTheOutputDirectory(t *testing.T) {
	root := t.TempDir()
	name := ArtifactName(Source{Project: "../../evil", VolumeKey: "data/../../x"}, time.Unix(1700000000, 0))
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		t.Fatalf("unsafe artifact name %q", name)
	}
	path, err := ArtifactPath(root, name)
	if err != nil {
		t.Fatalf("artifactPath: %v", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("artifact escaped the output directory: %q", path)
	}
	if _, err := ArtifactPath(root, "../escape"); err == nil {
		t.Fatal("escaping artifact name was accepted")
	}
}

func TestValidateOutputDirRequiresAnExplicitWritableDirectory(t *testing.T) {
	cwd := t.TempDir()
	if _, err := ValidateOutputDir("", cwd); !errors.Is(err, ErrOutputRequired) {
		t.Fatalf("empty output error = %v", err)
	}
	file := filepath.Join(cwd, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateOutputDir(file, cwd); err == nil {
		t.Fatal("a file was accepted as an output directory")
	}
	got, err := ValidateOutputDir("backups", cwd)
	if err != nil {
		t.Fatalf("ValidateOutputDir: %v", err)
	}
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(resolvedCwd, "backups") {
		t.Fatalf("resolved output = %q", got)
	}
	entries, err := os.ReadDir(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("write probe was left behind: %v", entries)
	}
}

func TestValidateOutputDirRejectsAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := ValidateOutputDir(dir, t.TempDir()); err == nil {
		t.Fatal("unwritable directory was accepted")
	}
}

func TestSelectVolumeSourcesMatchesKeysAndActualNames(t *testing.T) {
	sources := []DataSource{
		{Kind: KindVolume, Key: "data", Name: "proj_data", Services: []string{"app"}},
		{Kind: KindVolume, Key: "custom", Name: "custom-engine"},
		{Kind: KindVolume, Anonymous: true, MountTargets: []string{"/anon"}},
		{Kind: KindBind, Name: "/host/bind"},
	}
	if _, err := SelectVolumeSources(sources, nil); !errors.Is(err, ErrVolumeRequired) {
		t.Fatalf("missing selection error = %v", err)
	}
	if _, err := SelectVolumeSources(sources, []string{"nope"}); err == nil {
		t.Fatal("unknown volume was accepted")
	}
	got, err := SelectVolumeSources(sources, []string{"data", "custom-engine", "data"})
	if err != nil {
		t.Fatalf("SelectVolumeSources: %v", err)
	}
	if len(got) != 2 || got[0].Key != "data" || got[1].Key != "custom" {
		t.Fatalf("selected = %+v", got)
	}
	if _, err := SelectVolumeSources(sources, []string{"data", "data"}); err != nil {
		t.Fatalf("dedupe: %v", err)
	}
}

func TestSelectVolumeSourcesRejectsAnonymousAndBindSourcesExplicitly(t *testing.T) {
	sources := []DataSource{
		{Kind: KindVolume, Anonymous: true, MountTargets: []string{"/anon"}},
		{Kind: KindBind, Name: "/host/bind"},
	}
	if _, err := SelectVolumeSources(sources, []string{"/anon"}); err == nil {
		t.Fatal("anonymous volume was selectable")
	}
	if _, err := SelectVolumeSources(sources, []string{"/host/bind"}); err == nil {
		t.Fatal("bind source was selectable")
	}
}

func manifestForTest(t *testing.T, dir string) Manifest {
	t.Helper()
	inspected, err := InspectArchive(filepath.Join(dir, DataFile))
	if err != nil {
		t.Fatal(err)
	}
	size, sum, err := fileSHA256(filepath.Join(dir, DataFile))
	if err != nil {
		t.Fatal(err)
	}
	return Manifest{
		Schema:        Schema,
		FormatVersion: FormatVersion,
		Complete:      true,
		CreatedAt:     time.Unix(1700000000, 0).UTC(),
		Tool:          Tool{Version: "test"},
		Source: Source{
			Kind: KindVolume, Project: "proj", ProjectDir: "/work/proj",
			VolumeKey: "data", VolumeName: "proj_data", Services: []string{"app"},
		},
		Data:      ArchiveInfo{File: DataFile, Format: DataFormatTarGz, SizeBytes: size, SHA256: sum},
		Entries:   inspected.Entries,
		Totals:    inspected.Totals,
		Sensitive: true,
	}
}

func TestSourceFromKeepsVolumeAndBindIdentitySeparate(t *testing.T) {
	vol := SourceFrom(DataSource{Kind: KindVolume, Key: "data", Name: "proj_data"}, "proj", "", "/work/proj")
	if vol.VolumeKey != "data" || vol.VolumeName != "proj_data" || vol.HostPath != "" {
		t.Fatalf("volume source = %+v", vol)
	}
	bind := SourceFrom(DataSource{Kind: KindBind, Name: "/host/folder"}, "proj", "", "/work/proj")
	if bind.HostPath != "/host/folder" || bind.VolumeName != "" || bind.VolumeKey != "" {
		t.Fatalf("bind source = %+v", bind)
	}
}
