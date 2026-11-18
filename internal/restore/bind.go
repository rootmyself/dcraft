package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// BindDestination is the host-folder implementation of Destination. It only
// ever operates on one bind source declared by the current stack. The archived
// source.host_path is machine-specific, so Match requires the same current path
// unless an explicit path mapping was requested; project/environment mapping
// keeps its own independent explicit flag.
//
// Preparation, safety backup, emptying, extraction and verification are all
// host-side. Emptying removes the folder's content (never the folder or any
// other folder) and extraction writes through os.Root so a pre-existing or
// archive-provided symlink cannot redirect a write outside the target.
type BindDestination struct {
	backend     data.Backend
	source      data.DataSource
	target      core.Target
	project     string
	environment string
	baseDir     string

	// path is the symlink-resolved absolute target folder. Prepare re-resolves
	// it and records the observed directory identity; later destructive steps
	// refuse a target replaced by another object.
	path     string
	prepared os.FileInfo

	explicitPath bool

	rootModTime  time.Time
	rootMetadata bool
}

// NewBindDestination builds one bind-folder destination. target is the resolved
// Compose target; project is the same effective name used by status/lifecycle.
func NewBindDestination(backend data.Backend, source data.DataSource, target core.Target, project string) *BindDestination {
	baseDir := target.ProjectDir
	if len(target.Files) > 0 {
		baseDir = filepath.Dir(target.Files[0])
	}
	return &BindDestination{
		backend: backend, source: source, target: target, project: project,
		environment: target.Environment, baseDir: baseDir, path: source.Name,
	}
}

func (d *BindDestination) Kind() data.Kind { return data.KindBind }

// Key is the configured host path (the Compose spelling), Name is the
// symlink-resolved absolute path that is actually replaced.
func (d *BindDestination) Key() string {
	if declared := strings.TrimSpace(d.source.Declared); declared != "" {
		return declared
	}
	return d.currentPath()
}

func (d *BindDestination) Name() string { return d.currentPath() }

func (d *BindDestination) Services() []string { return append([]string{}, d.source.Services...) }

func (d *BindDestination) MountTargets() []string {
	return append([]string{}, d.source.MountTargets...)
}

func (d *BindDestination) ManifestSource() backup.Source {
	return backup.SourceFrom(d.source, d.project, d.environment, d.target.ProjectDir)
}

func (d *BindDestination) currentPath() string {
	if strings.TrimSpace(d.path) != "" {
		return d.path
	}
	return d.source.Name
}

// Match verifies the archived identity and the bind-specific shapes before any
// service is stopped. A different current path is refused by default; the
// explicit path mapping flag turns it into a reported warning.
func (d *BindDestination) Match(m backup.Manifest, explicitSource bool) ([]string, error) {
	if m.Source.Kind != data.KindBind {
		return nil, fmt.Errorf("%w: artifact kind %q, target kind %q", ErrTargetMismatch, m.Source.Kind, data.KindBind)
	}
	if err := validateBindManifest(m); err != nil {
		return nil, err
	}
	var warnings []string
	mismatch := func(field, have, want string) error {
		if have == want {
			return nil
		}
		if !explicitSource {
			return fmt.Errorf("%w: %s %q, target %q", ErrTargetMismatch, field, have, want)
		}
		warnings = append(warnings, fmt.Sprintf("%s가 백업과 다릅니다: 백업 %q → 대상 %q (명시적 매핑)", field, have, want))
		return nil
	}
	if err := mismatch("project", m.Source.Project, d.project); err != nil {
		return nil, err
	}
	if err := mismatch("environment", m.Source.Environment, d.environment); err != nil {
		return nil, err
	}
	if !d.matchesArchivedPath(m.Source.HostPath) {
		if !d.explicitPath {
			return nil, fmt.Errorf("%w: 연결 폴더 경로가 백업과 다릅니다: 백업 %q, 현재 선언 %q (의도한 경로 매핑이면 --bind와 --allow-path-mismatch를 함께 지정하세요)", ErrTargetMismatch, m.Source.HostPath, d.currentPath())
		}
		warnings = append(warnings, fmt.Sprintf("연결 폴더 경로가 백업과 다릅니다: 백업 %q → 대상 %q (명시적 경로 매핑)", m.Source.HostPath, d.currentPath()))
	}
	return warnings, nil
}

func (d *BindDestination) matchesArchivedPath(archived string) bool {
	if strings.TrimSpace(archived) == "" {
		return false
	}
	spellings := append([]string{d.currentPath(), d.source.Declared}, d.source.DeclaredPaths...)
	for _, spelling := range spellings {
		if sameHostPath(archived, spelling) {
			return true
		}
	}
	return false
}

// sameHostPath compares two host paths after resolving every existing symlink
// and appending the missing suffix lexically (so an artifact path recorded on a
// symlinked spelling still matches the same folder).
func sameHostPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	return resolvePath(a) == resolvePath(b)
}

// Outside reports whether a host path is outside the target folder. A named
// volume has no host path; only this bind destination performs the real check.
func (d *BindDestination) Outside(hostPath string) (bool, error) {
	if strings.TrimSpace(hostPath) == "" {
		return false, fmt.Errorf("%w: empty host path", ErrSafetyInvalid)
	}
	return !within(d.currentPath(), hostPath), nil
}

// Prepare proves the target exists, is a real directory, is writable, and is
// not the project folder or a filesystem root before any service is stopped.
// It never destroys content; the write probe is removed immediately.
func (d *BindDestination) Prepare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw := strings.TrimSpace(d.source.Name)
	if raw == "" {
		raw = strings.TrimSpace(d.source.Declared)
	}
	if raw == "" {
		return fmt.Errorf("%w: empty bind target", ErrTargetInvalid)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrTargetMissing, raw)
		}
		return fmt.Errorf("%w: %s: %v", ErrTargetInvalid, raw, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrTargetMissing, resolved)
		}
		return fmt.Errorf("%w: %s: %v", ErrTargetInvalid, resolved, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrTargetInvalid, resolved)
	}
	if filepath.Dir(resolved) == resolved {
		return fmt.Errorf("%w: %s is a filesystem root and would sweep unrelated folders", ErrTargetInvalid, resolved)
	}
	if projectDir := strings.TrimSpace(d.target.ProjectDir); projectDir != "" {
		projectResolved := resolvePath(projectDir)
		if within(resolved, projectResolved) {
			return fmt.Errorf("%w: %s is the project folder or its parent; the project folder cannot be emptied", ErrTargetInvalid, resolved)
		}
	}
	probe, err := os.CreateTemp(resolved, ".dcraft-restore-probe-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable: %v", ErrTargetInvalid, resolved, err)
	}
	probeName := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probeName)
	if closeErr != nil {
		return fmt.Errorf("%w: %s write probe: %v", ErrTargetInvalid, resolved, closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("%w: %s write probe cleanup: %v", ErrTargetInvalid, resolved, removeErr)
	}
	d.path = resolved
	d.prepared = info
	return nil
}

// UnsafeConsumers queries every declared spelling of the folder (including the
// Docker-visible spelling) and refuses containers of another stack or an
// unmanaged container. Stopped containers still hold the mount reference.
func (d *BindDestination) UnsafeConsumers(ctx context.Context) ([]data.ContainerConsumer, error) {
	if d.backend == nil {
		return nil, fmt.Errorf("bind restore backend is not configured")
	}
	allowed := map[string]bool{}
	for _, svc := range d.source.Services {
		allowed[svc] = true
	}
	seenSpelling := map[string]bool{}
	seenContainer := map[string]bool{}
	var outside []data.ContainerConsumer
	spellings := append([]string{d.currentPath(), d.source.Declared}, d.source.DeclaredPaths...)
	for _, spelling := range spellings {
		if spelling == "" || seenSpelling[spelling] {
			continue
		}
		seenSpelling[spelling] = true
		consumers, err := d.backend.BindConsumers(ctx, d.target, spelling)
		if err != nil {
			return nil, err
		}
		for _, consumer := range consumers {
			if consumer.Project == d.project && consumer.Service != "" && allowed[consumer.Service] {
				continue
			}
			key := consumer.Name + "\x00" + consumer.Project
			if seenContainer[key] {
				continue
			}
			seenContainer[key] = true
			outside = append(outside, consumer)
		}
	}
	sort.Slice(outside, func(i, j int) bool {
		if outside[i].Project != outside[j].Project {
			return outside[i].Project < outside[j].Project
		}
		return outside[i].Name < outside[j].Name
	})
	return outside, nil
}

// Usage summarizes the current target content without following symlinks.
func (d *BindDestination) Usage(ctx context.Context) (ContentSummary, error) {
	if err := ctx.Err(); err != nil {
		return ContentSummary{}, err
	}
	var summary ContentSummary
	err := filepath.WalkDir(d.currentPath(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == d.currentPath() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			summary.Bytes += info.Size()
		}
		summary.Entries++
		return nil
	})
	if err != nil {
		return ContentSummary{}, err
	}
	return summary, nil
}

// SafetyBackup writes a host-side tar of the current folder into the partial
// safety artifact directory. The caller (CreateSafetyBackup) completes and
// verifies the v1 manifest before any replacement.
func (d *BindDestination) SafetyBackup(ctx context.Context, hostDir string) (data.OpResult, error) {
	if err := d.checkIdentity(); err != nil {
		return data.OpResult{}, err
	}
	if err := backup.ArchiveHostDir(ctx, d.currentPath(), filepath.Join(hostDir, backup.DataFile)); err != nil {
		return data.OpResult{}, err
	}
	return data.OpResult{}, nil
}

// Empty removes every entry below the target folder, including dotfiles,
// hidden directories and symlinks. It is the host-side equivalent of
// `find <target> -mindepth 1 -delete`: no shell glob is used, the folder root
// is never removed, and symlinks are unlinked, never followed.
func (d *BindDestination) Empty(ctx context.Context) (data.OpResult, error) {
	if err := d.checkIdentity(); err != nil {
		return data.OpResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return data.OpResult{}, err
	}
	root, err := os.OpenRoot(d.currentPath())
	if err != nil {
		return data.OpResult{}, fmt.Errorf("open target root: %w", err)
	}
	defer root.Close()
	entries, err := os.ReadDir(d.currentPath())
	if err != nil {
		return data.OpResult{}, fmt.Errorf("read target: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return data.OpResult{}, err
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			return data.OpResult{}, fmt.Errorf("remove %s: %w", entry.Name(), err)
		}
	}
	remaining, err := os.ReadDir(d.currentPath())
	if err != nil {
		return data.OpResult{}, fmt.Errorf("re-read target: %w", err)
	}
	if len(remaining) != 0 {
		return data.OpResult{}, fmt.Errorf("target is still not empty: %d entries remain", len(remaining))
	}
	return data.OpResult{}, d.checkIdentity()
}

// checkIdentity refuses a target that was replaced after Prepare. The lock
// protects against another dcraft operation; this check covers an unrelated
// host process swapping the directory.
func (d *BindDestination) checkIdentity() error {
	if d.prepared == nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(d.currentPath())
	if err != nil {
		return fmt.Errorf("%w: target disappeared: %v", ErrTargetInvalid, err)
	}
	if resolved != d.currentPath() {
		return fmt.Errorf("%w: target path now resolves to %s", ErrTargetInvalid, resolved)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTargetInvalid, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: target is no longer a directory", ErrTargetInvalid)
	}
	if !os.SameFile(d.prepared, info) {
		return fmt.Errorf("%w: target directory was replaced during the operation", ErrTargetInvalid)
	}
	return nil
}

// validateBindManifest is the storage-specific preflight for a bind artifact.
// It refuses special files (a host restore must fail explicitly rather than
// silently skip them) and link/path shapes that cannot be represented or
// safely materialized as a host folder.
func validateBindManifest(m backup.Manifest) error {
	src := m.Source
	if src.Kind != data.KindBind {
		return fmt.Errorf("%w: artifact kind %q", ErrTargetMismatch, src.Kind)
	}
	hostPath := strings.TrimSpace(src.HostPath)
	if hostPath == "" || !filepath.IsAbs(hostPath) {
		return fmt.Errorf("%w: bind host path %q is not absolute", ErrArtifactInvalid, src.HostPath)
	}
	clean := filepath.Clean(hostPath)
	if filepath.Dir(clean) == clean {
		return fmt.Errorf("%w: bind host path %q is a filesystem root", ErrArtifactInvalid, hostPath)
	}
	if src.VolumeKey != "" || src.VolumeName != "" {
		return fmt.Errorf("%w: bind source carries volume identity", ErrArtifactInvalid)
	}

	byPath := map[string]backup.Entry{}
	dirs := map[string]bool{}
	for _, entry := range m.Entries {
		p := normalizeEntryPath(entry.Path)
		if p == "" {
			if entry.Type != backup.EntryDir {
				return fmt.Errorf("%w: root entry is %q, not a directory", ErrArtifactInvalid, entry.Type)
			}
			continue
		}
		byPath[p] = entry
		if entry.Type == backup.EntryDir {
			dirs[p] = true
		}
		switch entry.Type {
		case backup.EntryFile, backup.EntryDir:
		case backup.EntrySymlink:
			if strings.TrimSpace(entry.LinkTarget) == "" || strings.ContainsRune(entry.LinkTarget, 0) {
				return fmt.Errorf("%w: symlink %q has no representable target", ErrArtifactInvalid, entry.Path)
			}
		case backup.EntryHardlink:
			target := normalizeEntryPath(entry.LinkTarget)
			if target == "" || strings.ContainsRune(entry.LinkTarget, 0) {
				return fmt.Errorf("%w: hardlink %q has no representable target", ErrArtifactInvalid, entry.Path)
			}
		case backup.EntryOther:
			return fmt.Errorf("%w: %q is a special file (FIFO/device) that a host bind restore cannot recreate", ErrTargetUnsupported, entry.Path)
		default:
			return fmt.Errorf("%w: unsupported entry type %q", ErrArtifactInvalid, entry.Type)
		}
	}

	// Every non-root entry must have real directory entries for its parents.
	// Extraction never creates implicit parents, so a missing directory would
	// turn into an extra entry or a misleading mode.
	for p := range byPath {
		parts := strings.Split(p, "/")
		for i := 1; i < len(parts); i++ {
			parent := strings.Join(parts[:i], "/")
			if !dirs[parent] {
				return fmt.Errorf("%w: %q has no directory entry for %q", ErrArtifactInvalid, p, parent)
			}
		}
	}
	for p, entry := range byPath {
		if entry.Type != backup.EntryHardlink {
			continue
		}
		target := normalizeEntryPath(entry.LinkTarget)
		te, ok := byPath[target]
		if !ok || te.Type != backup.EntryFile {
			return fmt.Errorf("%w: hardlink %q points to %q, which is not an archived file", ErrArtifactInvalid, p, entry.LinkTarget)
		}
	}
	return nil
}
