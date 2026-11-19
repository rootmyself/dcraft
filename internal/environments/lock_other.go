//go:build !unix

package environments

import (
	"sync"

	"github.com/rootmyself/dcraft/internal/core"
)

// Cross-process flock is unavailable on this platform. The in-process mutex
// still serializes concurrent writers in one process; the feature keeps
// building and working for a single process.
var processConfigMu sync.Mutex

func acquireConfigLock(projectDir string) (func(), error) {
	processConfigMu.Lock()
	return func() { processConfigMu.Unlock() }, nil
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
