//go:build !unix

package projectconfig

import "errors"

// LockFileName is the project-local lock every writer would share on a
// platform with flock support.
const LockFileName = ".dcraft.lock"

// acquireLock cannot coordinate across processes on platforms without Unix
// flock. Returning an explicit unsupported error is safer than a silent
// in-process mutex that would still lose a concurrent writer's update.
func acquireLock(string) (func(), error) {
	return nil, &Error{
		Kind:   KindUnsupported,
		Detail: errors.New("cross-process project config locking is unsupported on this platform").Error(),
	}
}
