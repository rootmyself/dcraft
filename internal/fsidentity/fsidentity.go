// Package fsidentity derives a stable identity for a filesystem folder.
//
// Two spellings of the same folder must produce one identity even when the
// path strings differ: symlinks, case differences on case-insensitive
// filesystems (APFS/HFS+) and Unicode normalization differences (NFC/NFD
// Korean names) are all spelling differences, not stack differences.
//
// When the folder exists, the identity is the filesystem identity (device and
// inode on Unix, compared with os.SameFile elsewhere). The path-based
// canonical form is the documented fallback for a folder that does not exist
// yet; callers must only use it where a missing folder is meaningful.
package fsidentity

import (
	"os"
	"path/filepath"
)

// Key returns a stable identity string for dir.
func Key(dir string) string {
	if key, ok := existingKey(dir); ok {
		return key
	}
	return "path:" + canonicalPath(dir)
}

// Same reports whether a and b identify the same existing folder. When either
// path cannot be inspected it compares the canonical path spelling, which
// keeps non-existent test fixtures deterministic.
func Same(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil {
		return os.SameFile(ai, bi)
	}
	return canonicalPath(a) == canonicalPath(b)
}

func existingKey(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	key, ok := statKey(info)
	if !ok {
		return "", false
	}
	return "fs:" + key, true
}

// canonicalPath resolves symlinks when the path exists and returns an absolute
// cleaned path otherwise. It is the fallback identity only; it is never mixed
// with a filesystem identity.
func canonicalPath(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
}
