//go:build unix

package backup

import (
	"os"
	"syscall"
)

type hostFileKey struct {
	dev uint64
	ino uint64
}

// hostFileIdentity returns the inode identity used to detect hardlinks. On
// platforms without dev/ino this reports false and duplicate files are
// archived as separate regular files.
func hostFileIdentity(info os.FileInfo) (hostFileKey, uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return hostFileKey{}, 0, false
	}
	return hostFileKey{dev: uint64(stat.Dev), ino: uint64(stat.Ino)}, uint64(stat.Nlink), true
}

// openHostSource refuses to follow a symlink that was swapped in after the
// lstat-based walk. This keeps a backup from reading an unrelated file.
func openHostSource(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
