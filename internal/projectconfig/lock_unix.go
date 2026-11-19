//go:build unix

package projectconfig

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LockFileName is a stable, project-local lock inode shared by every feature
// that writes .dcraft.yaml. It is never removed: unlinking a flock file lets a
// new writer lock a different inode while an old writer still holds the
// previous one, which would defeat serialization.
const LockFileName = ".dcraft.lock"

const lockWait = 5 * time.Second

// acquireLock serializes read-modify-write of .dcraft.yaml across processes
// with a bounded wait. The returned function releases the lock exactly once.
func acquireLock(projectDir string) (func(), error) {
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, &Error{Kind: KindLockFailed, Detail: err.Error()}
	}
	path := filepath.Join(projectDir, LockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, &Error{Kind: KindLockFailed, Detail: err.Error()}
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, &Error{Kind: KindLockFailed, Detail: err.Error()}
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, &Error{Kind: KindLockBusy, Detail: path}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
