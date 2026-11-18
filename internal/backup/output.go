package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// ErrOutputRequired is returned when no explicit output directory was chosen.
// dcraft never picks a hidden backup location.
var ErrOutputRequired = errors.New("backup output directory is required")

// ErrOutputInvalid reports a path that is not a writable directory.
var ErrOutputInvalid = errors.New("backup output directory is not usable")

var safeComponentRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ResolveOutputPath expands ~, makes the output absolute and cleans it without
// creating or probing anything. Callers that need a writable directory use
// ValidateOutputDir after the bind-containment checks.
func ResolveOutputPath(raw, cwd string) (string, error) {
	abs, err := absOutputPath(raw, cwd)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrOutputInvalid, abs)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, statErr)
	}
	return abs, nil
}

// ValidateOutputDir resolves and proves a user-chosen output directory before
// any service is stopped. Relative paths resolve against cwd. The directory is
// created when missing and checked with a temporary write probe that is
// removed again. The returned path is absolute and symlink-resolved.
func ValidateOutputDir(raw, cwd string) (string, error) {
	abs, err := absOutputPath(raw, cwd)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", ErrOutputInvalid, resolved)
	}
	probe, err := os.CreateTemp(resolved, ".dcraft-write-*")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	probeName := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probeName)
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, closeErr)
	}
	if err := os.Remove(probeName); err != nil {
		return "", fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	return resolved, nil
}

func absOutputPath(raw, cwd string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrOutputRequired
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: expand %q: %v", ErrOutputInvalid, raw, err)
		}
		if raw == "~" {
			raw = home
		} else if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		if strings.TrimSpace(cwd) == "" {
			return "", fmt.Errorf("%w: working directory is unknown", ErrOutputInvalid)
		}
		abs = filepath.Join(cwd, abs)
	}
	return filepath.Clean(abs), nil
}

// artifactName builds a readable, filesystem-safe directory name for one
// backup. Timestamps are UTC and second-granularity; a collision gets a
// numeric suffix so two backups are both kept.
// ArtifactName is the published naming rule for every v1 artifact (#17 uses
// it for bind-folder backups).
func ArtifactName(source Source, now time.Time) string {
	label := source.VolumeKey
	if label == "" {
		label = source.VolumeName
	}
	if label == "" {
		label = source.HostPath
	}
	parts := []string{"dcraft"}
	if project := safeComponent(source.Project); project != "" {
		parts = append(parts, project)
	}
	kind := safeComponent(string(source.Kind))
	if kind == "" {
		kind = "data"
	}
	parts = append(parts, kind, safeComponent(label), now.UTC().Format("20060102T150405Z"))
	return strings.Join(parts, "-")
}

func safeComponent(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = safeComponentRe.ReplaceAllString(raw, "_")
	for strings.Contains(raw, "..") {
		raw = strings.ReplaceAll(raw, "..", "_")
	}
	raw = strings.Trim(raw, "._-")
	if len(raw) > 48 {
		raw = raw[:48]
	}
	return raw
}

// ArtifactPath joins one generated name below root and proves containment.
func ArtifactPath(root, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%w: empty artifact name", ErrOutputInvalid)
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: artifact name %q contains a path separator", ErrOutputInvalid, name)
	}
	path := filepath.Join(root, name)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: artifact %q escapes the output directory", ErrOutputInvalid, name)
	}
	return path, nil
}

// UniqueArtifactPath returns the first free final/partial pair for base.
func UniqueArtifactPath(root, base string) (string, error) {
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		final, err := ArtifactPath(root, name)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(final); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, err := os.Lstat(final + PartialSuffix); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return final, nil
	}
	return "", fmt.Errorf("%w: no free artifact name for %q", ErrOutputInvalid, base)
}

// syncFile flushes one artifact file before it is published.
func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// syncDir flushes one directory entry so a rename or manifest replacement is
// durable. Filesystems that do not support directory fsync are treated as a
// no-op instead of a false failure.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

// invalidatePublished moves a published-but-not-complete artifact back to its
// partial name. If even that fails, the complete=false manifest still keeps it
// out of every valid-backup listing.
func invalidatePublished(final string, cause error) error {
	detail := cause.Error()
	if _, statErr := os.Lstat(final); statErr == nil {
		partial := final + PartialSuffix
		if _, existErr := os.Lstat(partial); errors.Is(existErr, os.ErrNotExist) {
			if renameErr := os.Rename(final, partial); renameErr == nil {
				detail += "\n" + fmt.Sprintf(msgPartialLocation, partial)
			} else {
				detail += "\n" + fmt.Sprintf(msgPartialLocation, final) + " (" + msgPartialKept + ")"
			}
		} else {
			detail += "\n" + fmt.Sprintf(msgPartialLocation, final) + " (" + msgPartialKept + ")"
		}
	}
	return core.FailCause(cause, core.Diagnostic{
		Category:    core.CategoryBackupFailed,
		Severity:    core.SeverityError,
		Message:     msgCompleteFailed,
		Detail:      detail,
		NextActions: []string{msgCompleteFailedAction, msgPublishFailedAction},
	})
}
