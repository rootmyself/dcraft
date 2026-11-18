package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CompareInspect checks a fully inspected archive against a manifest. Verify
// uses the same rule for the incoming artifact; restore uses it to compare a
// re-read archive with the manifest it already validated.
func CompareInspect(m Manifest, got InspectResult) error {
	if err := compareEntries(m.Entries, got.Entries); err != nil {
		return err
	}
	if m.Totals != got.Totals {
		return fmt.Errorf("%w: totals do not match the archive", ErrIntegrity)
	}
	return nil
}

// BuildArtifact turns an already-written data.tar.gz into a complete v1
// artifact. The caller creates dir (normally a ".partial" directory) and writes
// DataFile there. BuildArtifact inspects the archive, writes the completion
// marker last and runs Verify. It is the published helper for safety backups
// created outside the `dcraft backup` wizard (restore #18/#19).
func BuildArtifact(dir string, source Source, stack StackState, tool Tool, createdAt time.Time) (Manifest, error) {
	dataPath := filepath.Join(dir, DataFile)
	inspected, err := InspectArchive(dataPath)
	if err != nil {
		return Manifest{}, err
	}
	size, sum, err := fileSHA256(dataPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	m := Manifest{
		Schema:        Schema,
		FormatVersion: FormatVersion,
		Complete:      false,
		CreatedAt:     createdAt.UTC(),
		Tool:          tool,
		Source:        source,
		Stack:         stack,
		Data: ArchiveInfo{
			File: DataFile, Format: DataFormatTarGz, SizeBytes: size, SHA256: sum,
		},
		Entries:       inspected.Entries,
		Totals:        inspected.Totals,
		Sensitive:     true,
		OwnershipNote: msgOwnershipNote,
	}
	if err := WriteManifest(dir, m); err != nil {
		return Manifest{}, err
	}
	m.Complete = true
	if err := WriteManifest(dir, m); err != nil {
		return Manifest{}, err
	}
	if _, err := Verify(dir); err != nil {
		return Manifest{}, err
	}
	if err := syncDir(dir); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// EnsureEmptyDir creates dir (0700) when missing and refuses a non-empty one.
// It protects safety-backup and scratch locations from accidental reuse.
func EnsureEmptyDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("directory %s is not empty", dir)
	}
	return nil
}
