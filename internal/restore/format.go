// Package restore owns safe data replacement from a complete v1 backup. The
// backup format is consumed through backup.Verify; the storage-specific half of
// the operation is the Destination interface, which bind-folder restore (#19)
// implements alongside the named-volume destination in this package.
package restore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/data"
)

// Errors of the restore contract. Callers classify diagnostics with errors.Is.
var (
	ErrBackupRequired    = errors.New("restore backup artifact path is required")
	ErrArtifactInvalid   = errors.New("backup artifact is not a complete valid dcraft backup")
	ErrTargetUnsupported = errors.New("backup data source kind is not supported by this restore command")
	ErrTargetMismatch    = errors.New("backup source does not match the restore target")
	ErrUnsafeLink        = errors.New("backup contains a dangerous link")
	ErrSafetyInvalid     = errors.New("safety backup location is not usable")
	ErrSpaceInsufficient = errors.New("insufficient free space for the safety backup")
)

// Artifact is one validated backup directory.
type Artifact struct {
	Path     string
	Manifest backup.Manifest
}

// ValidateArtifact resolves and fully verifies a backup before anything is
// stopped or overwritten. It requires a supported schema, the completion
// marker, a matching gzip/tar hash and per-entry hashes (backup.Verify), a
// stable source identity (volume or bind), no duplicate normalized destination
// paths, and no link component used as a directory. The storage-specific
// command decides whether the source kind matches its destination.
func ValidateArtifact(raw, cwd string) (Artifact, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Artifact{}, ErrBackupRequired
	}
	path, err := expandPath(raw, cwd)
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	if !info.IsDir() {
		return Artifact{}, fmt.Errorf("%w: %s is not a backup directory", ErrArtifactInvalid, resolved)
	}
	m, err := backup.Verify(resolved)
	if err != nil {
		return Artifact{}, err
	}
	if err := validateSource(m.Source); err != nil {
		return Artifact{}, err
	}
	if err := validateEntries(m.Entries); err != nil {
		return Artifact{}, err
	}
	return Artifact{Path: resolved, Manifest: m}, nil
}

// validateEntries rejects two spellings of the same destination path and any
// symlink used as a parent directory.
func validateEntries(entries []backup.Entry) error {
	seen := map[string]string{}
	for _, e := range entries {
		p := normalizeEntryPath(e.Path)
		if prev, ok := seen[p]; ok {
			return fmt.Errorf("%w: duplicate destination %q (also %s)", ErrArtifactInvalid, e.Path, prev)
		}
		seen[p] = e.Type
	}
	return validateLinks(entries)
}

func validateSource(src backup.Source) error {
	switch src.Kind {
	case data.KindVolume:
		if strings.TrimSpace(src.Project) == "" || strings.TrimSpace(src.VolumeKey) == "" || strings.TrimSpace(src.VolumeName) == "" {
			return fmt.Errorf("%w: volume source identity is incomplete", ErrArtifactInvalid)
		}
	case data.KindBind:
		if strings.TrimSpace(src.Project) == "" || strings.TrimSpace(src.HostPath) == "" {
			return fmt.Errorf("%w: bind source identity is incomplete", ErrArtifactInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown source kind %q", ErrArtifactInvalid, src.Kind)
	}
	return nil
}

// validateLinks rejects an archive that uses a symlink as a parent directory
// of another entry. Such an archive can turn a member path into a write through
// a link; GNU/BusyBox extractors have historically followed pre-existing
// destination links, so restore never relies on their behavior.
func validateLinks(entries []backup.Entry) error {
	links := map[string]bool{}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		p := normalizeEntryPath(e.Path)
		paths = append(paths, p)
		if e.Type == backup.EntrySymlink {
			links[p] = true
		}
	}
	if len(links) == 0 {
		return nil
	}
	for _, p := range paths {
		parts := strings.Split(p, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if links[parent] {
				return fmt.Errorf("%w: %q is below symlink %q", ErrUnsafeLink, p, parent)
			}
		}
	}
	return nil
}

func normalizeEntryPath(p string) string {
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// CheckArtifactData re-checks the data payload hash and size against the
// manifest immediately before a destructive step. It is much cheaper than a
// full Verify and catches a file changed after approval/validation.
func CheckArtifactData(a Artifact) error {
	f, err := os.Open(filepath.Join(a.Path, backup.DataFile))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	if n != a.Manifest.Data.SizeBytes || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.Manifest.Data.SHA256) {
		return fmt.Errorf("%w: archive changed after validation", ErrArtifactInvalid)
	}
	return nil
}

func expandPath(raw, cwd string) (string, error) {
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		switch {
		case raw == "~":
			raw = home
		case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`):
			raw = filepath.Join(home, raw[2:])
		}
	}
	if !filepath.IsAbs(raw) {
		if strings.TrimSpace(cwd) == "" {
			return "", errors.New("working directory is unknown")
		}
		raw = filepath.Join(cwd, raw)
	}
	return filepath.Clean(raw), nil
}
