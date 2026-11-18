package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rootmyself/dcraft/internal/data"
)

// ErrBindInvalid reports a declared bind source that cannot be read as a host
// folder (missing, not a directory, unreadable or an unsupported entry).
var ErrBindInvalid = errors.New("bind folder is not usable")

// ErrBindUnsafe reports a bind selection that would copy the backup output
// recursively or sweep a filesystem root.
var ErrBindUnsafe = errors.New("bind folder selection is unsafe")

// BindPathError names the exact host path and reason a bind source cannot be
// archived. Reason is stable machine data; the controller maps it to a Korean
// diagnostic and exit code.
type BindPathError struct {
	Path      string
	OutputDir string
	Reason    string
	Cause     error
}

const (
	BindReasonMissing        = "missing"
	BindReasonNotDirectory   = "not_directory"
	BindReasonUnreadable     = "unreadable"
	BindReasonFilesystemRoot = "filesystem_root"
	BindReasonOutputInside   = "output_inside_source"
	BindReasonUnsafeName     = "unsupported_name"
)

func (e *BindPathError) Error() string {
	switch e.Reason {
	case BindReasonOutputInside:
		return fmt.Sprintf("backup output %q is inside bind folder %q; the archive would include itself", e.OutputDir, e.Path)
	case BindReasonFilesystemRoot:
		return fmt.Sprintf("bind folder %q is a filesystem root and would sweep unrelated folders", e.Path)
	case BindReasonMissing:
		return fmt.Sprintf("bind folder %q does not exist", e.Path)
	case BindReasonNotDirectory:
		return fmt.Sprintf("bind source %q is not a folder", e.Path)
	case BindReasonUnreadable:
		return fmt.Sprintf("bind folder %q is not readable", e.Path)
	default:
		return fmt.Sprintf("bind folder %q cannot be archived: %s", e.Path, e.Reason)
	}
}

func (e *BindPathError) Unwrap() error {
	if e.Reason == BindReasonFilesystemRoot || e.Reason == BindReasonOutputInside {
		return ErrBindUnsafe
	}
	return ErrBindInvalid
}

// UnknownBindError reports a --bind value that is not one of the selected
// stack's declared bind folders. Available lists the declared host paths.
type UnknownBindError struct {
	Requested []string
	Available []string
}

func (e *UnknownBindError) Error() string {
	return fmt.Sprintf("unknown bind folder %q (declared: %s)",
		strings.Join(e.Requested, ", "), strings.Join(e.Available, ", "))
}

// AmbiguousBindError reports a mount target shared by several bind folders.
type AmbiguousBindError struct {
	Requested string
	Matches   []string
}

func (e *AmbiguousBindError) Error() string {
	return fmt.Sprintf("bind reference %q matches several folders: %s",
		e.Requested, strings.Join(e.Matches, ", "))
}

// BindAsVolumeError explains a host folder passed through --volume.
type BindAsVolumeError struct{ Reference string }

func (e *BindAsVolumeError) Error() string {
	return fmt.Sprintf("%q is a bind-mounted host folder; select it with --bind instead of --volume", e.Reference)
}

// NormalizeBindSource resolves a declared bind source for display, containment
// checks and archiving. Declared keeps the path exactly as Compose declared it
// (after making it absolute); Name is the symlink-resolved absolute path.
// A missing path is kept lexically and rejected later with a clear error.
func NormalizeBindSource(src data.DataSource, baseDir string) data.DataSource {
	if src.Kind != data.KindBind {
		return src
	}
	declared := strings.TrimSpace(src.Declared)
	if declared == "" {
		declared = src.Name
	}
	abs, err := absHostPath(declared, baseDir)
	if err != nil {
		src.Declared = declared
		src.Name = declared
		return src
	}
	src.Declared = abs
	src.Name = abs
	if len(src.DeclaredPaths) == 0 {
		src.DeclaredPaths = []string{abs}
	} else if !contains(src.DeclaredPaths, abs) {
		src.DeclaredPaths = append(src.DeclaredPaths, abs)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		src.Name = resolved
	}
	return src
}

// absHostPath expands ~ and resolves a relative path against baseDir. Compose
// itself resolves relative bind sources against the first Compose file's
// directory; callers pass that directory so an explicit --bind value follows
// the same rule.
func absHostPath(raw, baseDir string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty path", ErrBindInvalid)
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: expand %q: %v", ErrBindInvalid, raw, err)
		}
		if raw == "~" {
			raw = home
		} else {
			raw = filepath.Join(home, raw[2:])
		}
	}
	if !filepath.IsAbs(raw) {
		if strings.TrimSpace(baseDir) == "" {
			return "", fmt.Errorf("%w: %q is relative and the Compose directory is unknown", ErrBindInvalid, raw)
		}
		raw = filepath.Join(baseDir, raw)
	}
	return filepath.Clean(raw), nil
}

// SelectableSources lists every data source the backup command can offer:
// named volumes and declared bind folders, in backend order. Anonymous volumes
// are never offered.
func SelectableSources(sources []data.DataSource) []data.DataSource {
	var volumes, binds []data.DataSource
	for _, src := range sources {
		switch {
		case src.Kind == data.KindVolume && !src.Anonymous && src.Key != "":
			volumes = append(volumes, src)
		case src.Kind == data.KindBind:
			binds = append(binds, src)
		}
	}
	return append(volumes, binds...)
}

// SelectSources resolves the user's volume and bind selection against the
// discovered sources. Only stack-declared sources are accepted; an arbitrary
// host path is an unknown reference, never an implicit addition. The result is
// volumes first, then binds, matching the fixed flag order.
func SelectSources(sources []data.DataSource, volumes, binds []string, baseDir string) ([]data.DataSource, error) {
	if len(volumes) == 0 && len(binds) == 0 {
		return nil, ErrVolumeRequired
	}
	var out []data.DataSource
	if len(volumes) > 0 {
		selected, err := SelectVolumeSources(sources, volumes)
		if err != nil {
			return nil, err
		}
		out = append(out, selected...)
	}
	if len(binds) > 0 {
		selected, err := selectBindSources(sources, binds, baseDir)
		if err != nil {
			return nil, err
		}
		out = append(out, selected...)
	}
	return out, nil
}

func selectBindSources(sources []data.DataSource, requested []string, baseDir string) ([]data.DataSource, error) {
	byPath := map[string]data.DataSource{}
	byMountTarget := map[string][]data.DataSource{}
	var available []string
	for _, src := range sources {
		if src.Kind != data.KindBind {
			continue
		}
		byPath[src.Name] = src
		if src.Declared != "" {
			byPath[src.Declared] = src
		}
		for _, declared := range src.DeclaredPaths {
			if declared != "" {
				byPath[declared] = src
			}
		}
		for _, target := range src.MountTargets {
			byMountTarget[target] = append(byMountTarget[target], src)
		}
		available = append(available, src.Name)
	}
	sort.Strings(available)

	seen := map[string]bool{}
	seenRequested := map[string]bool{}
	var out []data.DataSource
	for _, raw := range requested {
		ref := strings.TrimSpace(raw)
		if ref == "" || seenRequested[ref] {
			continue
		}
		seenRequested[ref] = true

		if matches := byMountTarget[ref]; len(matches) > 1 {
			var paths []string
			for _, match := range matches {
				paths = append(paths, match.Name)
			}
			sort.Strings(paths)
			return nil, &AmbiguousBindError{Requested: ref, Matches: paths}
		} else if len(matches) == 1 {
			if !seen[matches[0].Name] {
				seen[matches[0].Name] = true
				out = append(out, matches[0])
			}
			continue
		}

		normalized := ref
		if abs, err := absHostPath(ref, baseDir); err == nil {
			normalized = abs
			if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
				normalized = resolved
			}
		}
		src, ok := byPath[normalized]
		if !ok {
			src, ok = byPath[ref]
		}
		if !ok {
			for _, candidate := range sources {
				if candidate.Kind == data.KindVolume &&
					(candidate.Key == ref || candidate.Name == ref) {
					return nil, &BindAsVolumeError{Reference: ref}
				}
			}
			return nil, &UnknownBindError{Requested: []string{ref}, Available: available}
		}
		if seen[src.Name] {
			continue
		}
		seen[src.Name] = true
		out = append(out, src)
	}
	if len(out) == 0 {
		return nil, ErrVolumeRequired
	}
	return out, nil
}

// CheckBindSources validates every selected bind folder before any service is
// stopped. It requires an existing readable directory, refuses a filesystem
// root, and refuses an output directory inside the source (the archive would
// recursively include itself). It never creates or writes anything.
func CheckBindSources(sources []data.DataSource, outputPath string) error {
	var binds []data.DataSource
	for _, src := range sources {
		if src.Kind == data.KindBind {
			binds = append(binds, src)
		}
	}
	if len(binds) == 0 {
		return nil
	}
	var outputResolved string
	if strings.TrimSpace(outputPath) != "" {
		resolved, err := ResolveForContainment(outputPath)
		if err != nil {
			return fmt.Errorf("%w: resolve output %q: %v", ErrOutputInvalid, outputPath, err)
		}
		outputResolved = resolved
	}
	for _, src := range binds {
		path := src.Name
		info, err := os.Lstat(path)
		if err != nil {
			return &BindPathError{Path: path, Reason: BindReasonMissing, Cause: err}
		}
		if !info.IsDir() {
			return &BindPathError{Path: path, Reason: BindReasonNotDirectory}
		}
		if filepath.Dir(path) == path {
			return &BindPathError{Path: path, Reason: BindReasonFilesystemRoot}
		}
		dir, err := os.Open(path)
		if err != nil {
			return &BindPathError{Path: path, Reason: BindReasonUnreadable, Cause: err}
		}
		_, readErr := dir.Readdirnames(1)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return &BindPathError{Path: path, Reason: BindReasonUnreadable, Cause: readErr}
		}
		if closeErr != nil {
			return &BindPathError{Path: path, Reason: BindReasonUnreadable, Cause: closeErr}
		}
		if outputResolved != "" && isPathWithin(outputResolved, path) {
			return &BindPathError{Path: path, OutputDir: outputResolved, Reason: BindReasonOutputInside}
		}
	}
	return nil
}

// ResolveForContainment resolves every symlink that exists in path and appends
// the missing suffix lexically. It is used to compare an output directory that
// does not exist yet against the selected sources before creating it.
func ResolveForContainment(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return "", resolveErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", resolveErr
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// isPathWithin reports whether child is parent or lies below it.
func isPathWithin(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
