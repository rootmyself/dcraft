package secretref

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
)

// The build override is a second dcraft-managed Compose file. It is appended
// with -f only for build operations. It declares Compose build.secrets, which
// BuildKit exposes to a Dockerfile as RUN --mount=type=secret. It never carries
// a value, and the user's Compose files and Dockerfiles are never edited.
const (
	buildOverrideName = "compose.build-secrets.yaml"
	buildSecretsDir   = "build-secrets"
	buildOverrideHead = "# dcraft-managed build secret override. Do not edit; dcraft regenerates it.\n# Values are materialized only under the dcraft state directory and only for a dcraft build.\n"
)

type buildOverrideDoc struct {
	Services map[string]buildOverrideService `yaml:"services"`
	Secrets  map[string]overrideSecret       `yaml:"secrets,omitempty"`
}

type buildOverrideService struct {
	Build buildOverrideBuild `yaml:"build"`
}

type buildOverrideBuild struct {
	Secrets []overrideServiceSecret `yaml:"secrets"`
}

// buildOverrideData renders build.secrets declarations and the top-level file
// secrets they reference. refs must contain build-phase references only.
func buildOverrideData(refs []Reference, pathFor func(Reference) (string, error)) ([]byte, error) {
	doc := buildOverrideDoc{Services: map[string]buildOverrideService{}}
	secrets := map[string]overrideSecret{}
	for i, ref := range refs {
		path, err := pathFor(ref)
		if err != nil {
			return nil, err
		}
		name := fmt.Sprintf("dcraft_build_secret_%d", i+1)
		secrets[name] = overrideSecret{File: path}
		svc := doc.Services[ref.Service]
		svc.Build.Secrets = append(svc.Build.Secrets, overrideServiceSecret{
			Source: name,
			Target: ref.EffectiveTarget(),
		})
		doc.Services[ref.Service] = svc
	}
	if len(secrets) > 0 {
		doc.Secrets = secrets
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, applyError("dcraft 관리 빌드 secret override를 만들지 못했습니다.", err.Error())
	}
	return append([]byte(buildOverrideHead), data...), nil
}

// BuildOverridePath is the deterministic dcraft-managed build override path
// for a target. It is part of the effective Compose inputs for build
// operations that have build-phase references.
func (r *Runtime) BuildOverridePath(target core.Target) (string, error) {
	if r == nil || r.dirs == nil {
		return "", applyError("빌드 시점 비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return "", applyError("빌드 시점 비밀값 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	return filepath.Join(stackDir, buildOverrideName), nil
}

// BuildMaterializedPath returns the per-stack materialized source path for one
// build reference. Both environment and file sources are materialized as
// private files so Compose build.secrets always reads a regular 0600 file.
func (r *Runtime) BuildMaterializedPath(target core.Target, ref Reference) (string, error) {
	if r == nil || r.dirs == nil {
		return "", applyError("빌드 시점 비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return "", applyError("빌드 시점 비밀값 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	return filepath.Join(stackDir, buildSecretsDir, materializedName(ref)), nil
}

func (r *Runtime) writeBuildOverride(target core.Target, refs []Reference) error {
	path, err := r.BuildOverridePath(target)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return applyError("dcraft 관리 빌드 secret override를 정리하지 못했습니다.", err.Error())
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return applyError("빌드 시점 비밀값 상태 폴더를 만들지 못했습니다.", err.Error())
	}
	data, err := buildOverrideData(refs, func(ref Reference) (string, error) {
		return r.BuildMaterializedPath(target, ref)
	})
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

// writePrivateFile replaces a file atomically and always ends with mode 0600,
// even if an older file had broader permissions.
func writePrivateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dcraft-secret-*.tmp")
	if err != nil {
		return applyError("dcraft 관리 비밀값 파일을 쓰지 못했습니다.", err.Error())
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return applyError("dcraft 관리 비밀값 파일 권한을 설정하지 못했습니다.", err.Error())
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return applyError("dcraft 관리 비밀값 파일을 쓰지 못했습니다.", err.Error())
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return applyError("dcraft 관리 비밀값 파일을 저장하지 못했습니다.", err.Error())
	}
	if err := tmp.Close(); err != nil {
		return applyError("dcraft 관리 비밀값 파일을 닫지 못했습니다.", err.Error())
	}
	if err := os.Rename(tmpName, path); err != nil {
		return applyError("dcraft 관리 비밀값 파일을 적용하지 못했습니다.", err.Error())
	}
	return nil
}

// BuildServiceHint is what the Compose adapter knows about one resolved
// service. HasBuild rejects a build-phase reference for a service Compose
// would never build. Dockerfiles and InlineDockerfiles are only read to warn
// that a mount must be consumed; dcraft never edits them.
type BuildServiceHint struct {
	HasBuild          bool
	Dockerfiles       []string
	InlineDockerfiles []string
}

// BuildSession is one prepared build-secret delivery. Files are the managed
// Compose override inputs to append with -f for the build command. The session
// owns the private materialized sources and knows which of them may be removed
// as soon as the Compose build command exits.
type BuildSession struct {
	Files   []string
	Notices []string
	state   buildState
}

// buildState is the private lifetime state of one build delivery. It never
// stores a fetched value in a place that outlives the Compose build command
// except the private file-source copies needed for a rebuild.
type buildState struct {
	runtime      *Runtime
	target       core.Target
	selected     []string
	overridePath string
	values       []string
	// transientFiles are environment-source and AWS materializations. They
	// live only while one Compose build command runs and are removed
	// afterwards so a fetched value never outlives the build.
	transientFiles []string
	// fileRefs are the file-source references selected for this build. Their
	// private copies stay for a rebuild and are pruned at a safe point.
	fileRefs []Reference
	created  []string
	// overwrote means an existing private copy was rewritten. dcraft never
	// stored the old value, so Abort cannot restore it.
	overwrote     bool
	wroteOverride bool
}

func (s BuildSession) Redact(text string) string { return redactValues(text, s.state.values) }

// HasValues reports whether this preparation fetched at least one value.
func (s BuildSession) HasValues() bool { return len(s.state.values) > 0 }

// PrepareBuild resolves only the build-phase references of the selected
// services, materializes every source as a private file and writes the managed
// build override. An empty services list means the whole stack. Every source
// is resolved before any file is written, so a missing or empty source leaves
// no partial override or materialization.
func (r *Runtime) PrepareBuild(ctx context.Context, target core.Target, services []string, hints map[string]BuildServiceHint) (BuildSession, error) {
	session := BuildSession{state: buildState{
		runtime:  r,
		target:   target,
		selected: append([]string{}, services...),
	}}
	if err := ctx.Err(); err != nil {
		return BuildSession{}, err
	}
	if r == nil || r.dirs == nil {
		return BuildSession{}, applyError("빌드 시점 비밀값 전달을 준비할 수 없습니다.", "state directories are not configured")
	}
	if strings.TrimSpace(target.ProjectDir) == "" {
		return BuildSession{}, applyError("빌드 시점 비밀값 전달 대상 폴더를 확인할 수 없습니다.", "target has no project folder")
	}
	cat, err := Load(target.ProjectDir)
	if err != nil {
		return BuildSession{}, err
	}
	refs := cat.ForPhaseServices(PhaseBuild, services)
	if len(refs) == 0 {
		// The build override is a build-operation-only input. Without selected
		// build references it must not stay in the effective inputs.
		if err := r.writeBuildOverride(target, nil); err != nil {
			return BuildSession{}, err
		}
		return session, nil
	}
	stackDir, err := r.dirs.StackDir(target)
	if err != nil {
		return BuildSession{}, applyError("빌드 시점 비밀값 상태 폴더를 확인할 수 없습니다.", err.Error())
	}
	if err := r.checkStateOutsideProject(target.ProjectDir, stackDir); err != nil {
		return BuildSession{}, err
	}

	// Reject unconsumable references before reading any value.
	if err := validateBuildServices(refs, hints); err != nil {
		return BuildSession{}, err
	}

	// Resolve every source before writing anything.
	fetcher := newAWSFetcher(r.runner)
	items := make([]resolvedItem, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return BuildSession{}, err
		}
		var value []byte
		switch ref.Source {
		case SourceEnv:
			value, err = r.readEnv(ref)
		case SourceFile:
			value, err = r.readSourceFile(target, ref)
		case SourceAWS:
			value, err = fetcher.fetch(ctx, ref)
		default:
			return BuildSession{}, applyError("지원하지 않는 빌드 비밀값 원본입니다.", string(ref.Source))
		}
		if err != nil {
			return BuildSession{}, err
		}
		items = append(items, resolvedItem{ref: ref, value: value})
	}
	for _, item := range items {
		session.state.values = append(session.state.values, string(item.value))
	}

	secretsDir := filepath.Join(stackDir, buildSecretsDir)
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return BuildSession{}, applyError("빌드 시점 비밀값 폴더를 만들지 못했습니다.", err.Error())
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return BuildSession{}, session.abortError(err)
		}
		path, err := r.BuildMaterializedPath(target, item.ref)
		if err != nil {
			return BuildSession{}, session.abortError(err)
		}
		if err := session.materialize(path, item.value); err != nil {
			return BuildSession{}, session.abortError(err)
		}
		switch item.ref.Source {
		case SourceFile:
			session.state.fileRefs = append(session.state.fileRefs, item.ref)
		case SourceEnv, SourceAWS:
			session.state.transientFiles = append(session.state.transientFiles, path)
		}
	}
	if err := r.writeBuildOverride(target, refs); err != nil {
		return BuildSession{}, session.abortError(err)
	}
	session.state.overridePath, err = r.BuildOverridePath(target)
	if err != nil {
		return BuildSession{}, session.abortError(err)
	}
	session.state.wroteOverride = true
	session.Files = []string{session.state.overridePath}

	labels := make([]string, 0, len(items))
	hasEnvSource := false
	hasAWSSource := false
	for _, item := range items {
		labels = append(labels, item.ref.Service+":"+item.ref.EffectiveTarget())
		switch item.ref.Source {
		case SourceEnv:
			hasEnvSource = true
		case SourceAWS:
			hasAWSSource = true
		}
	}
	session.Notices = append(session.Notices, fmt.Sprintf(msgBuildApplied, strings.Join(labels, ", "), session.state.overridePath))
	session.Notices = append(session.Notices, msgBuildCacheNotice, msgBuildNoFallback, msgBuildImageDisclosure)
	if hasEnvSource {
		session.Notices = append(session.Notices, msgBuildEnvLifetime)
	}
	if hasAWSSource {
		session.Notices = append(session.Notices, fmt.Sprintf(msgAWSFetchedBuild, awsSourceCount(items)), msgBuildAWSLifetime, msgAWSCredentialDisclosure)
	}
	session.Notices = append(session.Notices, mountNotices(items, hints)...)
	return session, nil
}

func validateBuildServices(refs []Reference, hints map[string]BuildServiceHint) error {
	var unknown, nonBuild []string
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.Service] {
			continue
		}
		seen[ref.Service] = true
		hint, ok := hints[ref.Service]
		if !ok {
			unknown = append(unknown, ref.Service)
			continue
		}
		if !hint.HasBuild {
			nonBuild = append(nonBuild, ref.Service)
		}
	}
	if len(unknown) > 0 {
		known := make([]string, 0, len(hints))
		for name := range hints {
			known = append(known, name)
		}
		sort.Strings(known)
		sort.Strings(unknown)
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryServiceUnknown,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgUnknownService, strings.Join(unknown, ", ")),
			Detail:      msgAvailableServices + strings.Join(known, ", "),
			NextActions: []string{msgUnknownServiceActionOne, msgUnknownServiceActionTwo},
		})
	}
	if len(nonBuild) > 0 {
		sort.Strings(nonBuild)
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBuildServiceInvalid,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgBuildServiceInvalid, strings.Join(nonBuild, ", ")),
			NextActions: []string{msgBuildServiceInvalidAction},
		})
	}
	return nil
}

func (s *BuildSession) materialize(path string, value []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return applyError("이전 빌드 비밀값 파일을 정리하지 못했습니다.", err.Error())
			}
			s.state.created = append(s.state.created, path)
		} else {
			s.state.overwrote = true
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		s.state.created = append(s.state.created, path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return applyError("빌드 비밀값 전달 파일을 만들지 못했습니다.", err.Error())
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return applyError("빌드 비밀값 전달 파일 권한을 설정하지 못했습니다.", err.Error())
	}
	if _, err := f.Write(value); err != nil {
		_ = f.Close()
		return applyError("빌드 비밀값 전달 파일을 쓰지 못했습니다.", err.Error())
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return applyError("빌드 비밀값 전달 파일을 저장하지 못했습니다.", err.Error())
	}
	if err := f.Close(); err != nil {
		return applyError("빌드 비밀값 전달 파일을 닫지 못했습니다.", err.Error())
	}
	return nil
}

// Finish removes the environment-source and AWS materializations. It is called
// after the Compose build command exits, whether the build succeeded or failed,
// so those values never outlive the build they were fetched for. File-source
// copies stay for a rebuild.
func (s *BuildSession) Finish() error {
	var firstErr error
	for _, path := range s.state.transientFiles {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) && firstErr == nil {
			firstErr = applyError(msgBuildFinishFailed, err.Error())
		}
	}
	s.state.transientFiles = nil
	return firstErr
}

// Commit prunes file-source materializations that no saved reference uses any
// more, limited to the services this build selection could have rebuilt. It is
// called only after a successful build command.
func (s *BuildSession) Commit() {
	if s.state.runtime == nil || s.state.runtime.dirs == nil {
		return
	}
	if len(s.state.transientFiles) > 0 {
		_ = s.Finish()
	}
	stackDir, err := s.state.runtime.dirs.StackDir(s.state.target)
	if err != nil {
		return
	}
	current := map[string]bool{}
	for _, ref := range s.state.fileRefs {
		if path, err := s.state.runtime.BuildMaterializedPath(s.state.target, ref); err == nil {
			current[path] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(stackDir, buildSecretsDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(stackDir, buildSecretsDir, entry.Name())
		if current[path] {
			continue
		}
		if len(s.state.selected) > 0 && !matchesSelected(entry.Name(), s.state.selected) {
			continue
		}
		_ = os.Remove(path)
	}
}

// Abort removes environment-source and AWS materializations and private copies
// created by a preparation that never reached the build command. An
// overwritten private copy is left as-is: dcraft never stored the old value and
// does not pretend to restore it.
func (s *BuildSession) Abort() (partial bool) {
	partial = s.state.overwrote
	for _, path := range append(append([]string{}, s.state.transientFiles...), s.state.created...) {
		_ = os.Remove(path)
	}
	s.state.transientFiles = nil
	s.state.created = nil
	if s.state.wroteOverride && s.state.overridePath != "" {
		_ = os.Remove(s.state.overridePath)
	}
	return partial
}

func (s *BuildSession) abortError(err error) error {
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

// mountNotices warns when a Dockerfile does not consume the configured mount.
// It only reads the files; it never edits them.
func mountNotices(items []resolvedItem, hints map[string]BuildServiceHint) []string {
	checked := map[string]bool{}
	var notices []string
	for _, item := range items {
		ref := item.ref
		key := ref.Service + "\x00" + ref.EffectiveTarget()
		if checked[key] {
			continue
		}
		checked[key] = true
		hint := hints[ref.Service]
		found := ""
		readable := false
		var readErr error
		for _, inline := range hint.InlineDockerfiles {
			readable = true
			if dockerfileReferencesMount(inline, ref.EffectiveTarget()) {
				found = "inline Dockerfile"
				break
			}
		}
		if found != "" {
			continue
		}
		for _, path := range hint.Dockerfiles {
			data, err := os.ReadFile(path)
			if err != nil {
				readErr = err
				continue
			}
			readable = true
			if dockerfileReferencesMount(string(data), ref.EffectiveTarget()) {
				found = path
				break
			}
		}
		if found != "" {
			continue
		}
		location := "dockerfile_inline"
		if len(hint.Dockerfiles) > 0 {
			location = hint.Dockerfiles[0]
		}
		switch {
		case len(hint.Dockerfiles) == 0 && len(hint.InlineDockerfiles) == 0:
			notices = append(notices, fmt.Sprintf(msgBuildDockerfileUnknown, ref.Service, ref.EffectiveTarget()))
		case !readable:
			notices = append(notices, fmt.Sprintf(msgBuildDockerfileUnreadable, ref.Service, location, readErr, ref.EffectiveTarget()))
		default:
			notices = append(notices, fmt.Sprintf(msgBuildMountMissing, ref.Service, location, ref.EffectiveTarget(), ref.EffectiveTarget()))
		}
	}
	return notices
}

// dockerfileReferencesMount reports whether a Dockerfile body consumes the
// BuildKit secret id. Continuation lines are joined so a --mount split over
// several physical lines is still recognized.
func dockerfileReferencesMount(content, id string) bool {
	var joined strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasSuffix(trimmed, "\\") {
			joined.WriteString(strings.TrimSuffix(trimmed, "\\"))
			joined.WriteString(" ")
			continue
		}
		joined.WriteString(trimmed)
		joined.WriteString("\n")
	}
	for _, line := range strings.Split(joined.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "RUN") {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "--mount=") {
				continue
			}
			isSecret, idMatches := false, false
			for _, part := range strings.Split(strings.TrimPrefix(field, "--mount="), ",") {
				part = strings.TrimSpace(part)
				switch {
				case part == "type=secret":
					isSecret = true
				case part == "id="+id:
					idMatches = true
				}
			}
			if isSecret && idMatches {
				return true
			}
		}
	}
	return false
}
