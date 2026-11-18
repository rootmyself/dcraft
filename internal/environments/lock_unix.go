//go:build unix

package environments

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// configLockFile is the same stable, project-local lock inode used by the
// hooks registration writer. It is never removed: unlinking a flock file lets
// a new writer lock a different inode while an old writer still holds the
// previous one.
const configLockFile = ".dcraft.lock"

const configLockWait = 5 * time.Second

// acquireConfigLock serializes read-modify-write of .dcraft.yaml across
// processes and goroutines. The in-process mutex is held for the whole
// lock/write/release cycle so two concurrent environment additions in one
// process cannot interleave.
func acquireConfigLock(projectDir string) (func(), error) {
	processConfigMu.Lock()
	release, err := lockConfigFile(projectDir)
	if err != nil {
		processConfigMu.Unlock()
		return nil, err
	}
	return func() {
		release()
		processConfigMu.Unlock()
	}, nil
}

func lockConfigFile(projectDir string) (func(), error) {
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

func configLockError(err error) *core.Error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryEnvironmentInvalid,
		Severity:    core.SeverityError,
		Message:     msgWriteLockFailed,
		Detail:      err.Error(),
		NextActions: []string{msgWriteLockActionOne, msgWriteLockActionTwo},
	})
}

func configLockBusy(path string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentInvalid,
		Severity:    core.SeverityError,
		Message:     msgWriteLocked,
		Detail:      path,
		NextActions: []string{msgWriteLockActionOne, msgWriteLockActionTwo},
	})
}
