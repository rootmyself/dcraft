package restore

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/data"
)

func TestMatchSourceRequiresTheSameStackAndVolumeByDefault(t *testing.T) {
	m := backup.Manifest{Source: volumeSource()}
	target := volumeSource()
	if _, err := MatchSource(m, target, false); err != nil {
		t.Fatalf("same identity rejected: %v", err)
	}

	other := target
	other.Project = "other"
	if _, err := MatchSource(m, other, false); err == nil {
		t.Fatal("other project was accepted by default")
	} else if !isErr(err, ErrTargetMismatch) {
		t.Fatalf("project mismatch error = %v", err)
	}
	warnings, err := MatchSource(m, other, true)
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "project") {
		t.Fatalf("explicit mapping = %v, %v", warnings, err)
	}

	other = target
	other.Environment = "prod"
	if _, err := MatchSource(m, other, false); err == nil {
		t.Fatal("other environment was accepted by default")
	}

	other = target
	other.VolumeKey = "other"
	if _, err := MatchSource(m, other, false); err == nil {
		t.Fatal("other volume key was accepted by default")
	}

	other = target
	other.VolumeName = "other_data"
	if _, err := MatchSource(m, other, false); err == nil {
		t.Fatal("other volume name was accepted by default")
	}

	bind := target
	bind.Kind = data.KindBind
	bind.HostPath = "/host"
	if _, err := MatchSource(m, bind, true); err == nil {
		t.Fatal("a volume artifact matched a bind target")
	}
}

func TestValidateLocationRefusesTargetAndArtifactOverlap(t *testing.T) {
	dest := &pathDestination{root: "/target"}
	artifact := filepath.Join(t.TempDir(), "artifact")
	safety := filepath.Join(t.TempDir(), "safety")
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLocation(dest, artifact, safety); err != nil {
		t.Fatalf("outside locations rejected: %v", err)
	}
	safety = filepath.Join(artifact, "safety")
	if err := ValidateLocation(dest, artifact, safety); err == nil {
		t.Fatal("safety inside the artifact was accepted")
	}
	safety = filepath.Join(t.TempDir(), "safety")
	dest.root = filepath.Dir(safety)
	if err := ValidateLocation(dest, artifact, safety); err == nil {
		t.Fatal("safety inside the target was accepted")
	}
}

// pathDestination is a minimal Destination used only for containment tests.
type pathDestination struct{ root string }

func (d *pathDestination) Kind() data.Kind               { return data.KindBind }
func (d *pathDestination) Key() string                   { return d.root }
func (d *pathDestination) Name() string                  { return d.root }
func (d *pathDestination) Services() []string            { return nil }
func (d *pathDestination) MountTargets() []string        { return nil }
func (d *pathDestination) ManifestSource() backup.Source { return backup.Source{} }
func (d *pathDestination) Match(backup.Manifest, bool) ([]string, error) {
	return nil, nil
}
func (d *pathDestination) Outside(path string) (bool, error) {
	return !within(d.root, path), nil
}
func (d *pathDestination) Prepare(context.Context) error { return nil }
func (d *pathDestination) UnsafeConsumers(context.Context) ([]data.ContainerConsumer, error) {
	return nil, nil
}
func (d *pathDestination) Usage(context.Context) (ContentSummary, error) {
	return ContentSummary{}, nil
}
func (d *pathDestination) SafetyBackup(context.Context, string) (data.OpResult, error) {
	return data.OpResult{}, nil
}
func (d *pathDestination) Empty(context.Context) (data.OpResult, error) { return data.OpResult{}, nil }
func (d *pathDestination) Extract(context.Context, string) (data.OpResult, error) {
	return data.OpResult{}, nil
}
func (d *pathDestination) Verify(context.Context, backup.Manifest) (data.OpResult, error) {
	return data.OpResult{}, nil
}

func TestWriteVerifyScriptChecksHashesMetadataLinksAndCount(t *testing.T) {
	_, m := writeArtifact(t, []fixtureEntry{
		{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		{name: "./한글 파일.txt", typeflag: tar.TypeReg, mode: 0o640, uid: 1000, gid: 2000, body: "hello"},
		{name: "./it's.txt", typeflag: tar.TypeReg, mode: 0o600, body: "quote"},
		{name: "./link", typeflag: tar.TypeSymlink, mode: 0o777, link: "한글 파일.txt"},
		{name: "./hard", typeflag: tar.TypeLink, mode: 0o640, link: "./한글 파일.txt"},
	}, nil)
	path := filepath.Join(t.TempDir(), "check.sh")
	if err := WriteVerifyScript(path, m); err != nil {
		t.Fatalf("WriteVerifyScript: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, want := range []string{
		"sha256sum '/data/한글 파일.txt'",
		"meta '/data/한글 파일.txt' 1000 2000 '640'",
		"'/data/it'\\''s.txt'",
		"readlink '/data/link'",
		"'한글 파일.txt'",
		"stat -c '%i' '/data/hard'",
		"entry count: $actual != 4",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}
