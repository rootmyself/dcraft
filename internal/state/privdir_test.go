package state_test

import (
	"os"
	"testing"
)

// privateStateDir returns a state root with the private mode dcraft requires.
// Go's t.TempDir is not guaranteed to be 0700 on every platform.
func privateStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod private state dir: %v", err)
	}
	return dir
}
