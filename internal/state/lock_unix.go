//go:build unix

package state

import (
	"errors"
	"os"
	"syscall"
)

// errLockBusy is the portable sentinel for "someone else holds the lock".
var errLockBusy = errors.New("state: lock is busy")

func flockExclusive(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errLockBusy
	}
	return err
}

func funlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
