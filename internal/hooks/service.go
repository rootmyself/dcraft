package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// TrustVersion is the on-disk format version of the machine-local script trust
// record. A record written by a newer dcraft is refused, never guessed.
const TrustVersion = 1

const trustFile = "script-trust.json"

// StateDirs resolves the per-computer, per-stack state directory. The concrete
// *state.Store implements it; tests pass a temporary root.
type StateDirs interface {
	StackDir(core.Target) (string, error)
}

type trustEntry struct {
	Path       string    `json:"path"`
	Stage      Stage     `json:"stage"`
	SHA256     string    `json:"sha256"`
	ApprovedAt time.Time `json:"approved_at"`
}

type trustRecord struct {
	Version int          `json:"version"`
	Entries []trustEntry `json:"entries"`
}

// View is the non-executing description of one registered script, used by the
// `hooks` command. Problem is a short Korean reason when the script cannot run.
type View struct {
	Path          string `json:"path"`
	Stage         Stage  `json:"stage"`
	EffectivePath string `json:"effective_path"`
	SHA256        string `json:"sha256,omitempty"`
	Trusted       bool   `json:"trusted"`
	Changed       bool   `json:"changed"`
	Present       bool   `json:"present"`
	Executable    bool   `json:"executable"`
	Problem       string `json:"problem,omitempty"`
}

// Service owns the registration plan, the machine-local trust record and
// process execution. It never writes trust into the project config.
type Service struct {
	dirs   StateDirs
	getenv func(string) string

	mu sync.Mutex
}

// New builds the service. getenv is used only for redacting known
// environment-sourced secret references from hook output.
func New(dirs StateDirs, getenv func(string) string) *Service {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Service{dirs: dirs, getenv: getenv}
}

// Plan implements core.HookService. It lists every registered script for the
// target and computes the current content identity without writing anything.
func (s *Service) Plan(ctx context.Context, target core.Target) (core.HookPlan, error) {
	if err := ctx.Err(); err != nil {
		return core.HookPlan{}, err
	}
	if s == nil || s.dirs == nil {
		return core.HookPlan{}, failure(msgStateUnavailable, "hook service has no state store")
	}
	cat, err := Load(target.ProjectDir)
	if err != nil {
		return core.HookPlan{}, err
	}
	plan := core.HookPlan{Entries: []core.HookEntry{}}
	if !cat.Has() {
		return plan, nil
	}
	trust, err := s.loadTrust(target)
	if err != nil {
		return plan, err
	}
	for _, reg := range cat.Registrations {
		resolved, err := ResolvePath(target.ProjectDir, reg.Path)
		if err != nil {
			return plan, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return plan, invalid(fmt.Sprintf(msgScriptMissing, reg.Path), resolved)
			}
			return plan, invalid(msgScriptUnreadable, err.Error())
		}
		if info.IsDir() {
			return plan, invalid(fmt.Sprintf(msgScriptDirectory, reg.Path), resolved)
		}
		if !info.Mode().IsRegular() {
			return plan, invalid(fmt.Sprintf(msgScriptNotRegular, reg.Path), resolved)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return plan, invalid(fmt.Sprintf(msgScriptNotExecutable, reg.Path), resolved)
		}
		sha, err := contentSHA256(resolved)
		if err != nil {
			return plan, invalid(msgScriptUnreadable, err.Error())
		}
		approved, known := trust.lookup(resolved, reg.Stage)
		trusted := known && approved == sha
		plan.Entries = append(plan.Entries, core.HookEntry{
			Path:    reg.Path,
			Stage:   reg.Stage,
			SHA256:  sha,
			Trusted: trusted,
			Changed: known && approved != sha,
		})
	}
	for _, entry := range plan.Entries {
		if !entry.Trusted {
			plan.RequiresApproval = true
			break
		}
	}
	return plan, nil
}

// Approve records the current content identity for every plan entry that needs
// a new approval. It is only called by the engine after the independent
// script_trust approval was supplied.
func (s *Service) Approve(ctx context.Context, target core.Target, plan core.HookPlan, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.dirs == nil {
		return failure(msgStateUnavailable, "hook service has no state store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trust, err := s.loadTrust(target)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range plan.Entries {
		if entry.Trusted {
			continue
		}
		resolved, err := ResolvePath(target.ProjectDir, entry.Path)
		if err != nil {
			return err
		}
		trust.upsert(trustEntry{Path: resolved, Stage: entry.Stage, SHA256: entry.SHA256, ApprovedAt: at})
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveTrust(target, trust)
}

// Describe lists registrations for the hooks command without executing or
// approving anything. Missing or non-executable scripts are reported in the
// view instead of failing the whole list.
func (s *Service) Describe(ctx context.Context, target core.Target) ([]View, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.dirs == nil {
		return nil, failure(msgStateUnavailable, "hook service has no state store")
	}
	cat, err := Load(target.ProjectDir)
	if err != nil {
		return nil, err
	}
	trust, err := s.loadTrust(target)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(cat.Registrations))
	for _, reg := range cat.Registrations {
		view := View{Path: reg.Path, Stage: reg.Stage, Changed: false}
		resolved, err := ResolvePath(target.ProjectDir, reg.Path)
		if err != nil {
			view.Problem = err.Error()
			views = append(views, view)
			continue
		}
		view.EffectivePath = resolved
		info, statErr := os.Stat(resolved)
		switch {
		case statErr != nil:
			view.Problem = msgScriptMissing
		case info.IsDir():
			view.Problem = msgScriptDirectory
		case !info.Mode().IsRegular():
			view.Problem = msgScriptNotRegular
		default:
			view.Present = true
			view.Executable = info.Mode().Perm()&0o111 != 0
			if !view.Executable {
				view.Problem = msgScriptNotExecutableShort
			}
			if sha, hashErr := contentSHA256(resolved); hashErr == nil {
				view.SHA256 = sha
				if approved, ok := trust.lookup(resolved, reg.Stage); ok {
					view.Trusted = approved == sha
					view.Changed = approved != sha
				}
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) loadTrust(target core.Target) (trustRecord, error) {
	dir, err := s.dirs.StackDir(target)
	if err != nil {
		return trustRecord{}, failure(msgStateUnavailable, err.Error())
	}
	path := filepath.Join(dir, trustFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return trustRecord{Version: TrustVersion, Entries: []trustEntry{}}, nil
		}
		return trustRecord{}, failure(msgTrustUnreadable, err.Error())
	}
	if strings.TrimSpace(string(data)) == "" {
		return trustRecord{Version: TrustVersion, Entries: []trustEntry{}}, nil
	}
	var rec trustRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return trustRecord{}, failure(msgTrustCorrupt, err.Error())
	}
	if rec.Version > TrustVersion {
		return trustRecord{}, failure(msgTrustNewer, fmt.Sprintf("format %d", rec.Version))
	}
	if rec.Entries == nil {
		rec.Entries = []trustEntry{}
	}
	return rec, nil
}

func (s *Service) saveTrust(target core.Target, rec trustRecord) error {
	dir, err := s.dirs.StackDir(target)
	if err != nil {
		return failure(msgStateUnavailable, err.Error())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return failure(msgTrustWrite, err.Error())
	}
	rec.Version = TrustVersion
	sort.Slice(rec.Entries, func(i, j int) bool {
		if rec.Entries[i].Path != rec.Entries[j].Path {
			return rec.Entries[i].Path < rec.Entries[j].Path
		}
		return rec.Entries[i].Stage < rec.Entries[j].Stage
	})
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return failure(msgTrustWrite, err.Error())
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".script-trust-*.tmp")
	if err != nil {
		return failure(msgTrustWrite, err.Error())
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return failure(msgTrustWrite, err.Error())
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return failure(msgTrustWrite, err.Error())
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return failure(msgTrustWrite, err.Error())
	}
	if err := tmp.Close(); err != nil {
		return failure(msgTrustWrite, err.Error())
	}
	if err := os.Rename(tmpName, filepath.Join(dir, trustFile)); err != nil {
		return failure(msgTrustWrite, err.Error())
	}
	return nil
}

func (r trustRecord) lookup(path string, stage Stage) (string, bool) {
	for _, entry := range r.Entries {
		if entry.Path == path && entry.Stage == stage {
			return entry.SHA256, true
		}
	}
	return "", false
}

func (r *trustRecord) upsert(entry trustEntry) {
	for i := range r.Entries {
		if r.Entries[i].Path == entry.Path && r.Entries[i].Stage == entry.Stage {
			r.Entries[i] = entry
			return
		}
	}
	r.Entries = append(r.Entries, entry)
}

func contentSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ContentSHA256 exposes the exact content identity used for script trust. It
// is published for registration UIs and generated-script features.
func ContentSHA256(path string) (string, error) { return contentSHA256(path) }

func failure(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgStateActionOne, msgStateActionTwo},
	})
}
