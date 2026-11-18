package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// LockHolder describes the operation holding a stack lock. It is written into
// the lock file so a conflicting run can name the current owner.
type LockHolder struct {
	Action    string    `json:"action"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// BusyError means another process or goroutine is already changing the same
// stack. Different stacks never produce this error.
type BusyError struct {
	Target    core.Target
	Holder    LockHolder
	HasHolder bool
}

func (e *BusyError) Error() string {
	msg := "stack is busy: " + e.Target.ProjectDir
	if e.Target.Environment != "" {
		msg += " (environment " + e.Target.Environment + ")"
	}
	if e.HasHolder {
		msg += fmt.Sprintf("; held by %s pid %d since %s", e.Holder.Action, e.Holder.PID, e.Holder.StartedAt.Format(time.RFC3339))
	}
	return msg
}

// Locker is the published narrow per-stack lock capability. *Store implements
// it; features and deps consume this interface instead of type-asserting a
// concrete store, so a differently wired StateStore cannot silently lose
// coordination.
type Locker interface {
	AcquireStackLock(ctx context.Context, target core.Target, holder LockHolder) (*StackLock, error)
}

// StackLock is one held per-stack coordination lock. Release must be called
// exactly once; a released lock is inert.
type StackLock struct {
	file       *os.File
	processKey string

	mu       sync.Mutex
	released bool
}

// AcquireStackLock takes the exclusive change lock for one stack without
// waiting. Contenders fail with *BusyError immediately; callers decide how to
// report it. Different folders/environments use different locks. The lock is
// released automatically by the operating system if the process dies.
func (s *Store) AcquireStackLock(ctx context.Context, target core.Target, holder LockHolder) (*StackLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := s.StackDir(target)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create stack state dir: %w", err)
	}
	path := filepath.Join(dir, ".lock")

	if !acquireProcessLock(path) {
		return nil, busyError(target, path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		releaseProcessLock(path)
		return nil, fmt.Errorf("open stack lock: %w", err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		releaseProcessLock(path)
		if errors.Is(err, errLockBusy) {
			return nil, busyError(target, path)
		}
		return nil, fmt.Errorf("lock stack: %w", err)
	}
	writeHolder(f, holder)
	return &StackLock{file: f, processKey: path}, nil
}

// Release unlocks the stack. It is safe on a nil or already released lock.
func (l *StackLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	var releaseErr error
	if l.file != nil {
		if err := funlock(l.file); err != nil {
			releaseErr = err
		}
		if err := l.file.Close(); err != nil && releaseErr == nil {
			releaseErr = err
		}
	}
	releaseProcessLock(l.processKey)
	return releaseErr
}

func busyError(target core.Target, path string) *BusyError {
	holder, ok := readHolder(path)
	return &BusyError{Target: target, Holder: holder, HasHolder: ok}
}

func writeHolder(f *os.File, holder LockHolder) {
	data, err := json.Marshal(holder)
	if err != nil {
		return
	}
	if err := f.Truncate(0); err != nil {
		return
	}
	if _, err := f.Seek(0, 0); err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Sync()
}

func readHolder(path string) (LockHolder, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return LockHolder{}, false
	}
	var holder LockHolder
	if err := json.Unmarshal(data, &holder); err != nil {
		return LockHolder{}, false
	}
	return holder, true
}

// processLocks coordinates locks inside one process in addition to the
// operating-system lock. This makes contention deterministic in tests and
// avoids relying on platform-specific same-process flock behavior.
var processLocks = struct {
	sync.Mutex
	held map[string]bool
}{held: map[string]bool{}}

func acquireProcessLock(path string) bool {
	key := lockKey(path)
	processLocks.Lock()
	defer processLocks.Unlock()
	if processLocks.held[key] {
		return false
	}
	processLocks.held[key] = true
	return true
}

func releaseProcessLock(path string) {
	key := lockKey(path)
	processLocks.Lock()
	defer processLocks.Unlock()
	delete(processLocks.held, key)
}

func lockKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// acquireFileLock takes one short-lived exclusive lock for internal state
// updates. It waits a bounded time because callers are short critical sections
// that must all succeed (for example concurrent automatic name allocation).
// The stack change lock is separate and never waits.
func acquireFileLock(ctx context.Context, path string) (release func(), err error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, ok, err := tryFileLock(path)
		if err != nil {
			return nil, err
		}
		if ok {
			return release, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("state lock stayed busy: %s", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func tryFileLock(path string) (release func(), ok bool, err error) {
	if !acquireProcessLock(path) {
		return nil, false, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		releaseProcessLock(path)
		return nil, false, fmt.Errorf("open state lock: %w", err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		releaseProcessLock(path)
		if errors.Is(err, errLockBusy) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock state: %w", err)
	}
	release = func() {
		_ = funlock(f)
		_ = f.Close()
		releaseProcessLock(path)
	}
	return release, true, nil
}
