//go:build unix

package hooks

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// configLockFile is a stable, project-local lock inode. It is never removed:
// unlinking a flock file lets a new writer lock a different inode while an old
// writer still holds the previous one, which would defeat serialization.
const configLockFile = ".dcraft.lock"

const configLockWait = 5 * time.Second

// acquireConfigLock serializes read-modify-write of .dcraft.yaml across
// processes with a bounded wait. The returned function releases the lock and
// is safe to call once.
func acquireConfigLock(projectDir string) (func(), error) {
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, configLockError(err)
	}
	path := filepath.Join(projectDir, configLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, configLockError(err)
	}
	deadline := time.Now().Add(configLockWait)
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
			return nil, configLockError(err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, configLockBusy(path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
