package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ArchiveHostDir writes a tar+gzip archive of a host bind folder into
// destFile. It is the bind-folder counterpart of the volume helper: it runs as
// the invoking host user, never writes into the source, never follows
// symlinks in the source tree, and records modes, uid/gid, symlinks and
// hardlinks. Extended attributes and ACLs are not part of the v1 format; the
// manifest and notices say so explicitly.
//
// destFile must not exist. The caller uses a fresh .partial directory, so a
// pre-existing destination symlink can never be followed.
func ArchiveHostDir(ctx context.Context, sourceDir, destFile string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rootInfo, err := os.Lstat(sourceDir)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrBindInvalid, sourceDir, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", ErrBindInvalid, sourceDir)
	}

	out, err := os.OpenFile(destFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	writeErr := writeHostTree(ctx, tw, sourceDir)
	if closeErr := tw.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if closeErr := gz.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if syncErr := out.Sync(); writeErr == nil {
		writeErr = syncErr
	}
	if closeErr := out.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func writeHostTree(ctx context.Context, tw *tar.Writer, sourceDir string) error {
	hardlinks := map[hostFileKey]string{}
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("%w: %s: %v", ErrBindInvalid, path, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrBindInvalid, path, err)
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrBindInvalid, path, err)
		}
		name := "./"
		if rel != "." {
			if err := validateHostName(rel); err != nil {
				return err
			}
			name = "./" + filepath.ToSlash(rel)
		}
		hdr, err := hostHeader(info, path, name, hardlinks)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("%w: write %q: %v", ErrBindInvalid, path, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil
		}
		return writeHostFile(ctx, tw, path, info, hdr)
	})
}

// hostHeader builds the tar header for one host entry. It never follows a
// symlink: a symlink is stored as a symlink and its target is only metadata.
func hostHeader(info os.FileInfo, path, name string, hardlinks map[hostFileKey]string) (*tar.Header, error) {
	mode := info.Mode()
	var hdr *tar.Header
	var err error
	switch {
	case mode.IsDir(), mode&os.ModeNamedPipe != 0, (mode&os.ModeDevice != 0):
		hdr, err = tar.FileInfoHeader(info, "")
	case mode&os.ModeSymlink != 0:
		target, readErr := os.Readlink(path)
		if readErr != nil {
			return nil, fmt.Errorf("%w: read link %q: %v", ErrBindInvalid, path, readErr)
		}
		hdr, err = tar.FileInfoHeader(info, target)
	case mode.IsRegular():
		hdr, err = tar.FileInfoHeader(info, "")
		if err == nil {
			if key, links, ok := hostFileIdentity(info); ok && links > 1 {
				if first, seen := hardlinks[key]; seen {
					hdr.Typeflag = tar.TypeLink
					hdr.Linkname = first
					hdr.Size = 0
				} else {
					hardlinks[key] = name
				}
			}
		}
	case mode&os.ModeSocket != 0:
		return nil, fmt.Errorf("%w: %s is a socket and cannot be archived by backup format v1", ErrBindInvalid, path)
	default:
		return nil, fmt.Errorf("%w: %s has an unsupported file type %s", ErrBindInvalid, path, mode.Type())
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBindInvalid, path, err)
	}
	hdr.Name = name
	hdr.Mode = int64(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		hdr.Mode |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		hdr.Mode |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		hdr.Mode |= 0o1000
	}
	return hdr, nil
}

func writeHostFile(ctx context.Context, tw *tar.Writer, path string, info os.FileInfo, hdr *tar.Header) error {
	f, err := openHostSource(path)
	if err != nil {
		return fmt.Errorf("%w: open %q: %v", ErrBindInvalid, path, err)
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat %q: %v", ErrBindInvalid, path, err)
	}
	if !current.Mode().IsRegular() || current.Size() != hdr.Size {
		return fmt.Errorf("%w: %q changed while it was being archived", ErrBindInvalid, path)
	}
	n, err := io.Copy(tw, io.LimitReader(ctxReader{ctx: ctx, r: f}, hdr.Size))
	if err != nil {
		return fmt.Errorf("%w: read %q: %v", ErrBindInvalid, path, err)
	}
	if n != hdr.Size {
		return fmt.Errorf("%w: %q was truncated while it was being archived", ErrBindInvalid, path)
	}
	if after, statErr := f.Stat(); statErr == nil && after.Size() != hdr.Size {
		return fmt.Errorf("%w: %q changed while it was being archived", ErrBindInvalid, path)
	}
	return nil
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

// validateHostName keeps host names inside the published v1 name contract.
// A backslash is legal on Unix but ambiguous in a Windows extraction, so v1
// refuses it explicitly instead of writing an archive Verify would reject.
func validateHostName(rel string) error {
	if strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return fmt.Errorf("%w: file name %q contains a character that backup format v1 cannot store", ErrBindInvalid, rel)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("%w: file name %q is absolute", ErrBindInvalid, rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return fmt.Errorf("%w: file name %q escapes the folder", ErrBindInvalid, rel)
		}
	}
	return nil
}
