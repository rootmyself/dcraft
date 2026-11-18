package restore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/data"
)

type fixtureEntry struct {
	name     string
	typeflag byte
	mode     int64
	uid      int
	gid      int
	body     string
	link     string
}

func writeTarGz(t *testing.T, path string, entries []fixtureEntry) {
	t.Helper()
	raw, err := tarGzBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tarGzBytes(entries []fixtureEntry) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name: e.name, Typeflag: e.typeflag, Mode: e.mode,
			Uid: e.uid, Gid: e.gid, Linkname: e.link,
			ModTime: time.Unix(1700000000, 0),
		}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func referenceEntries() []fixtureEntry {
	return []fixtureEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./.hidden", typeflag: tar.TypeReg, mode: 0o600, body: "dot"},
		{name: "./nested/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./nested/file.txt", typeflag: tar.TypeReg, mode: 0o644, body: "hello", uid: 1000, gid: 2000},
		{name: "./link", typeflag: tar.TypeSymlink, mode: 0o777, link: "nested/file.txt"},
		{name: "./hard", typeflag: tar.TypeLink, mode: 0o644, link: "./nested/file.txt"},
	}
}

func volumeSource() backup.Source {
	return backup.Source{
		Kind: data.KindVolume, Project: "proj", Environment: "",
		ProjectDir: "/work/proj", VolumeKey: "data", VolumeName: "proj_data",
		MountTargets: []string{"/data"}, Services: []string{"app"},
	}
}

// writeArtifact builds a complete, verified v1 artifact fixture.
func writeArtifact(t *testing.T, entries []fixtureEntry, mutate func(*backup.Source)) (string, backup.Manifest) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTarGz(t, filepath.Join(dir, backup.DataFile), entries)
	src := volumeSource()
	if mutate != nil {
		mutate(&src)
	}
	m, err := backup.BuildArtifact(dir, src, backup.StackState{ProjectName: src.Project}, backup.Tool{Version: "test"}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	return dir, m
}

func TestValidateArtifactAcceptsACompleteVolumeBackup(t *testing.T) {
	dir, want := writeArtifact(t, referenceEntries(), nil)
	got, err := ValidateArtifact(dir, "")
	if err != nil {
		t.Fatalf("ValidateArtifact: %v", err)
	}
	if got.Path == "" || got.Manifest.Source.VolumeName != want.Source.VolumeName || !got.Manifest.Complete {
		t.Fatalf("artifact = %+v", got)
	}
}

func TestValidateArtifactRequiresAnExplicitPath(t *testing.T) {
	if _, err := ValidateArtifact("  ", "/work"); err != ErrBackupRequired {
		t.Fatalf("err = %v, want ErrBackupRequired", err)
	}
}

func TestValidateArtifactRejectsTruncatedIncompleteAndUnsupportedArchives(t *testing.T) {
	dir, _ := writeArtifact(t, referenceEntries(), nil)
	path := filepath.Join(dir, backup.DataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(dir, ""); err == nil {
		t.Fatal("truncated archive was accepted")
	}

	dir, m := writeArtifact(t, referenceEntries(), nil)
	m.Complete = false
	if err := backup.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(dir, ""); err == nil {
		t.Fatal("incomplete archive was accepted")
	}

	dir, m = writeArtifact(t, referenceEntries(), nil)
	m.FormatVersion = backup.FormatVersion + 1
	if err := backup.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateArtifact(dir, ""); err == nil {
		t.Fatal("unsupported format was accepted")
	}
}

func TestValidateArtifactAcceptsBindArtifactsForTheSharedContract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTarGz(t, filepath.Join(dir, backup.DataFile), []fixtureEntry{{name: "../escape.txt", typeflag: tar.TypeReg, mode: 0o644, body: "x"}})
	if _, err := backup.BuildArtifact(dir, volumeSource(), backup.StackState{}, backup.Tool{}, time.Now()); err == nil {
		t.Fatal("unsafe archive was published")
	}

	dir, _ = writeArtifact(t, referenceEntries(), func(src *backup.Source) {
		src.Kind = data.KindBind
		src.HostPath = "/host/folder"
		src.VolumeKey = ""
		src.VolumeName = ""
	})
	artifact, err := ValidateArtifact(dir, "")
	if err != nil {
		t.Fatalf("shared validation refused a bind artifact: %v", err)
	}
	if artifact.Manifest.Source.Kind != data.KindBind {
		t.Fatalf("artifact kind = %s", artifact.Manifest.Source.Kind)
	}
}

func TestValidateLinksRejectsALinkUsedAsADirectory(t *testing.T) {
	entries := []backup.Entry{
		{Path: "./dir", Type: backup.EntrySymlink, LinkTarget: "../outside"},
		{Path: "./dir/pwned.txt", Type: backup.EntryFile},
	}
	if err := validateLinks(entries); err == nil {
		t.Fatal("symlink used as a directory was accepted")
	}
	ok := []backup.Entry{
		{Path: "./abs-link", Type: backup.EntrySymlink, LinkTarget: "/etc/passwd"},
		{Path: "./file", Type: backup.EntryFile},
	}
	if err := validateLinks(ok); err != nil {
		t.Fatalf("ordinary absolute symlink was rejected: %v", err)
	}
}

func TestCheckArtifactDataDetectsAChangeAfterValidation(t *testing.T) {
	dir, _ := writeArtifact(t, referenceEntries(), nil)
	artifact, err := ValidateArtifact(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckArtifactData(artifact); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
	path := filepath.Join(dir, backup.DataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckArtifactData(artifact); err == nil {
		t.Fatal("changed archive passed the pre-replace hash check")
	}
}

func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
