package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

func holder(action string) state.LockHolder {
	return state.LockHolder{
		Action: action, PID: os.Getpid(),
		StartedAt: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestStackLockSerializesTheSameTargetAndReleases(t *testing.T) {
	s := state.New(privateStateDir(t))
	tgt := target("/work/app")

	lock, err := s.AcquireStackLock(context.Background(), tgt, holder("up"))
	if err != nil {
		t.Fatalf("AcquireStackLock: %v", err)
	}
	_, err = s.AcquireStackLock(context.Background(), tgt, holder("down"))
	var busy *state.BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second acquire err = %v, want BusyError", err)
	}
	if busy.Holder.Action != "up" || !busy.HasHolder {
		t.Fatalf("busy holder = %+v", busy.Holder)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	again, err := s.AcquireStackLock(context.Background(), tgt, holder("down"))
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = again.Release()
}

func TestStackLockDoesNotBlockDifferentTargets(t *testing.T) {
	s := state.New(privateStateDir(t))
	first, err := s.AcquireStackLock(context.Background(), target("/work/a"), holder("up"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	defer first.Release()
	second, err := s.AcquireStackLock(context.Background(), target("/work/b"), holder("up"))
	if err != nil {
		t.Fatalf("second target blocked: %v", err)
	}
	_ = second.Release()
}

func TestStackLockSeparatesEnvironments(t *testing.T) {
	s := state.New(privateStateDir(t))
	dev := target("/work/app")
	staging := target("/work/app")
	staging.Environment = "staging"
	first, err := s.AcquireStackLock(context.Background(), dev, holder("up"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := s.AcquireStackLock(context.Background(), staging, holder("up"))
	if err != nil {
		t.Fatalf("environment lock not separate: %v", err)
	}
	_ = second.Release()
}

func TestStackLockHolderIsVisibleAndPrivate(t *testing.T) {
	root := privateStateDir(t)
	s := state.New(root)
	tgt := target("/work/app")
	lock, err := s.AcquireStackLock(context.Background(), tgt, holder("up"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	dir, err := s.StackDir(tgt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".lock")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if !strings.Contains(string(data), "up") || !strings.Contains(string(data), "pid") {
		t.Fatalf("lock holder not recorded: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock permissions = %v, want private", info.Mode().Perm())
	}
}

func TestStackLockIsExclusiveAcrossGoroutines(t *testing.T) {
	s := state.New(privateStateDir(t))
	tgt := target("/work/app")
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, busy := 0, 0
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lock, err := s.AcquireStackLock(context.Background(), tgt, holder("up"))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				var b *state.BusyError
				if errors.As(err, &b) {
					busy++
					return
				}
				t.Errorf("unexpected error: %v", err)
				return
			}
			wins++
			_ = lock.Release()
		}()
	}
	close(start)
	wg.Wait()
	if wins == 0 {
		t.Fatal("no goroutine acquired the lock")
	}
	if wins+busy != n {
		t.Fatalf("wins=%d busy=%d total=%d", wins, busy, n)
	}
}

func TestStackLockCancellationDoesNotAcquire(t *testing.T) {
	s := state.New(privateStateDir(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lock, err := s.AcquireStackLock(ctx, target("/work/app"), holder("up"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if lock != nil {
		t.Fatal("canceled acquire returned a lock")
	}
}

func TestBusyErrorNamesTheConflictingAction(t *testing.T) {
	tgt := core.Target{ProjectDir: "/work/app", Environment: "dev"}
	err := &state.BusyError{
		Target: tgt, HasHolder: true, Holder: holder("restart"),
	}
	msg := err.Error()
	if !strings.Contains(msg, "restart") || !strings.Contains(msg, "/work/app") {
		t.Fatalf("busy message = %q", msg)
	}
}
