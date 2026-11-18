package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

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

// LookupName returns the recorded project name for a target without creating,
// locking or modifying any state. Read-only features use it to identify the
// same stack as lifecycle operations. ok is false when no claim exists.
func (s *Store) LookupName(target core.Target) (NameAllocation, bool, error) {
	if strings.TrimSpace(target.ProjectDir) == "" {
		return NameAllocation{}, false, errors.New("state: name lookup has no project folder")
	}
	registry, err := loadNames(filepath.Join(s.root, "names.json"))
	if err != nil {
		return NameAllocation{}, false, err
	}
	claim, ok := registry.Claims[key(target.ProjectDir, target.Environment)]
	if !ok || claim.ProjectName == "" {
		return NameAllocation{}, false, nil
	}
	return NameAllocation{Name: claim.ProjectName, Source: claim.Source}, true, nil
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
