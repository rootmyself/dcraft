package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// NameSource explains where an effective Compose project name came from.
type NameSource string

const (
	// NameSourceExplicit is a name supplied with -p/--project-name.
	NameSourceExplicit NameSource = "explicit"
	// NameSourceConfigured is a name declared by the Compose configuration
	// (a project `name:` or COMPOSE_PROJECT_NAME) that differs from the
	// folder-derived default.
	NameSourceConfigured NameSource = "configured"
	// NameSourceAllocated is an automatic readable name.
	NameSourceAllocated NameSource = "allocated"
	// NameSourceExisting is the name of a project that already exists for the
	// same folder (created outside dcraft or before its state was lost).
	NameSourceExisting NameSource = "existing"
)

// ExternalProject is one Compose project that already exists on the local
// engine. ProjectDir is the directory of its first config file.
type ExternalProject struct {
	Name       string
	ProjectDir string
}

// NameRequest is the input of automatic name allocation.
type NameRequest struct {
	Target core.Target
	// DeclaredName must be honored as-is (-p or a configured project name).
	// Empty means dcraft may allocate BaseName or its smallest free suffix.
	DeclaredName string
	// DeclaredSource is NameSourceExplicit or NameSourceConfigured.
	DeclaredSource NameSource
	// BaseName is Compose's own derived default name for the target folder.
	BaseName string
	// External lists projects that already exist on this engine.
	External []ExternalProject
}

// NameAllocation is the stable name of one target.
type NameAllocation struct {
	Name   string
	Source NameSource
	// PreviousName is set when a declared name replaced an automatically
	// assigned name for the same target.
	PreviousName string
}

// NameConflictError means the requested name belongs to another target. dcraft
// never renames or reuses the other stack; the user has to resolve it.
type NameConflictError struct {
	Name             string
	Target           core.Target
	OwnerDir         string
	OwnerEnvironment string
	External         bool
}

func (e *NameConflictError) Error() string {
	owner := e.OwnerDir
	if e.OwnerEnvironment != "" {
		owner += " (environment " + e.OwnerEnvironment + ")"
	}
	if owner == "" {
		owner = "another stack"
	}
	return fmt.Sprintf("project name %q is already used by %s", e.Name, owner)
}

const nameRegistryVersion = 1

type nameClaim struct {
	ProjectName string     `json:"project_name"`
	Source      NameSource `json:"source"`
	ProjectDir  string     `json:"project_dir"`
	Environment string     `json:"environment"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type nameRegistry struct {
	Version int                  `json:"version"`
	Claims  map[string]nameClaim `json:"claims"`
}

// AllocateName resolves the Compose project name of one target and records the
// mapping in the per-computer state root. Automatic names are readable and
// reused on rerun, after stop and after down; declared names are honored and
// never silently renamed. The update is atomic and serialized across
// processes.
func (s *Store) AllocateName(ctx context.Context, req NameRequest) (NameAllocation, error) {
	if err := ctx.Err(); err != nil {
		return NameAllocation{}, err
	}
	if strings.TrimSpace(req.Target.ProjectDir) == "" {
		return NameAllocation{}, errors.New("state: name request has no project folder")
	}
	declared := strings.TrimSpace(req.DeclaredName)
	if declared != "" && req.DeclaredSource != NameSourceExplicit && req.DeclaredSource != NameSourceConfigured {
		return NameAllocation{}, fmt.Errorf("state: declared name %q needs an explicit or configured source", declared)
	}
	if declared == "" {
		if err := validateName(req.BaseName); err != nil {
			return NameAllocation{}, fmt.Errorf("state: invalid base project name: %w", err)
		}
	}

	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return NameAllocation{}, fmt.Errorf("create state root: %w", err)
	}
	release, err := acquireFileLock(ctx, filepath.Join(s.root, "names.lock"))
	if err != nil {
		return NameAllocation{}, err
	}
	defer release()

	path := filepath.Join(s.root, "names.json")
	registry, err := loadNames(path)
	if err != nil {
		return NameAllocation{}, err
	}
	key := key(req.Target.ProjectDir, req.Target.Environment)
	ourDir := composeProjectDir(req.Target)
	now := time.Now().UTC()

	if declared != "" {
		if err := validateName(declared); err != nil {
			return NameAllocation{}, fmt.Errorf("state: invalid declared project name: %w", err)
		}
		current, hasCurrent := registry.Claims[key]
		if hasCurrent && current.ProjectName == declared {
			if current.Source != req.DeclaredSource {
				current.Source = req.DeclaredSource
				current.UpdatedAt = now
				registry.Claims[key] = current
				if err := saveNames(path, registry); err != nil {
					return NameAllocation{}, err
				}
			}
			return NameAllocation{Name: declared, Source: req.DeclaredSource}, nil
		}
		if owner, taken := claimOwner(registry.Claims, key, declared); taken {
			return NameAllocation{}, &NameConflictError{
				Name: declared, Target: req.Target,
				OwnerDir: owner.ProjectDir, OwnerEnvironment: owner.Environment,
			}
		}
		if ext, taken := externalOwner(req.External, declared, ourDir); taken {
			return NameAllocation{}, &NameConflictError{
				Name: declared, Target: req.Target,
				OwnerDir: ext.ProjectDir, External: true,
			}
		}
		previous := ""
		if hasCurrent {
			previous = current.ProjectName
		}
		registry.Claims[key] = nameClaim{
			ProjectName: declared, Source: req.DeclaredSource,
			ProjectDir: req.Target.ProjectDir, Environment: req.Target.Environment,
			UpdatedAt: now,
		}
		if err := saveNames(path, registry); err != nil {
			return NameAllocation{}, err
		}
		return NameAllocation{Name: declared, Source: req.DeclaredSource, PreviousName: previous}, nil
	}

	if current, ok := registry.Claims[key]; ok && current.ProjectName != "" {
		if _, taken := claimOwner(registry.Claims, key, current.ProjectName); taken {
			return NameAllocation{}, &NameConflictError{
				Name: current.ProjectName, Target: req.Target,
				OwnerDir: "", External: false,
			}
		}
		if ext, taken := externalOwner(req.External, current.ProjectName, ourDir); taken {
			return NameAllocation{}, &NameConflictError{
				Name: current.ProjectName, Target: req.Target,
				OwnerDir: ext.ProjectDir, External: true,
			}
		}
		return NameAllocation{Name: current.ProjectName, Source: current.Source}, nil
	}

	base := req.BaseName
	if env := sanitizeNamePart(req.Target.Environment); env != "" {
		base += "-" + env
	}
	if err := validateName(base); err != nil {
		return NameAllocation{}, fmt.Errorf("state: invalid automatic project name: %w", err)
	}

	for n := 0; n < 1000; n++ {
		candidate := base
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d", base, n)
		}
		if _, taken := claimOwner(registry.Claims, key, candidate); taken {
			continue
		}
		if _, taken := externalOwner(req.External, candidate, ourDir); taken {
			continue
		}
		source := NameSourceAllocated
		if ext, ok := externalMine(req.External, candidate, ourDir); ok {
			source = NameSourceExisting
			_ = ext
		}
		registry.Claims[key] = nameClaim{
			ProjectName: candidate, Source: source,
			ProjectDir: req.Target.ProjectDir, Environment: req.Target.Environment,
			UpdatedAt: now,
		}
		if err := saveNames(path, registry); err != nil {
			return NameAllocation{}, err
		}
		return NameAllocation{Name: candidate, Source: source}, nil
	}
	return NameAllocation{}, fmt.Errorf("state: no free automatic project name for %s", base)
}

func loadNames(path string) (nameRegistry, error) {
	registry := nameRegistry{Version: nameRegistryVersion, Claims: map[string]nameClaim{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return nameRegistry{}, fmt.Errorf("read name registry: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return registry, nil
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nameRegistry{}, fmt.Errorf("parse name registry: %w", err)
	}
	if registry.Version > nameRegistryVersion {
		return nameRegistry{}, fmt.Errorf("name registry version %d is newer than this dcraft", registry.Version)
	}
	if registry.Version == 0 {
		registry.Version = nameRegistryVersion
	}
	if registry.Claims == nil {
		registry.Claims = map[string]nameClaim{}
	}
	return registry, nil
}

func saveNames(path string, registry nameRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode name registry: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".names-*.tmp")
	if err != nil {
		return fmt.Errorf("create name registry temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("name registry permissions: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write name registry: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync name registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close name registry: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace name registry: %w", err)
	}
	return nil
}

func claimOwner(claims map[string]nameClaim, ourKey, name string) (nameClaim, bool) {
	for key, claim := range claims {
		if key == ourKey {
			continue
		}
		if claim.ProjectName == name {
			return claim, true
		}
	}
	return nameClaim{}, false
}

func externalOwner(external []ExternalProject, name, ourDir string) (ExternalProject, bool) {
	for _, ext := range external {
		if ext.Name != name {
			continue
		}
		if !sameDir(ext.ProjectDir, ourDir) {
			return ext, true
		}
	}
	return ExternalProject{}, false
}

func externalMine(external []ExternalProject, name, ourDir string) (ExternalProject, bool) {
	for _, ext := range external {
		if ext.Name == name && sameDir(ext.ProjectDir, ourDir) {
			return ext, true
		}
	}
	return ExternalProject{}, false
}

// composeProjectDir is Compose's project directory: the directory of the first
// config file. It identifies which folder an existing project belongs to.
func composeProjectDir(t core.Target) string {
	if len(t.Files) > 0 && t.Files[0] != "" {
		return filepath.Dir(t.Files[0])
	}
	return t.ProjectDir
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func validateName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if !isNameStart(name[0]) {
		return fmt.Errorf("%q must start with a lowercase letter or digit", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("%q contains unsupported character %q", name, r)
		}
	}
	return nil
}

func sanitizeNamePart(part string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(part) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	for len(out) > 0 && !isNameStart(out[0]) {
		out = out[1:]
	}
	return out
}

func isNameStart(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}
