//go:build !unix

package fsidentity

import "os"

// statKey is unavailable on this platform; Key falls back to the canonical
// path. Same still uses os.SameFile, which Windows implements with the file
// index.
func statKey(os.FileInfo) (string, bool) {
	return "", false
}
