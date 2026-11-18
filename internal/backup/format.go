// Package backup owns the backup feature (ticket #16) and the backup archive
// format v1 shared with bind-folder backup (#17) and restore (#18/#19).
//
// Format v1 is one directory per backup:
//
//	<name>/manifest.json   versioned identity, completion marker and integrity
//	<name>/data.tar.gz     tar+gzip of the source tree, paths relative to root
//
// The artifact is built as <name>.partial and renamed only after the archive
// was completely read and extracted into a fresh verification directory and
// the manifest was written. A partial or interrupted directory is never a
// valid backup.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/data"
)

// Format constants. They are the published archive contract.
const (
	Schema          = "dcraft.backup"
	FormatVersion   = 1
	ManifestFile    = "manifest.json"
	DataFile        = data.ArchiveDataFile
	DataFormatTarGz = "tar.gz"
	// PartialSuffix marks an artifact that is still being written or was
	// interrupted. It is never a valid backup and never listed as one.
	PartialSuffix = ".partial"
)

// DataSource is the published target-selection type. Ticket #17 contributes
// bind sources through the same data.DataSource values.
type DataSource = data.DataSource

// Kind constants re-exported for feature code and tests.
const (
	KindVolume = data.KindVolume
	KindBind   = data.KindBind
)

// Entry types recorded in the manifest.
const (
	EntryFile     = "file"
	EntryDir      = "dir"
	EntrySymlink  = "symlink"
	EntryHardlink = "hardlink"
	EntryOther    = "other"
)

// Errors of the published contract. Restore tickets classify archives with
// errors.Is.
var (
	ErrIncomplete      = errors.New("backup is incomplete (completion marker missing)")
	ErrUnsupported     = errors.New("unsupported backup format")
	ErrIntegrity       = errors.New("backup integrity check failed")
	ErrUnsafePath      = errors.New("backup contains an unsafe path")
	ErrVolumeRequired  = errors.New("no data volume was selected")
	ErrUnsupportedData = errors.New("data source cannot be archived by this command")
)

// Source identifies the archived data and its stack. Kind is "volume" today;
// ticket #17 publishes "bind" with HostPath instead of VolumeName.
type Source struct {
	Kind         data.Kind `json:"kind"`
	Project      string    `json:"project"`
	Environment  string    `json:"environment,omitempty"`
	ProjectDir   string    `json:"project_dir"`
	VolumeKey    string    `json:"volume_key,omitempty"`
	VolumeName   string    `json:"volume_name,omitempty"`
	HostPath     string    `json:"host_path,omitempty"`
	MountTargets []string  `json:"mount_targets"`
	External     bool      `json:"external"`
	Anonymous    bool      `json:"anonymous"`
	Services     []string  `json:"services"`
}

// StackState records the services that were affected and whether they were
// running before the copy. It is audit metadata, not a promise of consistency.
type StackState struct {
	ProjectName      string   `json:"project_name"`
	Services         []string `json:"services"`
	RunningBefore    []string `json:"running_before"`
	StoppedForBackup []string `json:"stopped_for_backup"`
}

// ArchiveInfo identifies and hashes the data payload.
type ArchiveInfo struct {
	File      string `json:"file"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Entry is one per-entry integrity record. SHA256 is set for regular files and
// is content-only (it does not include ownership, mode or mtime).
type Entry struct {
	Path       string `json:"path"`
	Type       string `json:"type"`
	Mode       string `json:"mode,omitempty"`
	UID        int    `json:"uid,omitempty"`
	GID        int    `json:"gid,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
}

// Totals summarize one archive.
type Totals struct {
	Entries   int   `json:"entries"`
	Files     int   `json:"files"`
	Dirs      int   `json:"dirs"`
	Symlinks  int   `json:"symlinks"`
	Hardlinks int   `json:"hardlinks"`
	Other     int   `json:"other"`
	Bytes     int64 `json:"bytes"`
}

// Tool identifies the writer.
type Tool struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version,omitempty"`
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
}

// Manifest is manifest.json v1. Complete is written last; Verify rejects a
// false/missing value. Sensitive is always true for stack data.
type Manifest struct {
	Schema        string      `json:"schema"`
	FormatVersion int         `json:"format_version"`
	Complete      bool        `json:"complete"`
	CreatedAt     time.Time   `json:"created_at"`
	Tool          Tool        `json:"tool"`
	Source        Source      `json:"source"`
	Stack         StackState  `json:"stack"`
	Data          ArchiveInfo `json:"data"`
	Entries       []Entry     `json:"entries"`
	Totals        Totals      `json:"totals"`
	Sensitive     bool        `json:"sensitive"`
	OwnershipNote string      `json:"ownership_note"`
}

// InspectResult is the result of reading one data archive completely.
type InspectResult struct {
	Entries []Entry
	Totals  Totals
}

// InspectArchive reads the whole gzip/tar stream and records every entry. It
// checks the gzip trailer, rejects unsafe names and duplicate entries, and
// never extracts to a destination. Names listing alone is never treated as
// integrity.
func InspectArchive(path string) (InspectResult, error) {
	dir, err := os.MkdirTemp("", "dcraft-backup-inspect-")
	if err != nil {
		return InspectResult{}, err
	}
	defer os.RemoveAll(dir)
	return readArchive(path, dir)
}

// Verify is the published integrity entry point for restore. It requires a
// complete v1 manifest, compares the archive hash and extracts the archive
// into a fresh empty directory, then compares every entry hash. It does not
// touch any restore destination.
func Verify(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return Manifest{}, err
	}
	if err := checkManifestHeader(m); err != nil {
		return Manifest{}, err
	}
	if m.Data.File != DataFile {
		return Manifest{}, fmt.Errorf("%w: data file %q is not %q", ErrUnsupported, m.Data.File, DataFile)
	}
	path := filepath.Join(dir, DataFile)
	size, sum, err := fileSHA256(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	if size != m.Data.SizeBytes || !strings.EqualFold(sum, m.Data.SHA256) {
		return Manifest{}, fmt.Errorf("%w: archive hash/size does not match the manifest", ErrIntegrity)
	}

	got, err := InspectArchive(path)
	if err != nil {
		return Manifest{}, err
	}
	if err := compareEntries(m.Entries, got.Entries); err != nil {
		return Manifest{}, err
	}
	if m.Totals != got.Totals {
		return Manifest{}, fmt.Errorf("%w: totals do not match the archive", ErrIntegrity)
	}
	return m, nil
}

// ReadManifest reads manifest.json without checking integrity. Restore must
// follow it with Verify before replacing data.
func ReadManifest(dir string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse backup manifest: %w", err)
	}
	return m, nil
}

// WriteManifest atomically replaces manifest.json. The caller writes it only
// after archive verification and immediately before publishing, so the
// complete flag is the last content written.
func WriteManifest(dir string, m Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, ManifestFile)); err != nil {
		return fmt.Errorf("publish manifest: %w", err)
	}
	return nil
}

// Artifact is one valid published backup directory.
type Artifact struct {
	Path     string   `json:"path"`
	Manifest Manifest `json:"manifest"`
}

// List returns only published backups with a complete, supported manifest.
// Directories still being written (`.partial`) and interrupted leftovers are
// ignored, never presented as valid backups.
func List(root string) ([]Artifact, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), PartialSuffix) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := ReadManifest(dir)
		if err != nil || checkManifestHeader(m) != nil {
			continue
		}
		out = append(out, Artifact{Path: dir, Manifest: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func checkManifestHeader(m Manifest) error {
	if m.Schema != Schema {
		return fmt.Errorf("%w: schema %q", ErrUnsupported, m.Schema)
	}
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("%w: format_version %d", ErrUnsupported, m.FormatVersion)
	}
	if !m.Complete {
		return ErrIncomplete
	}
	return nil
}

func fileSHA256(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func readArchive(path, dest string) (InspectResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return InspectResult{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return InspectResult{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var result InspectResult
	seen := map[string]string{}
	var dirs []Entry
	var links []Entry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return InspectResult{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
		}
		entry, err := inspectHeader(hdr)
		if err != nil {
			return InspectResult{}, err
		}
		if existing, ok := seen[entry.Path]; ok {
			return InspectResult{}, fmt.Errorf("%w: duplicate entry %q (also %s)", ErrIntegrity, entry.Path, existing)
		}
		seen[entry.Path] = entry.Type
		result.Entries = append(result.Entries, entry)
		addTotals(&result.Totals, entry)
		switch entry.Type {
		case EntryDir:
			dirs = append(dirs, entry)
			if dest != "" {
				if err := os.MkdirAll(memberPath(dest, entry.Path), 0o700); err != nil {
					return InspectResult{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
				}
			}
		case EntryFile:
			if err := readFile(tr, dest, &entry); err != nil {
				return InspectResult{}, err
			}
			result.Entries[len(result.Entries)-1] = entry
		case EntrySymlink, EntryHardlink:
			links = append(links, entry)
		case EntryOther:
			// FIFOs and device nodes carry no readable payload. They are
			// recorded but not materialized during host verification.
		}
	}

	// Draining the gzip stream forces the CRC/ISIZE trailer check. A truncated
	// archive can list every name and still fail here.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return InspectResult{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}

	if dest != "" {
		if err := extractLinks(dest, links); err != nil {
			return InspectResult{}, err
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			if mode, err := parseMode(dirs[i].Mode); err == nil && dirs[i].Path != "./" && dirs[i].Path != "." {
				_ = os.Chmod(memberPath(dest, dirs[i].Path), mode)
			}
		}
	}
	return result, nil
}

func inspectHeader(hdr *tar.Header) (Entry, error) {
	name := strings.TrimRight(hdr.Name, "/")
	if name == "" {
		name = "./"
	}
	if err := validateMember(name); err != nil {
		return Entry{}, err
	}
	e := Entry{
		Path: name,
		Mode: fmt.Sprintf("%04o", hdr.Mode&0o7777),
		UID:  hdr.Uid,
		GID:  hdr.Gid,
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		e.Type = EntryDir
	case tar.TypeReg, tar.TypeRegA:
		e.Type = EntryFile
		e.SizeBytes = hdr.Size
	case tar.TypeSymlink:
		e.Type = EntrySymlink
		e.LinkTarget = hdr.Linkname
	case tar.TypeLink:
		e.Type = EntryHardlink
		e.LinkTarget = hdr.Linkname
	case tar.TypeFifo, tar.TypeChar, tar.TypeBlock:
		e.Type = EntryOther
	default:
		return Entry{}, fmt.Errorf("%w: unsupported tar entry type %q for %q", ErrUnsupported, hdr.Typeflag, name)
	}
	return e, nil
}

// readFile hashes one regular entry and, when dest is set, writes the same
// bytes into the fresh verification directory.
func readFile(tr *tar.Reader, dest string, entry *Entry) error {
	h := sha256.New()
	var out io.Writer = io.Discard
	var f *os.File
	if dest != "" {
		path := memberPath(dest, entry.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("%w: %v", ErrIntegrity, err)
		}
		var err error
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrIntegrity, err)
		}
		out = f
	}
	n, err := io.Copy(io.MultiWriter(h, out), io.LimitReader(tr, entry.SizeBytes))
	if f != nil {
		if chmodErr := f.Chmod(mustMode(entry.Mode)); chmodErr != nil && err == nil {
			err = chmodErr
		}
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return fmt.Errorf("%w: read %q: %v", ErrIntegrity, entry.Path, err)
	}
	if n != entry.SizeBytes {
		return fmt.Errorf("%w: %q is truncated", ErrIntegrity, entry.Path)
	}
	entry.SHA256 = hex.EncodeToString(h.Sum(nil))
	return nil
}

func mustMode(raw string) os.FileMode {
	mode, err := parseMode(raw)
	if err != nil {
		return 0o600
	}
	return mode
}

func extractLinks(dest string, links []Entry) error {
	for _, entry := range links {
		path := memberPath(dest, entry.Path)
		switch entry.Type {
		case EntrySymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("%w: %v", ErrIntegrity, err)
			}
			if err := os.Symlink(entry.LinkTarget, path); err != nil {
				return fmt.Errorf("%w: %v", ErrIntegrity, err)
			}
		case EntryHardlink:
			if err := validateMember(entry.LinkTarget); err != nil {
				return err
			}
			target := memberPath(dest, entry.LinkTarget)
			info, err := os.Lstat(target)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%w: hardlink target %q is not an extracted file", ErrIntegrity, entry.LinkTarget)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("%w: %v", ErrIntegrity, err)
			}
			if err := os.Link(target, path); err != nil {
				return fmt.Errorf("%w: %v", ErrIntegrity, err)
			}
		}
	}
	return nil
}

func validateMember(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty entry name", ErrUnsafePath)
	}
	if strings.ContainsRune(name, 0) || strings.Contains(name, "\\") {
		return fmt.Errorf("%w: %q contains an invalid character", ErrUnsafePath, name)
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return fmt.Errorf("%w: absolute entry %q", ErrUnsafePath, name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("%w: parent traversal in %q", ErrUnsafePath, name)
		}
	}
	return nil
}

func memberPath(root, name string) string {
	clean := strings.TrimPrefix(name, "./")
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(clean))
}

func parseMode(raw string) (os.FileMode, error) {
	var mode uint32
	if _, err := fmt.Sscanf(raw, "%o", &mode); err != nil {
		return 0, err
	}
	return os.FileMode(mode).Perm(), nil
}

func addTotals(t *Totals, e Entry) {
	t.Entries++
	switch e.Type {
	case EntryFile:
		t.Files++
		t.Bytes += e.SizeBytes
	case EntryDir:
		t.Dirs++
	case EntrySymlink:
		t.Symlinks++
	case EntryHardlink:
		t.Hardlinks++
	default:
		t.Other++
	}
}

func compareEntries(want, got []Entry) error {
	wantByPath := make(map[string]Entry, len(want))
	for _, e := range want {
		wantByPath[e.Path] = e
	}
	gotByPath := make(map[string]Entry, len(got))
	for _, e := range got {
		gotByPath[e.Path] = e
	}
	if len(want) != len(got) {
		return fmt.Errorf("%w: manifest lists %d entries, archive has %d", ErrIntegrity, len(want), len(got))
	}
	for path, expected := range wantByPath {
		actual, ok := gotByPath[path]
		if !ok {
			return fmt.Errorf("%w: archive is missing %q", ErrIntegrity, path)
		}
		if expected.Type != actual.Type || expected.SHA256 != actual.SHA256 ||
			expected.SizeBytes != actual.SizeBytes || expected.LinkTarget != actual.LinkTarget {
			return fmt.Errorf("%w: entry %q does not match the manifest", ErrIntegrity, path)
		}
	}
	return nil
}
