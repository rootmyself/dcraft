package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrOutputRequired is returned when no explicit output directory was chosen.
// dcraft never picks a hidden backup location.
var ErrOutputRequired = errors.New("backup output directory is required")

// ErrOutputInvalid reports a path that is not a writable directory.
var ErrOutputInvalid = errors.New("backup output directory is not usable")

var safeComponentRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ValidateOutputDir resolves and proves a user-chosen output directory before
// any service is stopped. Relative paths resolve against cwd. The directory is
// created when missing and checked with a temporary write probe that is
// removed again. The returned path is absolute and symlink-resolved.
func ValidateOutputDir(raw, cwd string) (string, error) {
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
	abs = filepath.Clean(abs)
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

// artifactName builds a readable, filesystem-safe directory name for one
// backup. Timestamps are UTC and second-granularity; a collision gets a
// numeric suffix so two backups are both kept.
func artifactName(source Source, now time.Time) string {
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

// artifactPath joins one generated name bellow root and proves containment.
func artifactPath(root, name string) (string, error) {
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

// uniqueArtifactPath returns the first free final/partial pair for base.
func uniqueArtifactPath(root, base string) (string, error) {
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		final, err := artifactPath(root, name)
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
