package initconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrExists means another file appeared at the destination; dcraft does not
	// replace it without the file_overwrite approval.
	ErrExists = errors.New("destination already exists")
	// ErrSymlink means the destination is a symbolic link. dcraft never follows
	// it, so a link cannot redirect the write outside the confirmed path.
	ErrSymlink = errors.New("destination is a symbolic link")
	// ErrNotRegular means the destination is a directory or special file.
	ErrNotRegular = errors.New("destination is not a regular file")
)

// Saver publishes a completed file. Tests replace it with a fake to simulate
// failures; the production FileSaver uses a temp file in the destination
// directory, fsync and an atomic publish.
type Saver interface {
	Save(ctx context.Context, path string, content []byte, overwrite bool) error
}

// FileSaver is the atomic filesystem boundary. A new file is published with a
// no-replace hard link, so a concurrent creator is never overwritten; an
// approved overwrite uses rename in the same directory. The target file is
// either the previous inode or the complete new content, never a partial write.
type FileSaver struct {
	// publish is overridden only by tests to exercise cleanup after a failed
	// publish.
	publish func(tmp, dest string, overwrite bool) error
}

func (s FileSaver) Save(ctx context.Context, path string, content []byte, overwrite bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", ErrNotRegular, path)
		}
		if !overwrite {
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
		// Preserve a stricter mode (for example 0600 on a file with
		// environment values); the umask can only narrow it further.
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}

	dir := filepath.Dir(path)
	tmp, tmpName, err := createTempFile(dir, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	publish := s.publish
	if publish == nil {
		publish = defaultPublish
	}
	if err := publish(tmpName, path, overwrite); err != nil {
		return err
	}
	// A rename consumed the temp; a link left it. Removing unconditionally is
	// harmless for the rename case and cleans the link case.
	_ = os.Remove(tmpName)
	cleanup = false
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

// createTempFile uses O_EXCL and mode 0644 so the process umask decides the
// final permissions. os.CreateTemp always starts at 0600 and a later chmod
// would silently bypass a restrictive umask.
func createTempFile(dir string, mode os.FileMode) (*os.File, string, error) {
	for attempt := 0; attempt < 20; attempt++ {
		name := filepath.Join(dir, fmt.Sprintf(".dcraft-init-%d-%d.tmp", os.Getpid(), time.Now().UnixNano()+int64(attempt)))
		file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not create a temporary init file")
}

func defaultPublish(tmp, dest string, overwrite bool) error {
	if overwrite {
		return os.Rename(tmp, dest)
	}
	if err := os.Link(tmp, dest); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, dest)
		}
		return err
	}
	return nil
}
