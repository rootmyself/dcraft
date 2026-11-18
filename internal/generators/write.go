package generators

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScriptMode is the permission every generated host script receives: the user
// can review and run it directly, and dcraft's hook check requires the
// executable bit.
const ScriptMode os.FileMode = 0o755

// WriteScript publishes content at path atomically, creating missing parent
// folders. A new file is published with a no-replace hard link so a concurrent
// generator cannot silently replace something that appeared after the preview.
// An existing regular file is replaced through a same-folder rename. Symlinks,
// directories and non-regular files are refused; the preview may have been
// approved for a regular path, not for whatever a symlink points at.
func WriteScript(path string, content []byte) (created bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create script folder: %w", err)
	}
	info, statErr := os.Lstat(path)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return false, statErr
	}
	if exists {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("refuse to replace symlink %s", path)
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("refuse to replace non-regular file %s", path)
		}
	}

	temp, err := os.CreateTemp(dir, ".dcraft-script-*")
	if err != nil {
		return false, fmt.Errorf("create temporary script: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := temp.Write(content); err != nil {
		cleanup()
		return false, fmt.Errorf("write temporary script: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return false, fmt.Errorf("sync temporary script: %w", err)
	}
	if err := temp.Chmod(ScriptMode); err != nil {
		cleanup()
		return false, fmt.Errorf("set script mode: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return false, fmt.Errorf("close temporary script: %w", err)
	}

	if exists {
		if err := os.Rename(tempPath, path); err != nil {
			_ = os.Remove(tempPath)
			return false, fmt.Errorf("publish script: %w", err)
		}
		return false, nil
	}
	if err := os.Link(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		if os.IsExist(err) {
			return false, fmt.Errorf("script appeared after preview: %s", path)
		}
		return false, fmt.Errorf("publish script: %w", err)
	}
	_ = os.Remove(tempPath)
	return true, nil
}
