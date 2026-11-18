// Package state owns dcraft's per-computer state directory. State is kept per
// stack and per environment and never contains resolved secret values.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/rootmyself/dcraft/internal/core"
)

// EnvStateDir overrides the default state location.
const EnvStateDir = "DCRAFT_STATE_DIR"

// Root resolves the state root: DCRAFT_STATE_DIR when set, otherwise
// <OS user config dir>/dcraft. The directory is not created here.
func Root(getenv func(string) string) (string, error) {
	if getenv != nil {
		if v := strings.TrimSpace(getenv(EnvStateDir)); v != "" {
			abs, err := filepath.Abs(v)
			if err != nil {
				return "", fmt.Errorf("resolve %s: %w", EnvStateDir, err)
			}
			return abs, nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, "dcraft"), nil
}

// Store is the filesystem implementation of core.StateStore.
type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) *Store { return &Store{root: root} }

func (s *Store) Root() string { return s.root }

// StackDir implements core.StateStore. Layout:
//
//	<root>/stacks/<folder>-<hash>/{history.jsonl,state.json,...}
func (s *Store) StackDir(target core.Target) (string, error) {
	if strings.TrimSpace(target.ProjectDir) == "" {
		return "", errors.New("state: target has no project folder")
	}
	return filepath.Join(s.root, "stacks", key(target.ProjectDir, target.Environment)), nil
}

// AppendOperation appends one JSON line to <stack>/history.jsonl.
func (s *Store) AppendOperation(target core.Target, rec core.OperationRecord) error {
	dir, err := s.StackDir(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode history record: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

var unsafeKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

// key produces a stable, readable per-target directory name. The hash input is
// the absolute project folder and the environment id, so equal folder names in
// different places (or different environments) never share state.
func key(projectDir, environment string) string {
	sum := sha256.Sum256([]byte(projectDir + "\x00" + environment))
	suffix := hex.EncodeToString(sum[:])[:12]
	base := strings.ToLower(filepath.Base(projectDir))
	base = unsafeKeyChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "stack"
	}
	const maxBase = 40
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
		if base == "" {
			base = "stack"
		}
	}
	return base + "-" + suffix
}
