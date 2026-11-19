//go:build !unix

package state

import (
	"os"
)

// openNoFollow is the portable fallback. The symlink pre-check in
// openStateFile still refuses a linked target on platforms without
// O_NOFOLLOW; callers keep the same behavior.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

// verifyRootOwner is a no-op where a portable file-owner check is unavailable.
// Mode and regular-directory checks still apply.
func verifyRootOwner(string, os.FileInfo) error {
	return nil
}
