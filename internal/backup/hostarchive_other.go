//go:build !unix

package backup

import "os"

type hostFileKey struct {
	dev uint64
	ino uint64
}

func hostFileIdentity(info os.FileInfo) (hostFileKey, uint64, bool) {
	return hostFileKey{}, 0, false
}

func openHostSource(path string) (*os.File, error) {
	return os.Open(path)
}
