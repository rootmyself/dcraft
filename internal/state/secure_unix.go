//go:build unix

package state

import (
	"fmt"
	"os"
	"syscall"
)

func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

func verifyRootOwner(root string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot verify the owner of state root %s", root)
	}
	return rootOwnerError(root, int(st.Uid), os.Geteuid())
}

func rootOwnerError(root string, uid, euid int) error {
	if uid == euid {
		return nil
	}
	return fmt.Errorf("state root %s is owned by uid %d; require the current user (uid %d) and mode 0700", root, uid, euid)
}
