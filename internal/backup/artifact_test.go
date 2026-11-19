package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildArtifactPublishesACompleteVerifiableSafetyBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "safety.partial")
	if err := EnsureEmptyDir(dir); err != nil {
		t.Fatal(err)
	}
	writeFixtureArchive(t, filepath.Join(dir, DataFile), referenceEntries())
	source := Source{
		Kind: KindVolume, Project: "proj", VolumeKey: "data", VolumeName: "proj_data",
		Services: []string{"app"},
	}
	m, err := BuildArtifact(dir, source, StackState{ProjectName: "proj", Services: []string{"app"}}, Tool{Version: "test"}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	if !m.Complete || m.Source.VolumeName != "proj_data" || m.Totals.Files != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("Verify safety artifact: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("artifact entries = %v, want manifest + data", entries)
	}
}

func TestBuildArtifactRefusesATruncatedArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "safety.partial")
	if err := EnsureEmptyDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DataFile)
	writeFixtureArchive(t, path, referenceEntries())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-8], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArtifact(dir, Source{Kind: KindVolume, Project: "p", VolumeKey: "data", VolumeName: "p_data"}, StackState{}, Tool{}, time.Now()); err == nil {
		t.Fatal("truncated safety archive was published as complete")
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); !os.IsNotExist(err) {
		t.Fatalf("a manifest was written for a corrupt archive: %v", err)
	}
}

func TestCompareInspectRejectsAChangedEntryOrAnExtraEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), DataFile)
	writeFixtureArchive(t, path, referenceEntries())
	got, err := InspectArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Entries: got.Entries, Totals: got.Totals}
	if err := CompareInspect(m, got); err != nil {
		t.Fatalf("identical archive rejected: %v", err)
	}

	changed := got
	changed.Entries = append([]Entry{}, got.Entries...)
	changed.Entries[1].SHA256 = "00"
	if err := CompareInspect(m, changed); err == nil {
		t.Fatal("changed entry hash was accepted")
	}

	extra := InspectResult{
		Entries: append(append([]Entry{}, got.Entries...), Entry{Path: "./extra", Type: EntryFile, Mode: "0644"}),
		Totals:  got.Totals,
	}
	if err := CompareInspect(m, extra); err == nil {
		t.Fatal("an extra entry was accepted")
	}
}

func TestEnsureEmptyDirRefusesReuse(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureEmptyDir(dir); err != nil {
		t.Fatalf("empty dir refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureEmptyDir(dir); err == nil {
		t.Fatal("non-empty dir was accepted")
	}
}
