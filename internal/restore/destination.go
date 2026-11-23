package restore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/data"
)

// ErrTargetMissing reports a destination that does not exist (for example a
// volume removed after the backup was taken).
var ErrTargetMissing = errors.New("restore target does not exist")

// ContentSummary is a cheap description of current destination content used by
// the preview and the safety-backup space check.
type ContentSummary struct {
	Bytes   int64 `json:"bytes"`
	Entries int64 `json:"entries"`
}

// Destination is the published storage-specific half of safe replacement.
// Ticket #18 implements it for named volumes; bind-folder restore (#19)
// implements the same interface for host folders. The shared Executor owns
// validation, locking, approval-independent stop/safety/replace/verify/state
// sequencing and honest failure reporting so neither destination can skip it.
type Destination interface {
	Kind() data.Kind
	// Key is the Compose volume key (volume) or the configured host path
	// (bind). Name is the actual engine volume name (volume) or the resolved
	// absolute host path (bind).
	Key() string
	Name() string
	Services() []string
	MountTargets() []string
	// ManifestSource is the identity this destination records in a safety
	// backup. It uses the same v1 format as the incoming artifact.
	ManifestSource() backup.Source
	// Match verifies the incoming manifest identifies this destination.
	// explicit permits a documented, warning-laden mapping and must not be
	// used silently.
	Match(manifest backup.Manifest, explicit bool) ([]string, error)
	// Outside reports whether a host path is outside the destination. A
	// named volume has no host path, so it is always outside.
	Outside(hostPath string) (bool, error)
	// Prepare proves the destination exists and is operable before any
	// service is stopped.
	Prepare(ctx context.Context) error
	// UnsafeConsumers lists containers that reference the target but belong
	// to another stack or an unmanaged service. Any result aborts the restore
	// without stopping anything.
	UnsafeConsumers(ctx context.Context) ([]data.ContainerConsumer, error)
	// Usage summarizes current content for the preview and space check.
	Usage(ctx context.Context) (ContentSummary, error)
	// SafetyBackup writes data.tar.gz of the current content into hostDir.
	// The volume implementation reads the source read-only.
	SafetyBackup(ctx context.Context, hostDir string) (data.OpResult, error)
	// Empty removes all destination content, never the root itself.
	Empty(ctx context.Context) (data.OpResult, error)
	// Extract writes the validated artifact data into the emptied target.
	Extract(ctx context.Context, artifactDir string) (data.OpResult, error)
	// Verify compares the restored content with the manifest.
	Verify(ctx context.Context, manifest backup.Manifest) (data.OpResult, error)
}

// MatchSource is the shared source↔target identity rule. The default requires
// the same project, environment and volume/folder identity; explicit mapping
// is allowed only with a caller-supplied warning.
func MatchSource(m backup.Manifest, target backup.Source, explicit bool) ([]string, error) {
	src := m.Source
	if src.Kind != target.Kind {
		return nil, fmt.Errorf("%w: artifact kind %q, target kind %q", ErrTargetMismatch, src.Kind, target.Kind)
	}
	var warnings []string
	mismatch := func(field, have, want string) error {
		if have == want {
			return nil
		}
		if !explicit {
			return fmt.Errorf("%w: %s %q, target %q", ErrTargetMismatch, field, have, want)
		}
		warnings = append(warnings, fmt.Sprintf("%s가 백업과 다름: 백업 %q → 대상 %q (명시적 매핑)", field, have, want))
		return nil
	}
	if err := mismatch("project", src.Project, target.Project); err != nil {
		return nil, err
	}
	if err := mismatch("environment", src.Environment, target.Environment); err != nil {
		return nil, err
	}
	switch target.Kind {
	case data.KindVolume:
		if err := mismatch("volume key", src.VolumeKey, target.VolumeKey); err != nil {
			return nil, err
		}
		if err := mismatch("volume name", src.VolumeName, target.VolumeName); err != nil {
			return nil, err
		}
	case data.KindBind:
		have, err := filepath.Abs(src.HostPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTargetMismatch, err)
		}
		want, err := filepath.Abs(target.HostPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTargetMismatch, err)
		}
		if err := mismatch("host path", have, want); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: unknown target kind %q", ErrTargetMismatch, target.Kind)
	}
	return warnings, nil
}

// ValidateLocation proves a host location is outside the destination and that
// the artifact and safety backup never contain each other. A named volume has
// no host path; bind restore (#19) performs the real containment check.
func ValidateLocation(dest Destination, artifactDir, safetyDir string) error {
	outside, err := dest.Outside(artifactDir)
	if err != nil {
		return err
	}
	if !outside {
		return fmt.Errorf("%w: artifact directory is inside the target", ErrTargetMismatch)
	}
	outside, err = dest.Outside(safetyDir)
	if err != nil {
		return err
	}
	if !outside {
		return fmt.Errorf("%w: safety backup directory is inside the target", ErrSafetyInvalid)
	}
	if within(artifactDir, safetyDir) || within(safetyDir, artifactDir) {
		return fmt.Errorf("%w: artifact and safety backup locations overlap", ErrSafetyInvalid)
	}
	return nil
}

// within reports whether child is path itself or below it. Non-existent paths
// are resolved through their nearest existing ancestor so symlinked temp roots
// (macOS /var → /private/var) do not hide containment.
func within(parent, child string) bool {
	p := resolvePath(parent)
	c := resolvePath(child)
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func resolvePath(path string) string {
	clean := filepath.Clean(path)
	suffix := ""
	cur := clean
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if suffix == "" {
				return resolved
			}
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		if suffix == "" {
			suffix = filepath.Base(cur)
		} else {
			suffix = filepath.Join(filepath.Base(cur), suffix)
		}
		cur = parent
	}
}
