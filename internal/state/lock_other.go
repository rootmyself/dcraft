//go:build !unix

package state

import (
	"errors"
	"os"
)

var errLockBusy = errors.New("state: lock is busy")

// dcraft targets macOS, Linux and WSL2. Other platforms keep the single-binary
// build working but cannot coordinate state across processes.
func flockExclusive(*os.File) error {
	return errors.New("state: cross-process locking is unsupported on this platform")
}

func funlock(*os.File) error { return nil }
