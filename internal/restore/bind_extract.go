package restore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/data"
)

// Extract writes the validated artifact into the emptied host folder. Parents
// are created through os.Root, which never lets a path component escape the
// target; hardlinks are created after all files exist. Every entry is matched
// against the validated manifest while it is written, so the archive cannot
// silently add or change data after validation.
func (d *BindDestination) Extract(ctx context.Context, artifactDir string) (data.OpResult, error) {
	if err := d.checkIdentity(); err != nil {
		return data.OpResult{}, err
	}
	m, err := backup.ReadManifest(artifactDir)
	if err != nil {
		return data.OpResult{}, err
	}
	if err := validateBindManifest(m); err != nil {
		return data.OpResult{}, err
	}
	byPath := manifestEntries(m)
	if _, ok := byPath[""]; !ok {
		return data.OpResult{}, fmt.Errorf("%w: bind backup has no root directory entry", ErrArtifactInvalid)
	}
	empty, err := os.ReadDir(d.currentPath())
	if err != nil {
		return data.OpResult{}, fmt.Errorf("%w: %v", ErrTargetInvalid, err)
	}
	if len(empty) != 0 {
		return data.OpResult{}, fmt.Errorf("%w: target is not empty before extraction; a plain extract would be a merge", ErrTargetInvalid)
	}

	f, err := os.Open(filepath.Join(artifactDir, backup.DataFile))
	if err != nil {
		return data.OpResult{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return data.OpResult{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	root, err := os.OpenRoot(d.currentPath())
	if err != nil {
		return data.OpResult{}, fmt.Errorf("%w: %v", ErrTargetInvalid, err)
	}
	defer root.Close()

	seen := map[string]bool{}
	var dirs []dirMetadata
	var hardlinks []linkMetadata
	for {
		if err := ctx.Err(); err != nil {
			return data.OpResult{}, err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return data.OpResult{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
		}
		rel := normalizeEntryPath(hdr.Name)
		entry, ok := byPath[rel]
		if !ok {
			return data.OpResult{}, fmt.Errorf("%w: archive entry %q is not in the manifest", ErrArtifactInvalid, hdr.Name)
		}
		if seen[rel] {
			return data.OpResult{}, fmt.Errorf("%w: duplicate archive entry %q", ErrArtifactInvalid, hdr.Name)
		}
		seen[rel] = true
		if rel == "" {
			if entry.Type != backup.EntryDir {
				return data.OpResult{}, fmt.Errorf("%w: root entry is %q", ErrArtifactInvalid, entry.Type)
			}
			d.rootModTime = hdr.ModTime
			d.rootMetadata = true
			continue
		}
		mode, modeErr := bindMode(entry.Mode)
		if modeErr != nil {
			return data.OpResult{}, fmt.Errorf("%w: mode of %q: %v", ErrArtifactInvalid, entry.Path, modeErr)
		}
		switch entry.Type {
		case backup.EntryFile:
			if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
				return data.OpResult{}, fmt.Errorf("%w: %q is not a regular file in the archive", ErrArtifactInvalid, entry.Path)
			}
			if err := extractBindFile(ctx, root, tr, hdr, rel, entry, mode); err != nil {
				return data.OpResult{}, err
			}
		case backup.EntryDir:
			if hdr.Typeflag != tar.TypeDir {
				return data.OpResult{}, fmt.Errorf("%w: %q is not a directory in the archive", ErrArtifactInvalid, entry.Path)
			}
			if err := root.MkdirAll(rel, 0o700); err != nil {
				return data.OpResult{}, fmt.Errorf("create directory %q: %w", entry.Path, err)
			}
			dirs = append(dirs, dirMetadata{path: rel, mode: mode, mtime: hdr.ModTime})
		case backup.EntrySymlink:
			if hdr.Typeflag != tar.TypeSymlink {
				return data.OpResult{}, fmt.Errorf("%w: %q is not a symlink in the archive", ErrArtifactInvalid, entry.Path)
			}
			if hdr.Linkname != entry.LinkTarget {
				return data.OpResult{}, fmt.Errorf("%w: %q link target %q, manifest %q", ErrArtifactInvalid, entry.Path, hdr.Linkname, entry.LinkTarget)
			}
			if err := ensureBindParent(root, rel); err != nil {
				return data.OpResult{}, err
			}
			if err := root.Symlink(entry.LinkTarget, rel); err != nil {
				return data.OpResult{}, fmt.Errorf("create symlink %q: %w", entry.Path, err)
			}
		case backup.EntryHardlink:
			if hdr.Typeflag != tar.TypeLink {
				return data.OpResult{}, fmt.Errorf("%w: %q is not a hardlink in the archive", ErrArtifactInvalid, entry.Path)
			}
			if normalizeEntryPath(hdr.Linkname) != normalizeEntryPath(entry.LinkTarget) {
				return data.OpResult{}, fmt.Errorf("%w: %q hardlink target %q, manifest %q", ErrArtifactInvalid, entry.Path, hdr.Linkname, entry.LinkTarget)
			}
			hardlinks = append(hardlinks, linkMetadata{path: rel, target: normalizeEntryPath(entry.LinkTarget)})
		default:
			return data.OpResult{}, fmt.Errorf("%w: unsupported entry %q", ErrArtifactInvalid, entry.Path)
		}
	}

	// Draining the gzip stream forces the CRC/ISIZE trailer check. A
	// truncated archive can list every entry and still fail here.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return data.OpResult{}, fmt.Errorf("%w: %v", ErrArtifactInvalid, err)
	}
	if len(seen) != len(byPath) {
		for path := range byPath {
			if !seen[path] {
				return data.OpResult{}, fmt.Errorf("%w: archive is missing %q", ErrArtifactInvalid, path)
			}
		}
		return data.OpResult{}, fmt.Errorf("%w: archive entry count %d, manifest %d", ErrArtifactInvalid, len(seen), len(byPath))
	}
	for _, link := range hardlinks {
		if !seen[link.target] {
			return data.OpResult{}, fmt.Errorf("%w: hardlink %q target %q was not extracted", ErrArtifactInvalid, link.path, link.target)
		}
		if err := ensureBindParent(root, link.path); err != nil {
			return data.OpResult{}, err
		}
		if err := root.Link(link.target, link.path); err != nil {
			return data.OpResult{}, fmt.Errorf("create hardlink %q: %w", link.path, err)
		}
	}

	// Apply directory modes and mtimes deepest first. The target root's own
	// metadata is applied after verification, so a restrictive archived root
	// mode cannot prevent the verification walk.
	sort.Slice(dirs, func(i, j int) bool { return pathDepth(dirs[i].path) > pathDepth(dirs[j].path) })
	for _, dir := range dirs {
		if err := root.Chmod(dir.path, dir.mode); err != nil {
			return data.OpResult{}, fmt.Errorf("set mode of %q: %w", dir.path, err)
		}
		if !dir.mtime.IsZero() {
			if err := root.Chtimes(dir.path, dir.mtime, dir.mtime); err != nil {
				return data.OpResult{}, fmt.Errorf("set mtime of %q: %w", dir.path, err)
			}
		}
	}
	return data.OpResult{}, d.checkIdentity()
}

func extractBindFile(ctx context.Context, root *os.Root, tr *tar.Reader, hdr *tar.Header, rel string, entry backup.Entry, mode os.FileMode) error {
	if hdr.Size != entry.SizeBytes {
		return fmt.Errorf("%w: %q size %d, manifest %d", ErrArtifactInvalid, entry.Path, hdr.Size, entry.SizeBytes)
	}
	if err := ensureBindParent(root, rel); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create file %q: %w", entry.Path, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(h, f), io.LimitReader(ctxReader{ctx: ctx, r: tr}, entry.SizeBytes))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("write %q: %w", entry.Path, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %q: %w", entry.Path, closeErr)
	}
	if n != entry.SizeBytes {
		return fmt.Errorf("%w: %q is truncated", ErrArtifactInvalid, entry.Path)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), entry.SHA256) {
		return fmt.Errorf("%w: %q content does not match the manifest", ErrArtifactInvalid, entry.Path)
	}
	if err := root.Chmod(rel, mode); err != nil {
		return fmt.Errorf("set mode of %q: %w", entry.Path, err)
	}
	if !hdr.ModTime.IsZero() {
		if err := root.Chtimes(rel, hdr.ModTime, hdr.ModTime); err != nil {
			return fmt.Errorf("set mtime of %q: %w", entry.Path, err)
		}
	}
	return nil
}

// Verify compares the restored folder with the validated manifest: content
// hashes, entry types, ordinary permission bits, symlink targets, hardlink
// inode identity and an exact entry count. uid/gid and xattrs/ACLs are host
// limitations reported to the user; they are not required to match and are not
// claimed as preserved.
func (d *BindDestination) Verify(ctx context.Context, m backup.Manifest) (data.OpResult, error) {
	if err := d.checkIdentity(); err != nil {
		return data.OpResult{}, err
	}
	if err := validateBindManifest(m); err != nil {
		return data.OpResult{}, err
	}
	byPath := manifestEntries(m)
	expected := 0
	var rootEntry *backup.Entry
	for path, entry := range byPath {
		if path == "" {
			copy := entry
			rootEntry = &copy
			continue
		}
		expected++
	}

	target := d.currentPath()
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return data.OpResult{}, fmt.Errorf("%w: target is no longer a directory", ErrTargetInvalid)
	}
	oldMode := info.Mode().Perm()
	restoreOld := false
	if oldMode&0o700 != 0o700 {
		if err := os.Chmod(target, oldMode|0o700); err != nil {
			return data.OpResult{}, fmt.Errorf("prepare target for verification: %w", err)
		}
		restoreOld = true
	}
	seen := map[string]bool{}
	actual := 0
	walkErr := filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == target {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		want, ok := byPath[rel]
		if !ok {
			return fmt.Errorf("%w: unexpected entry %q remains in the folder (replace is not a merge)", ErrArtifactInvalid, rel)
		}
		seen[rel] = true
		actual++
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		wantMode, err := bindMode(want.Mode)
		if err != nil {
			return fmt.Errorf("%w: mode of %q: %v", ErrArtifactInvalid, want.Path, err)
		}
		switch want.Type {
		case backup.EntryFile:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: %q is not a regular file", ErrVerifyFailed, rel)
			}
			if info.Mode().Perm() != wantMode.Perm() {
				return fmt.Errorf("%w: %q mode %s, manifest %s", ErrVerifyFailed, rel, info.Mode().Perm(), wantMode.Perm())
			}
			sum, size, err := fileSHA256(path)
			if err != nil {
				return err
			}
			if size != want.SizeBytes || !strings.EqualFold(sum, want.SHA256) {
				return fmt.Errorf("%w: %q content differs from the manifest", ErrVerifyFailed, rel)
			}
		case backup.EntryDir:
			if !info.IsDir() {
				return fmt.Errorf("%w: %q is not a directory", ErrVerifyFailed, rel)
			}
			if info.Mode().Perm() != wantMode.Perm() {
				return fmt.Errorf("%w: %q mode %s, manifest %s", ErrVerifyFailed, rel, info.Mode().Perm(), wantMode.Perm())
			}
		case backup.EntrySymlink:
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("%w: %q is not a symlink", ErrVerifyFailed, rel)
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if target != want.LinkTarget {
				return fmt.Errorf("%w: %q symlink target %q, manifest %q", ErrVerifyFailed, rel, target, want.LinkTarget)
			}
		case backup.EntryHardlink:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: %q is not a regular file", ErrVerifyFailed, rel)
			}
			targetRel := normalizeEntryPath(want.LinkTarget)
			targetPath := filepath.Join(target, filepath.FromSlash(targetRel))
			other, err := os.Lstat(targetPath)
			if err != nil || !other.Mode().IsRegular() {
				return fmt.Errorf("%w: hardlink %q target %q is not a restored file", ErrVerifyFailed, rel, want.LinkTarget)
			}
			if !os.SameFile(info, other) {
				return fmt.Errorf("%w: hardlink %q does not share an inode with %q", ErrVerifyFailed, rel, want.LinkTarget)
			}
			if info.Mode().Perm() != wantMode.Perm() {
				return fmt.Errorf("%w: %q mode %s, manifest %s", ErrVerifyFailed, rel, info.Mode().Perm(), wantMode.Perm())
			}
		default:
			return fmt.Errorf("%w: unsupported entry %q", ErrVerifyFailed, rel)
		}
		return nil
	})
	if walkErr != nil {
		if restoreOld {
			_ = os.Chmod(target, oldMode)
		}
		return data.OpResult{}, walkErr
	}
	if actual != expected {
		if restoreOld {
			_ = os.Chmod(target, oldMode)
		}
		return data.OpResult{}, fmt.Errorf("%w: restored entry count %d, manifest %d", ErrVerifyFailed, actual, expected)
	}
	for path := range byPath {
		if path != "" && !seen[path] {
			if restoreOld {
				_ = os.Chmod(target, oldMode)
			}
			return data.OpResult{}, fmt.Errorf("%w: manifest entry %q is missing", ErrVerifyFailed, path)
		}
	}
	if rootEntry != nil {
		mode, err := bindMode(rootEntry.Mode)
		if err != nil {
			if restoreOld {
				_ = os.Chmod(target, oldMode)
			}
			return data.OpResult{}, fmt.Errorf("%w: root mode %q: %v", ErrArtifactInvalid, rootEntry.Mode, err)
		}
		if err := os.Chmod(target, mode); err != nil {
			if restoreOld {
				_ = os.Chmod(target, oldMode)
			}
			return data.OpResult{}, fmt.Errorf("set folder mode: %w", err)
		}
	} else if restoreOld {
		_ = os.Chmod(target, oldMode)
	}
	if d.rootMetadata && !d.rootModTime.IsZero() {
		if err := os.Chtimes(target, d.rootModTime, d.rootModTime); err != nil {
			return data.OpResult{}, fmt.Errorf("set folder mtime: %w", err)
		}
	}
	return data.OpResult{}, nil
}

func manifestEntries(m backup.Manifest) map[string]backup.Entry {
	out := make(map[string]backup.Entry, len(m.Entries))
	for _, entry := range m.Entries {
		out[normalizeEntryPath(entry.Path)] = entry
	}
	return out
}

func ensureBindParent(root *os.Root, rel string) error {
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if parent == "." || parent == "" {
		return nil
	}
	if err := root.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create parent of %q: %w", rel, err)
	}
	return nil
}

func bindMode(raw string) (os.FileMode, error) {
	var bits uint32
	if _, err := fmt.Sscanf(raw, "%o", &bits); err != nil {
		return 0, err
	}
	mode := os.FileMode(bits & 0o777)
	if bits&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if bits&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if bits&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}

func pathDepth(path string) int {
	return strings.Count(filepath.ToSlash(path), "/")
}

// fileSHA256 returns the size and lowercase hex digest of one regular file.
func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type dirMetadata struct {
	path  string
	mode  os.FileMode
	mtime time.Time
}

type linkMetadata struct {
	path   string
	target string
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
