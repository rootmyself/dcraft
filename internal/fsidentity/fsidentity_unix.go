//go:build unix

package fsidentity

import (
	"fmt"
	"os"
	"syscall"
)

// statKey is the filesystem identity on Unix: device and inode. Both case
// variants and NFC/NFD spellings of one folder stat to the same pair.
func statKey(info os.FileInfo) (string, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), uint64(st.Ino)), true
}
