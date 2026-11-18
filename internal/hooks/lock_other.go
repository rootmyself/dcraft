//go:build !unix

package hooks

import (
	"errors"
)

// acquireConfigLock cannot coordinate across processes on platforms without
// Unix flock. The project registration surface keeps building and working for
// a single process; concurrent writers are not serialized here.
func acquireConfigLock(projectDir string) (func(), error) {
	return nil, configLockError(errors.New("cross-process config locking is unsupported on this platform"))
}
