package state

import (
	"fmt"
	"io"
	"os"
)

// The state root is private per-user data. Sharing it (or letting another
// user pre-create it) would let that user read history and name claims or
// redirect state files. Every write path creates the root with 0700 and then
// verifies ownership and mode; every state file is opened without following a
// symlink.
const stateRootMode = 0o700

// ensureRoot creates the state root when needed and verifies it. The root must
// be a real directory owned by the current user with mode 0700.
func ensureRoot(root string) error {
	if err := os.MkdirAll(root, stateRootMode); err != nil {
		return fmt.Errorf("create state root: %w", err)
	}
	return verifyRoot(root)
}

// verifyRootIfExists checks an existing root without creating it. Read-only
// callers use it so status-like commands never create state.
func verifyRootIfExists(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect state root: %w", err)
	}
	return verifyRootInfo(root, info)
}

func verifyRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect state root: %w", err)
	}
	return verifyRootInfo(root, info)
}

func verifyRootInfo(root string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state root %s is a symlink; set %s to a real private directory", root, EnvStateDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("state root %s is not a directory", root)
	}
	if err := verifyRootOwner(root, info); err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != stateRootMode {
		return fmt.Errorf("state root %s has mode %04o; require 0700 (run: chmod 700 %s)", root, perm, root)
	}
	return nil
}

// openStateFile opens one state file without following a symlink. A pre-check
// gives clear guidance; the no-follow flag closes the race on Unix.
func openStateFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if err := refuseSymlink(path); err != nil {
		return nil, err
	}
	f, err := openNoFollow(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// readStateFile reads one state file without following a symlink.
func readStateFile(path string) ([]byte, error) {
	f, err := openStateFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("state file %s is not a regular file", path)
	}
	return io.ReadAll(f)
}

func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlinked state file %s; remove the link and let dcraft create a regular 0600 file", path)
	}
	return nil
}
