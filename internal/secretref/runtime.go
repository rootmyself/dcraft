package secretref

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
)

// StateDirs resolves the per-stack state directory. *state.Store implements
// it; tests provide a temporary root.
type StateDirs interface {
	StackDir(core.Target) (string, error)
}

// Mode is the delivery lifecycle a caller needs.
type Mode int

const (
	// ModeCreate prepares values, materialized files and the Compose
	// override. It is used before "up" creates or recreates containers.
	ModeCreate Mode = iota
	// ModeFiles rewrites materialized file secrets from their current source.
	// It is used before "restart", which does not re-read Compose
	// configuration and does not need environment values.
	ModeFiles
)

// Runtime owns runtime secret preparation. It reads only the references the
// caller needs, keeps materialized files per stack under the state root, and
// never writes resolved values into configuration or state metadata.
type Runtime struct {
	dirs   StateDirs
	getenv func(string) string
	runner execx.Runner
}

// Unavailable is returned when a project saves runtime references but the
// application was not wired with the secret runtime. It is a programming
// error, never a silent no-op.
func Unavailable() *core.Error {
	return applyError("비밀값 전달 연결이 준비되지 않았습니다.", "secret runtime is not configured")
}

// NewRuntime builds the runtime. getenv should return the process environment
// used for environment-variable sources. AWS references use the installed AWS
// CLI and therefore the operator's existing credential chain.
func NewRuntime(dirs StateDirs, getenv func(string) string) *Runtime {
	return NewRuntimeWithRunner(dirs, getenv, execx.NewOSRunner())
}

// NewRuntimeWithRunner is NewRuntime with an explicit external-command
// boundary. Tests use it to run the full preparation path without AWS.
func NewRuntimeWithRunner(dirs StateDirs, getenv func(string) string, runner execx.Runner) *Runtime {
	if getenv == nil {
		getenv = os.Getenv
	}
	if runner == nil {
		runner = execx.NewOSRunner()
	}
	return &Runtime{dirs: dirs, getenv: getenv, runner: runner}
}

// Session is one prepared delivery. It carries the extra Compose file, the
// process environment additions and the known values used for redaction.
type Session struct {
	// Files are dcraft-managed Compose override files to append to the
	// effective Compose inputs. Existing user Compose files are never edited.
	Files []string
	// Env are environment entries for the Compose subprocess only.
	Env []string
	// Notices are Korean messages safe to show after a successful run.
	Notices []string

	runtime       *Runtime
	target        core.Target
	mode          Mode
	refs          []Reference
	allRefs       []Reference
	selected      []string
	values        []string
	secretsDir    string
	overridePath  string
	materialized  map[string]string
	created       []string
	wroteOverride bool
	hadOverride   bool
	overwrote     bool
	explicit      bool
}

// Prepare resolves the references needed for services and prepares the
// delivery. An empty services list means the whole stack. It resolves every
// source before writing any file, so a missing or empty source leaves no
// partial materialization.
func (r *Runtime) Prepare(ctx context.Context, target core.Target, services []string, mode Mode) (Session, error) {
	cat, err := Load(target.ProjectDir)
	if err != nil {
		return Session{}, err
	}
	return r.prepare(ctx, target, cat.ForPhaseServices(PhaseRuntime, services), services, cat.References, false, mode)
}

// PrepareReferences prepares the delivery from an explicit reference list
// instead of the project's current .dcraft.yaml. Rollback uses exactly the
// references stored in the previous-good record; current configuration is
// never substituted. Only runtime-phase references are delivered.
func (r *Runtime) PrepareReferences(ctx context.Context, target core.Target, refs []Reference, services []string, mode Mode) (Session, error) {
	selected := map[string]bool{}
	for _, name := range services {
		selected[name] = true
	}
	runtimeRefs := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		if ref.Phase != "" && ref.Phase != PhaseRuntime {
			continue
		}
		if len(selected) > 0 && !selected[ref.Service] {
			continue
		}
		runtimeRefs = append(runtimeRefs, ref)
	}
	return r.prepare(ctx, target, runtimeRefs, services, refs, true, mode)
}

func (r *Runtime) prepare(ctx context.Context, target core.Target, refs []Reference, services []string, allRefs []Reference, explicit bool, mode Mode) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if r == nil || r.dirs == nil {
		return Session{}, applyError("비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	if strings.TrimSpace(target.ProjectDir) == "" {
		return Session{}, applyError("비밀값 전달 대상 폴더를 확인할 수 없습니다.", "target has no project folder")
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return Session{}, applyError("비밀값 전달 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	session := Session{
		runtime:      r,
		target:       target,
		mode:         mode,
		refs:         refs,
		allRefs:      append([]Reference{}, allRefs...),
		selected:     append([]string{}, services...),
		explicit:     explicit,
		secretsDir:   filepath.Join(stackDir, "secrets"),
		overridePath: filepath.Join(stackDir, "compose.secrets.yaml"),
		materialized: map[string]string{},
	}
	if len(refs) == 0 {
		// Nothing to deliver for this selection. If the project has no runtime
		// references at all, remove any override left by an older
		// configuration. If other services still have runtime references,
		// keep the on-disk override for the stack; it is regenerated below on
		// success. Build-phase references are never runtime delivery.
		if !explicit && mode == ModeCreate {
			if err := os.Remove(session.overridePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return Session{}, applyError("dcraft 관리 Compose override를 정리하지 못했습니다.", err.Error())
			}
		}
		return session, nil
	}

	// Resolve every source before writing anything.
	fetcher := newAWSFetcher(r.runner)
	items := make([]resolvedItem, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return Session{}, err
		}
		if mode == ModeFiles && ref.Delivery != DeliveryFile {
			// Restart does not re-read Compose configuration and does not need
			// to re-read environment-delivery values: Docker already stored
			// them in the container. File delivery is a live bind mount, so
			// its materialized file is rewritten from the current source on
			// every apply. dcraft never caches or compares old values.
			continue
		}
		switch ref.Source {
		case SourceEnv:
			value, err := r.readEnv(ref)
			if err != nil {
				return Session{}, err
			}
			items = append(items, resolvedItem{ref: ref, value: value})
		case SourceFile:
			value, err := r.readSourceFile(target, ref)
			if err != nil {
				return Session{}, err
			}
			items = append(items, resolvedItem{ref: ref, value: value})
		case SourceAWS:
			value, err := fetcher.fetch(ctx, ref)
			if err != nil {
				return Session{}, err
			}
			items = append(items, resolvedItem{ref: ref, value: value})
		default:
			return Session{}, applyError("지원하지 않는 비밀값 원본입니다.", string(ref.Source))
		}
	}
	for _, item := range items {
		session.values = append(session.values, string(item.value))
	}

	if mode == ModeFiles {
		if len(items) > 0 {
			if err := os.MkdirAll(session.secretsDir, 0o700); err != nil {
				return Session{}, applyError("비밀값 전달 폴더를 만들지 못했습니다.", err.Error())
			}
		}
		for _, item := range items {
			if item.ref.Delivery != DeliveryFile {
				continue
			}
			if err := session.ensureMaterialized(item.ref, item.value); err != nil {
				return Session{}, session.abortError(err)
			}
		}
		session.Notices = append(session.Notices, msgFilesEnsured)
		if count := awsSourceCount(items); count > 0 {
			session.Notices = append(session.Notices, fmt.Sprintf(msgAWSFetchedRuntime, count), msgAWSCredentialDisclosure)
		}
		return session, nil
	}

	if err := r.checkStateOutsideProject(target.ProjectDir, stackDir); err != nil {
		return Session{}, err
	}
	if err := os.MkdirAll(session.secretsDir, 0o700); err != nil {
		return Session{}, applyError("비밀값 전달 폴더를 만들지 못했습니다.", err.Error())
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return Session{}, session.abortError(err)
		}
		if item.ref.Delivery != DeliveryFile {
			continue
		}
		path := session.materializedPath(item.ref)
		if err := session.ensureMaterialized(item.ref, item.value); err != nil {
			return Session{}, session.abortError(err)
		}
		session.materialized[item.ref.Key()] = path
	}
	if err := r.writeOverride(target, &session, items); err != nil {
		return Session{}, session.abortError(err)
	}
	session.Files = []string{session.overridePath}
	session.Notices = append(session.Notices, fmt.Sprintf(msgAppliedOverride, session.overridePath))
	session.Notices = append(session.Notices, appliedNotices(items)...)
	if count := awsSourceCount(items); count > 0 {
		session.Notices = append(session.Notices, fmt.Sprintf(msgAWSFetchedRuntime, count), msgAWSCredentialDisclosure)
	}
	return session, nil
}

// Abort removes files created by this preparation and the override when this
// run created it. Files that existed before are left in place: dcraft never
// stores previous values, so it cannot and does not restore them. The bool
// reports whether an existing materialized file was already overwritten, so
// the caller can tell the user that a partial rotation may remain.
func (s *Session) Abort() (partial bool) {
	partial = s.overwrote
	for _, path := range s.created {
		_ = os.Remove(path)
	}
	if s.wroteOverride {
		if s.hadOverride {
			// Keep the on-disk effective inputs complete and value-free.
			_ = s.runtime.RegenerateOverride(s.target)
		} else {
			_ = os.Remove(s.overridePath)
		}
	}
	return partial
}

func (s *Session) abortError(err error) error {
	if !s.Abort() {
		return err
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategorySecretApplyFailed,
		Severity:    core.SeverityWarning,
		Message:     msgPartialOverwrite,
		NextActions: []string{msgPartialOverwriteAction},
	})
}

// Commit finalizes a successful preparation. After a create-mode "up", stale
// materialized files are removed only for the services this operation could
// have recreated: the whole stack for an empty selection, or the selected
// closure for a partial selection. Removed references keep their file until
// this safe point or until "down", so a running container can still restart
// in the meantime. The on-disk override is regenerated from the full catalog
// (references only) so the effective inputs stay complete and reproducible.
func (s *Session) Commit() {
	if s.mode != ModeCreate || s.explicit {
		return
	}
	current := map[string]bool{}
	for _, ref := range s.refs {
		if ref.Delivery == DeliveryFile {
			current[s.materializedPath(ref)] = true
		}
	}
	entries, err := os.ReadDir(s.secretsDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(s.secretsDir, entry.Name())
			if current[path] {
				continue
			}
			if len(s.selected) > 0 && !matchesSelected(entry.Name(), s.selected) {
				continue
			}
			_ = os.Remove(path)
		}
	}
	if s.runtime != nil {
		_ = s.runtime.RegenerateOverride(s.target)
	}
}

func matchesSelected(filename string, services []string) bool {
	for _, service := range services {
		if strings.HasPrefix(filename, sanitizePart(service)+"__") {
			return true
		}
	}
	return false
}

// Redact removes known fetched values from tool output, diagnostics and error
// text. It is not a guarantee about values the application itself prints.
func (s Session) Redact(text string) string {
	return redactValues(text, s.values)
}

// HasValues reports whether this session fetched at least one value.
func (s Session) HasValues() bool { return len(s.values) > 0 }

// RedactText removes values referenced by the project's saved secret
// references from tool output. It is the shared redaction entry point for
// diagnostics that are not part of a prepared session (for example backup
// helper output). Reads are best-effort: an unavailable source is skipped, and
// the value is never returned, persisted or printed.
func (r *Runtime) RedactText(target core.Target, text string) string {
	if text == "" || r == nil {
		return text
	}
	catalog, err := Load(target.ProjectDir)
	if err != nil || !catalog.Has() {
		return text
	}
	values := make([]string, 0, len(catalog.References))
	for _, ref := range catalog.References {
		switch ref.Source {
		case SourceEnv:
			if value := r.getenv(ref.Ref); value != "" {
				values = append(values, value)
			}
		case SourceFile:
			path := strings.TrimSpace(ref.Ref)
			if !filepath.IsAbs(path) {
				path = filepath.Join(target.ProjectDir, path)
			}
			info, statErr := os.Stat(filepath.Clean(path))
			// Redaction never reads an unbounded file or a non-regular one.
			if statErr != nil || info.IsDir() || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				continue
			}
			raw, readErr := os.ReadFile(filepath.Clean(path))
			if readErr != nil {
				continue
			}
			value := string(raw)
			if value != "" {
				values = append(values, value)
			}
		}
	}
	return redactValues(text, values)
}

func redactValues(text string, values []string) string {
	if text == "" || len(values) == 0 {
		return text
	}
	variants := make([]string, 0, len(values)*2)
	for _, value := range values {
		if value == "" {
			continue
		}
		variants = append(variants, value)
		// A Dockerfile can print a file value through a command substitution,
		// which strips the trailing newline. Redact that form too.
		if trimmed := strings.TrimRight(value, "\r\n"); trimmed != "" && trimmed != value {
			variants = append(variants, trimmed)
		}
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	for _, value := range variants {
		text = strings.ReplaceAll(text, value, msgRedacted)
	}
	return text
}

func (r *Runtime) readEnv(ref Reference) ([]byte, error) {
	name := strings.TrimSpace(ref.Ref)
	value := r.getenv(name)
	if strings.TrimSpace(value) == "" {
		return nil, sourceError(
			fmt.Sprintf(msgEnvSourceMissing, name),
			"environment variable is unset or empty",
			[]string{msgEnvSourceActionOne, msgEnvSourceActionTwo},
		)
	}
	if strings.ContainsRune(value, 0) {
		return nil, sourceError(
			fmt.Sprintf(msgEnvSourceInvalid, name),
			"environment value contains a NUL byte",
			[]string{msgEnvSourceActionTwo},
		)
	}
	return []byte(value), nil
}

func (r *Runtime) readSourceFile(target core.Target, ref Reference) ([]byte, error) {
	path := strings.TrimSpace(ref.Ref)
	if !filepath.IsAbs(path) {
		path = filepath.Join(target.ProjectDir, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, sourceError(
				fmt.Sprintf(msgFileSourceMissing, ref.Ref),
				path,
				[]string{msgFileSourceActionOne, msgFileSourceActionTwo},
			)
		}
		return nil, sourceError(
			fmt.Sprintf(msgFileSourceUnreadable, ref.Ref),
			err.Error(),
			[]string{msgFileSourceActionTwo},
		)
	}
	if info.IsDir() {
		return nil, sourceError(
			fmt.Sprintf(msgFileSourceInvalid, ref.Ref),
			"source is a directory",
			[]string{msgFileSourceActionTwo},
		)
	}
	if !info.Mode().IsRegular() {
		return nil, sourceError(
			fmt.Sprintf(msgFileSourceInvalid, ref.Ref),
			"source is not a regular file",
			[]string{msgFileSourceActionTwo},
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, sourceError(
			fmt.Sprintf(msgFileSourceUnreadable, ref.Ref),
			err.Error(),
			[]string{msgFileSourceActionTwo},
		)
	}
	if len(data) == 0 {
		return nil, sourceError(
			fmt.Sprintf(msgFileSourceEmpty, ref.Ref),
			"source file is empty",
			[]string{msgFileSourceActionTwo},
		)
	}
	return data, nil
}

func (s *Session) ensureMaterialized(ref Reference, value []byte) error {
	path := s.materializedPath(ref)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return applyError("이전 비밀값 전달 파일을 정리하지 못했습니다.", err.Error())
			}
			s.created = append(s.created, path)
		} else {
			s.overwrote = true
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		s.created = append(s.created, path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return applyError("비밀값 전달 파일을 만들지 못했습니다.", err.Error())
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return applyError("비밀값 전달 파일 권한을 설정하지 못했습니다.", err.Error())
	}
	if _, err := f.Write(value); err != nil {
		_ = f.Close()
		return applyError("비밀값 전달 파일을 쓰지 못했습니다.", err.Error())
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return applyError("비밀값 전달 파일을 저장하지 못했습니다.", err.Error())
	}
	if err := f.Close(); err != nil {
		return applyError("비밀값 전달 파일을 닫지 못했습니다.", err.Error())
	}
	return nil
}

func (s *Session) materializedPath(ref Reference) string {
	return filepath.Join(s.secretsDir, materializedName(ref))
}

func (r *Runtime) writeOverride(target core.Target, session *Session, items []resolvedItem) error {
	// The override contains only references and paths, never values.
	doc := overrideDoc{Services: map[string]overrideService{}}
	type envEntry struct {
		ref   Reference
		value string
	}
	fileSecrets := map[string]overrideSecret{}
	for i, item := range items {
		svc := doc.Services[item.ref.Service]
		switch item.ref.Delivery {
		case DeliveryEnvironment:
			if svc.Environment == nil {
				svc.Environment = map[string]string{}
			}
			placeholder := fmt.Sprintf("DCRAFT_SECRET_%d", i+1)
			svc.Environment[item.ref.EffectiveTarget()] = "${" + placeholder + "}"
			session.Env = append(session.Env, placeholder+"="+string(item.value))
		case DeliveryFile:
			name := fmt.Sprintf("dcraft_secret_%d", i+1)
			fileSecrets[name] = overrideSecret{File: session.materializedPath(item.ref)}
			svc.Secrets = append(svc.Secrets, overrideServiceSecret{
				Source: name,
				Target: item.ref.EffectiveTarget(),
			})
		}
		doc.Services[item.ref.Service] = svc
	}
	if len(fileSecrets) > 0 {
		doc.Secrets = fileSecrets
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return applyError("dcraft 관리 Compose override를 만들지 못했습니다.", err.Error())
	}
	header := "# dcraft-managed runtime secret override. Do not edit; dcraft regenerates it.\n"
	if _, err := os.Stat(session.overridePath); err == nil {
		session.hadOverride = true
	}
	tmp, err := os.CreateTemp(filepath.Dir(session.overridePath), ".compose-secrets-*.tmp")
	if err != nil {
		return applyError("dcraft 관리 Compose override를 쓰지 못했습니다.", err.Error())
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return applyError("dcraft 관리 Compose override 권한을 설정하지 못했습니다.", err.Error())
	}
	if _, err := tmp.Write(append([]byte(header), data...)); err != nil {
		_ = tmp.Close()
		return applyError("dcraft 관리 Compose override를 쓰지 못했습니다.", err.Error())
	}
	if err := tmp.Close(); err != nil {
		return applyError("dcraft 관리 Compose override를 쓰지 못했습니다.", err.Error())
	}
	if err := os.Rename(tmpName, session.overridePath); err != nil {
		return applyError("dcraft 관리 Compose override를 적용하지 못했습니다.", err.Error())
	}
	session.wroteOverride = true
	session.Files = []string{session.overridePath}
	return nil
}

func (r *Runtime) checkStateOutsideProject(projectDir, stackDir string) error {
	project, err := filepath.Abs(projectDir)
	if err != nil {
		return applyError("프로젝트 폴더를 확인할 수 없습니다.", err.Error())
	}
	state, err := filepath.Abs(stackDir)
	if err != nil {
		return applyError("상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	rel, err := filepath.Rel(project, state)
	if err != nil {
		return nil
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return applyError(msgStateInsideProject, state)
	}
	return nil
}

// OverridePath is the deterministic dcraft-managed Compose override path for
// a target. It is part of the effective Compose inputs when runtime secrets
// exist.
func (r *Runtime) OverridePath(target core.Target) (string, error) {
	if r == nil || r.dirs == nil {
		return "", applyError("비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return "", applyError("비밀값 전달 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	return filepath.Join(stackDir, "compose.secrets.yaml"), nil
}

// MaterializedPath returns the per-stack materialized file path for a file
// delivery reference.
func (r *Runtime) MaterializedPath(target core.Target, ref Reference) (string, error) {
	if r == nil || r.dirs == nil {
		return "", applyError("비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return "", applyError("비밀값 전달 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	return filepath.Join(stackDir, "secrets", materializedName(ref)), nil
}

// RemoveMaterialized deletes one file-delivery materialization after its
// reference was removed. A running container may lose access until it is
// recreated or removed; the caller must warn about that.
func (r *Runtime) RemoveMaterialized(target core.Target, ref Reference) error {
	if ref.Delivery != DeliveryFile {
		return nil
	}
	path, err := r.MaterializedPath(target, ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return applyError("비밀값 전달 파일을 정리하지 못했습니다.", err.Error())
	}
	return nil
}

// CleanupStack removes every materialized file and the managed overrides for
// one stack, both runtime and build delivery. It is called only after a
// successful "down"; a failed teardown keeps the files a restart may still
// need.
func (r *Runtime) CleanupStack(target core.Target) error {
	if r == nil || r.dirs == nil {
		return nil
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return applyError("비밀값 전달 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	for _, name := range []string{"compose.secrets.yaml", buildOverrideName} {
		if err := os.Remove(filepath.Join(stackDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return applyError("dcraft 관리 Compose override를 정리하지 못했습니다.", err.Error())
		}
	}
	for _, dir := range []string{"secrets", buildSecretsDir} {
		if err := os.RemoveAll(filepath.Join(stackDir, dir)); err != nil {
			return applyError("비밀값 전달 파일을 정리하지 못했습니다.", err.Error())
		}
	}
	return nil
}

// RegenerateOverride rewrites the managed overrides from the current
// references, without resolving any values. Runtime and build delivery are
// written to separate files so one phase can never be delivered by the other.
// It is used after add/remove and after a completed operation so the visible
// effective inputs match the saved configuration.
func (r *Runtime) RegenerateOverride(target core.Target) error {
	if r == nil || r.dirs == nil {
		return nil
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return applyError("비밀값 전달 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	cat, err := Load(target.ProjectDir)
	if err != nil {
		return err
	}

	runtimePath := filepath.Join(stackDir, "compose.secrets.yaml")
	runtimeRefs := cat.ForPhase(PhaseRuntime)
	if len(runtimeRefs) == 0 {
		if err := os.Remove(runtimePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return applyError("dcraft 관리 Compose override를 정리하지 못했습니다.", err.Error())
		}
	} else {
		if err := os.MkdirAll(stackDir, 0o700); err != nil {
			return applyError("비밀값 전달 상태 폴더를 만들지 못했습니다.", err.Error())
		}
		doc := overrideDoc{Services: map[string]overrideService{}}
		fileSecrets := map[string]overrideSecret{}
		for i, ref := range runtimeRefs {
			svc := doc.Services[ref.Service]
			switch ref.Delivery {
			case DeliveryEnvironment:
				if svc.Environment == nil {
					svc.Environment = map[string]string{}
				}
				svc.Environment[ref.EffectiveTarget()] = fmt.Sprintf("${DCRAFT_SECRET_%d}", i+1)
			case DeliveryFile:
				name := fmt.Sprintf("dcraft_secret_%d", i+1)
				path, err := r.MaterializedPath(target, ref)
				if err != nil {
					return err
				}
				fileSecrets[name] = overrideSecret{File: path}
				svc.Secrets = append(svc.Secrets, overrideServiceSecret{Source: name, Target: ref.EffectiveTarget()})
			}
			doc.Services[ref.Service] = svc
		}
		if len(fileSecrets) > 0 {
			doc.Secrets = fileSecrets
		}
		data, err := yaml.Marshal(doc)
		if err != nil {
			return applyError("dcraft 관리 Compose override를 만들지 못했습니다.", err.Error())
		}
		header := "# dcraft-managed runtime secret override. Do not edit; dcraft regenerates it.\n"
		if err := writePrivateFile(runtimePath, append([]byte(header), data...)); err != nil {
			return err
		}
	}

	return r.writeBuildOverride(target, cat.ForPhase(PhaseBuild))
}

type overrideDoc struct {
	Services map[string]overrideService `yaml:"services"`
	Secrets  map[string]overrideSecret  `yaml:"secrets,omitempty"`
}

type overrideSecret struct {
	File string `yaml:"file"`
}

type overrideService struct {
	Environment map[string]string       `yaml:"environment,omitempty"`
	Secrets     []overrideServiceSecret `yaml:"secrets,omitempty"`
}

type overrideServiceSecret struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

func materializedName(ref Reference) string {
	sum := sha256.Sum256([]byte(ref.Service + "\x00" + ref.EffectiveTarget()))
	return sanitizePart(ref.Service) + "__" + sanitizePart(ref.EffectiveTarget()) + "__" + hex.EncodeToString(sum[:])[:8] + ".secret"
}

func sanitizePart(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		out = "secret"
	}
	if len(out) > 32 {
		out = strings.Trim(out[:32], "-_")
	}
	return out
}

type resolvedItem struct {
	ref   Reference
	value []byte
}

func appliedNotices(items []resolvedItem) []string {
	var envTargets, fileTargets []string
	for _, item := range items {
		label := item.ref.Service + ":" + item.ref.EffectiveTarget()
		switch item.ref.Delivery {
		case DeliveryEnvironment:
			envTargets = append(envTargets, label)
		case DeliveryFile:
			fileTargets = append(fileTargets, label)
		}
	}
	var notices []string
	if len(envTargets) > 0 {
		notices = append(notices, fmt.Sprintf(msgAppliedEnv, strings.Join(envTargets, ", ")))
	}
	if len(fileTargets) > 0 {
		notices = append(notices, fmt.Sprintf(msgAppliedFile, strings.Join(fileTargets, ", ")))
	}
	if len(envTargets) > 0 {
		notices = append(notices, msgEnvVisible)
	}
	if len(fileTargets) > 0 {
		notices = append(notices, msgFileAppSupport)
	}
	return notices
}

func sourceError(message, detail string, actions []string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategorySecretSource,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: actions,
	})
}

func applyError(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategorySecretApplyFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgApplyActionOne, msgApplyActionTwo},
	})
}

// CheckAWS resolves one recorded AWS reference far enough to prove the source
// and selection are available. It returns the same classified errors as
// delivery, never returns the value and stores nothing. Rollback uses it for
// its availability precheck.
func (r *Runtime) CheckAWS(ctx context.Context, ref Reference) error {
	if err := validateAWSSelector(ref); err != nil {
		return inputError(err)
	}
	fetcher := newAWSFetcher(r.runner)
	_, err := fetcher.fetch(ctx, ref)
	return err
}

func awsSourceCount(items []resolvedItem) int {
	count := 0
	for _, item := range items {
		if item.ref.Source == SourceAWS {
			count++
		}
	}
	return count
}

// CheckMaterialized proves that every required file-delivery materialization
// already exists, without reading its source or rewriting the file. Restart
// reuses the bytes the running container already mounts; it must not apply a
// rotated value. A missing file fails before the containers are touched with
// guidance to run `up`, the configuration path.
func (r *Runtime) CheckMaterialized(target core.Target, refs []Reference) error {
	if r == nil || r.dirs == nil {
		if len(refs) == 0 {
			return nil
		}
		return Unavailable()
	}
	for _, ref := range refs {
		if ref.Delivery != DeliveryFile {
			continue
		}
		path, err := r.MaterializedPath(target, ref)
		if err != nil {
			return err
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return core.Fail(core.Diagnostic{
				Category:    core.CategorySecretApplyFailed,
				Severity:    core.SeverityError,
				Message:     fmt.Sprintf(msgMaterializedMissing, ref.Service, ref.EffectiveTarget()),
				Detail:      path,
				NextActions: []string{msgMaterializedMissingAction},
			})
		}
	}
	return nil
}
