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
	"github.com/rootmyself/dcraft/internal/fsidentity"
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
	// Every state consumer (history, names, good version, locks, hook trust
	// and secret materialization) goes through StackDir. Verify an existing
	// root here so a shared, misowned or symlinked root cannot be used even by
	// a caller that never writes through this package. A missing root is not
	// created: StackDir stays read-only.
	if err := verifyRootIfExists(s.root); err != nil {
		return "", err
	}
	return filepath.Join(s.root, "stacks", key(target.ProjectDir, target.Environment)), nil
}

// PlanStateFingerprint implements core.PlanStateReader. It fingerprints this
// target's name claim so an approved plan cannot survive a name-map change
// made while the confirmation was on screen, while an unrelated stack's
// allocation does not invalidate the approval. Unavailable state is a stable
// marker, never an error, because the plan and Run both observe the same
// marker.
func (s *Store) PlanStateFingerprint(target core.Target) string {
	if err := verifyRootIfExists(s.root); err != nil {
		return "unavailable"
	}
	registry, err := loadNames(filepath.Join(s.root, "names.json"))
	if err != nil {
		return "unavailable"
	}
	claim, ok := registry.Claims[key(target.ProjectDir, target.Environment)]
	if !ok {
		return "none"
	}
	data, err := json.Marshal(claim)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AppendOperation appends one JSON line to <stack>/history.jsonl.
func (s *Store) AppendOperation(target core.Target, rec core.OperationRecord) error {
	dir, err := s.StackDir(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureRoot(s.root); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode history record: %w", err)
	}
	f, err := openStateFile(filepath.Join(dir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
// the folder's filesystem identity and the environment id, so equal folder
// names in different places (or different environments) never share state and
// every spelling of one folder (symlink, case variant on a case-insensitive
// filesystem, NFC/NFD) produces one stack. A folder that does not exist yet
// falls back to its canonical path; that is the documented non-existent-path
// identity.
func key(projectDir, environment string) string {
	identity := fsidentity.Key(projectDir)
	sum := sha256.Sum256([]byte(identity + "\x00" + environment))
	suffix := hex.EncodeToString(sum[:])[:12]
	base := strings.ToLower(filepath.Base(displayDir(projectDir)))
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

// displayDir is only the readable prefix of a state key, never the identity.
// It resolves symlinks when the path exists so the prefix stays meaningful.
func displayDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}
