package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

// ProjectNameReader is the published read-only effective-name lookup.
type ProjectNameReader interface {
	ReadProjectName(ctx context.Context, target core.Target) (string, error)
}

// Config are the shared services this feature consumes. Backend must implement
// the volume restore capability; the same Controller is used by CLI and TUI.
type Config struct {
	Backend data.VolumeRestoreBackend
	Names   ProjectNameReader
	Locks   state.Locker
	Clock   core.Clock
	Tool    backup.Tool
	PID     int
	Space   SpaceChecker
}

// Controller owns the volume restore command and the shared destination
// selection used by the wizard and plan.
type Controller struct {
	cfg  Config
	exec *Executor

	mu   sync.Mutex
	last reportHolder

	wizardMu    sync.Mutex
	wizardCache *wizardCache
}

// New builds the controller.
func New(cfg Config) *Controller {
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.PID == 0 {
		cfg.PID = os.Getpid()
	}
	return &Controller{
		cfg: cfg,
		exec: &Executor{
			Backend: cfg.Backend, Locks: cfg.Locks, Clock: cfg.Clock,
			Tool: cfg.Tool, PID: cfg.PID, Space: cfg.Space,
		},
	}
}

// Report is the machine-readable data under data.restore.
type Report struct {
	ExecutionReport
	SafetyManifestPath string `json:"safety_manifest_path,omitempty"`
}

// Payload is the feature payload.
type Payload struct {
	Restore Report `json:"restore"`
}

// LastReport returns the most recent execution report. It is used for the
// honest cancellation diagnostic.
func (c *Controller) LastReport() ExecutionReport { return c.last.get() }

// CanceledDiagnostic implements core.CanceledReporter.
func (c *Controller) CanceledDiagnostic() core.Diagnostic {
	rep := c.last.get()
	stage := rep.Stage
	if stage == "" {
		stage = "validate_artifact"
	}
	state := string(rep.DataState)
	switch rep.DataState {
	case DataUntouched:
		if rep.SafetyBackup != "" {
			return core.Diagnostic{
				Message:     fmt.Sprintf(msgCanceledUntouched, fmt.Sprintf(msgStageLabel, stage), fmt.Sprintf(msgDataStateLabel, state)),
				NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
			}
		}
		return core.Diagnostic{
			Message:     fmt.Sprintf(msgCanceledNoSafety, fmt.Sprintf(msgStageLabel, stage), fmt.Sprintf(msgDataStateLabel, state)),
			NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
		}
	case DataReplaced:
		return core.Diagnostic{
			Message:     fmt.Sprintf(msgCanceledReplaced, fmt.Sprintf(msgStageLabel, stage), fmt.Sprintf(msgDataStateLabel, state), rep.SafetyBackup),
			NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
		}
	default:
		if rep.SafetyBackup != "" {
			return core.Diagnostic{
				Message:     fmt.Sprintf(msgCanceledWithSafety, fmt.Sprintf(msgStageLabel, stage), fmt.Sprintf(msgDataStateLabel, state), rep.SafetyBackup),
				NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
			}
		}
		return core.Diagnostic{
			Message:     fmt.Sprintf(msgCanceledNoSafety, fmt.Sprintf(msgStageLabel, stage), fmt.Sprintf(msgDataStateLabel, state)),
			NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
		}
	}
}

// Run implements the restore command.
func (c *Controller) Run(ctx context.Context, in core.Invocation) (any, error) {
	if c.cfg.Backend == nil || c.cfg.Names == nil || c.cfg.Locks == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "현재 작업 폴더를 확인하지 못했습니다.",
			NextActions: []string{"dcraft를 실행한 폴더를 확인한 뒤 다시 실행하세요."},
		})
	}

	var artifact Artifact
	err = core.RunStage(in.Reporter, "validate_artifact", msgStageValidate, func() error {
		a, verifyErr := ValidateArtifact(in.Option("backup"), cwd)
		if verifyErr != nil {
			return artifactError(verifyErr)
		}
		artifact = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.last.set(ExecutionReport{
		ArtifactPath: artifact.Path,
		DataState:    DataUntouched,
		Stage:        "validate_artifact",
		Warnings:     []string{},
		Notices:      []string{},
	})

	var lock *state.StackLock
	err = core.RunStage(in.Reporter, "coordinate", msgStageCoordinate, func() error {
		acquired, lockErr := c.cfg.Locks.AcquireStackLock(ctx, in.Target, state.LockHolder{
			Action: "restore", PID: c.cfg.PID, StartedAt: c.cfg.Clock.Now(),
		})
		if lockErr == nil {
			lock = acquired
			return nil
		}
		var busy *state.BusyError
		if errors.As(lockErr, &busy) {
			return busyError(busy)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return core.FailCause(lockErr, stateError(lockErr))
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()

	target := in.Target
	var projectName string
	err = core.RunStage(in.Reporter, "resolve_project", msgStageResolve, func() error {
		name, nameErr := c.effectiveName(ctx, target)
		if nameErr != nil {
			return nameErr
		}
		projectName = name
		target.ProjectName = name
		return nil
	})
	if err != nil {
		return nil, err
	}

	c.last.set(ExecutionReport{
		ProjectName:  projectName,
		ArtifactPath: artifact.Path,
		DataState:    DataUntouched,
		Stage:        "select_target",
		Warnings:     []string{},
		Notices:      []string{},
	})
	selection, err := c.selectTarget(ctx, in, target, projectName, artifact, cwd, true)
	if err != nil {
		return nil, err
	}

	err = core.RunStage(in.Reporter, "preview", msgStagePreview, func() error {
		return nil
	})
	if err != nil {
		return nil, err
	}

	report, execErr := c.exec.Execute(ctx, in, ExecutionRequest{
		Target: target, ProjectName: projectName, Artifact: artifact,
		Destination: selection.dest, SafetyDir: selection.safetyDir,
		Affected: selection.discovery.AffectedServices([]data.DataSource{selection.source}),
		Running:  selection.discovery.RunningAmong(selection.discovery.AffectedServices([]data.DataSource{selection.source})),
		Explicit: selection.explicit,
		Warnings: selection.warnings,
		Locked:   true,
	})
	c.last.set(report)
	if execErr != nil {
		return nil, execErr
	}
	out := Report{ExecutionReport: report}
	if report.SafetyBackup != "" {
		out.SafetyManifestPath = filepath.Join(report.SafetyBackup, backup.ManifestFile)
	}
	return Payload{Restore: out}, nil
}

// Plan is the read-only preview. It verifies the artifact and mapping but takes
// no lock, stops nothing and creates no safety directory.
func (c *Controller) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	if c.cfg.Backend == nil || c.cfg.Names == nil {
		return core.Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	cwd, err := os.Getwd()
	if err != nil {
		return core.Plan{}, err
	}
	artifact, err := ValidateArtifact(in.Option("backup"), cwd)
	if err != nil {
		return core.Plan{}, artifactError(err)
	}
	target := in.Target
	name, err := c.effectiveName(ctx, target)
	if err != nil {
		return core.Plan{}, err
	}
	target.ProjectName = name
	selection, err := c.selectTarget(ctx, in, target, name, artifact, cwd, false)
	if err != nil {
		return core.Plan{}, err
	}
	text := buildPreview(name, artifact, selection, c.cfg.Clock.Now())
	return core.Plan{Text: text}, nil
}

type targetSelection struct {
	discovery backup.Discovery
	source    data.DataSource
	dest      *VolumeDestination
	safetyDir string
	explicit  bool
	warnings  []string
	current   ContentSummary
}

func (c *Controller) selectTarget(ctx context.Context, in core.Invocation, target core.Target, projectName string, artifact Artifact, cwd string, create bool) (targetSelection, error) {
	cwdErr := ctx.Err()
	if cwdErr != nil {
		return targetSelection{}, cwdErr
	}
	var sel targetSelection
	err := core.RunStage(in.Reporter, "select_target", msgStageSelect, func() error {
		if artifact.Manifest.Source.Kind != data.KindVolume {
			return targetError(fmt.Errorf("%w: %s artifact", ErrTargetUnsupported, artifact.Manifest.Source.Kind))
		}
		discovery, err := backup.Discover(ctx, c.cfg.Backend, target, projectName)
		if err != nil {
			return err
		}
		source, err := selectVolumeSource(discovery.Sources, in.Option("volume"), artifact.Manifest)
		if err != nil {
			return err
		}
		explicit := in.BoolOption("allow-source-mismatch")
		dest := NewVolumeDestination(c.cfg.Backend, source, target, projectName)
		warnings, err := dest.Match(artifact.Manifest, explicit)
		if err != nil {
			return targetError(err)
		}
		safetyDir, err := resolveSafetyDir(in.Option("safety-output"), projectName, artifact.Path, cwd, create)
		if err != nil {
			return safetyLocationError(err)
		}
		if err := ValidateLocation(dest, artifact.Path, safetyDir); err != nil {
			return safetyLocationError(err)
		}
		if err := dest.Prepare(ctx); err != nil {
			return targetError(err)
		}
		unsafe, err := dest.UnsafeConsumers(ctx)
		if err != nil {
			return err
		}
		if len(unsafe) > 0 {
			return outsideConsumerError(unsafe)
		}
		runtime, err := c.cfg.Backend.ServiceRuntime(ctx, target)
		if err != nil {
			return err
		}
		affected := discovery.AffectedServices([]data.DataSource{source})
		if paused := pausedServices(runtime, affected); len(paused) > 0 {
			return pausedServiceError(paused)
		}
		usage, usageErr := dest.Usage(ctx)
		if usageErr != nil {
			sel.warnings = append(sel.warnings, fmt.Sprintf("현재 데이터 크기를 확인하지 못했습니다: %s", usageErr.Error()))
		} else {
			sel.current = usage
		}
		sel.discovery = discovery
		sel.source = source
		sel.dest = dest
		sel.safetyDir = safetyDir
		sel.explicit = explicit
		sel.warnings = append(sel.warnings, warnings...)
		if err := c.checkSpace(ctx, sel.current, safetyDir); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return targetSelection{}, err
	}
	return sel, nil
}

func (c *Controller) checkSpace(ctx context.Context, current ContentSummary, safetyDir string) error {
	checker := c.cfg.Space
	if checker == nil {
		checker = DiskSpace{}
	}
	available, err := checker.AvailableBytes(safetyDir)
	if err != nil {
		return nil
	}
	if needed := requiredSpace(current); available < needed {
		return spaceError(needed, available)
	}
	return nil
}

// selectVolumeSource resolves --volume, or the artifact's recorded volume when
// the flag is absent. An explicit unknown key is caller input (exit 2); a
// recorded key missing from this stack is a target mismatch (exit 1).
func selectVolumeSource(sources []data.DataSource, requested string, manifest backup.Manifest) (data.DataSource, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		selected, err := backup.SelectVolumeSources(sources, []string{requested})
		if err != nil {
			return data.DataSource{}, volumeSelectionError(err)
		}
		return selected[0], nil
	}
	for _, src := range backup.SelectableVolumes(sources) {
		if src.Key == manifest.Source.VolumeKey || src.Name == manifest.Source.VolumeName {
			return src, nil
		}
	}
	return data.DataSource{}, core.Fail(core.Diagnostic{
		Category:    core.CategoryRestoreTargetMismatch,
		Severity:    core.SeverityError,
		Message:     msgTargetMismatch,
		Detail:      fmt.Sprintf("백업 볼륨 %q(%s)을 현재 스택에서 찾지 못했습니다. --volume으로 대상을 명시할 수 있습니다.", manifest.Source.VolumeKey, manifest.Source.VolumeName),
		NextActions: []string{msgTargetMismatchAction},
	})
}

func volumeSelectionError(err error) error {
	var unknown *backup.UnknownVolumeError
	var unsupported *backup.UnsupportedSourceError
	var invalid *backup.InvalidVolumeNameError
	switch {
	case errors.As(err, &unknown):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryRestoreVolumeUnknown,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnknown,
			Detail:      unknown.Error(),
			NextActions: []string{msgVolumeUnknownAction},
		})
	case errors.As(err, &unsupported), errors.As(err, &invalid):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryRestoreTargetUnsupported,
			Severity:    core.SeverityError,
			Message:     msgTargetUnsupported,
			Detail:      err.Error(),
			NextActions: []string{msgTargetUnsupportedAction},
		})
	default:
		return err
	}
}

func (c *Controller) effectiveName(ctx context.Context, target core.Target) (string, error) {
	name, err := c.cfg.Names.ReadProjectName(ctx, target)
	if err != nil {
		return "", core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryRestoreFailed,
			Severity:    core.SeverityError,
			Message:     msgIdentityFailed,
			Detail:      err.Error(),
			NextActions: []string{msgIdentityFailedAction},
		})
	}
	name = strings.TrimSpace(name)
	if name != "" {
		return name, nil
	}
	name, err = c.cfg.Backend.ProjectName(ctx, target)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryRestoreFailed,
			Severity:    core.SeverityError,
			Message:     msgIdentityFailed,
			NextActions: []string{msgIdentityFailedAction},
		})
	}
	return name, nil
}

func artifactError(err error) error {
	switch {
	case errors.Is(err, ErrBackupRequired):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryRestoreBackupRequired,
			Severity:    core.SeverityError,
			Message:     msgBackupRequired,
			NextActions: []string{msgBackupRequiredAction, msgBackupUsageAction},
		})
	case errors.Is(err, backup.ErrIncomplete):
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryRestoreIntegrityFailed,
			Severity:    core.SeverityError,
			Message:     msgIntegrityFailed,
			Detail:      err.Error(),
			NextActions: []string{msgIntegrityFailedAction},
		})
	case errors.Is(err, backup.ErrUnsupported), errors.Is(err, backup.ErrIntegrity), errors.Is(err, backup.ErrUnsafePath), errors.Is(err, ErrUnsafeLink), errors.Is(err, ErrArtifactInvalid):
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryRestoreIntegrityFailed,
			Severity:    core.SeverityError,
			Message:     msgIntegrityFailed,
			Detail:      err.Error(),
			NextActions: []string{msgIntegrityFailedAction},
		})
	case errors.Is(err, ErrTargetUnsupported):
		return targetError(err)
	default:
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryRestoreArtifactInvalid,
			Severity:    core.SeverityError,
			Message:     msgArtifactInvalid,
			Detail:      err.Error(),
			NextActions: []string{msgArtifactInvalidAction},
		})
	}
}

func safetyLocationError(err error) error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreSafetyFailed,
		Severity:    core.SeverityError,
		Message:     msgSafetyDirInvalid,
		Detail:      err.Error(),
		NextActions: []string{msgSafetyDirInvalidAction},
	})
}

func resolveSafetyDir(raw, project, artifactPath, cwd string, create bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return filepath.Join(filepath.Dir(artifactPath), "dcraft-safety"), nil
	}
	abs, err := expandPath(raw, cwd)
	if err != nil {
		return "", err
	}
	if create {
		return backup.ValidateOutputDir(abs, cwd)
	}
	if info, statErr := os.Stat(abs); statErr == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("%w: %s is not a directory", ErrSafetyInvalid, abs)
		}
		resolved, evalErr := filepath.EvalSymlinks(abs)
		if evalErr != nil {
			return "", evalErr
		}
		return resolved, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	return resolvePath(abs), nil
}

func buildPreview(project string, artifact Artifact, sel targetSelection, now time.Time) string {
	var b strings.Builder
	b.WriteString(msgPreviewHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, msgPreviewProject+"\n", project)
	fmt.Fprintf(&b, msgPreviewArtifact+"\n", artifact.Path, artifact.Manifest.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, msgPreviewBackup+"\n", artifact.Manifest.Totals.Entries, humanBytes(artifact.Manifest.Totals.Bytes))
	fmt.Fprintf(&b, msgPreviewTarget+"\n", sel.source.Key, sel.source.Name)
	if sel.current.Entries > 0 || sel.current.Bytes > 0 {
		fmt.Fprintf(&b, msgPreviewCurrent+"\n", sel.current.Entries, humanBytes(sel.current.Bytes))
	} else {
		b.WriteString(msgPreviewCurrentNone)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, msgPreviewSafety+"\n", sel.safetyDir)
	affected := sel.discovery.AffectedServices([]data.DataSource{sel.source})
	running := sel.discovery.RunningAmong(affected)
	fmt.Fprintf(&b, msgPreviewServices+"\n", joinOrNone(affected))
	fmt.Fprintf(&b, msgPreviewRunning+"\n", joinOrNone(running))
	fmt.Fprintf(&b, msgPreviewStopped+"\n", joinOrNone(difference(affected, running)))
	if sel.explicit {
		b.WriteString(msgPreviewExplicit)
		b.WriteString("\n")
	}
	if len(sel.warnings) > 0 {
		b.WriteString(msgPreviewWarnings)
		b.WriteString("\n")
		for _, warning := range sel.warnings {
			fmt.Fprintf(&b, "  - %s\n", warning)
		}
	}
	b.WriteString(msgPreviewSteps)
	b.WriteString("\n")
	b.WriteString(msgPreviewLock)
	b.WriteString("\n")
	b.WriteString(msgOverwrite)
	b.WriteString("\n")
	b.WriteString(msgInterrupt)
	b.WriteString("\n")
	b.WriteString(msgSafetyNotice)
	b.WriteString("\n")
	b.WriteString(msgOwnership)
	return strings.TrimRight(b.String(), "\n")
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return msgPreviewNone
	}
	return strings.Join(values, ", ")
}

func difference(all, remove []string) []string {
	drop := map[string]bool{}
	for _, v := range remove {
		drop[v] = true
	}
	var out []string
	for _, v := range all {
		if !drop[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// WizardSteps lists complete artifacts found in the current folder and the
// conventional ./dcraft-backups folder, plus the current stack's volume keys.
// It changes nothing.
func (c *Controller) WizardSteps(ctx context.Context, in core.Invocation) (core.WizardSpec, error) {
	if c.cfg.Backend == nil || c.cfg.Names == nil {
		return core.WizardSpec{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	cwd, err := os.Getwd()
	if err != nil {
		return core.WizardSpec{}, err
	}
	target := in.Target
	name, err := c.effectiveName(ctx, target)
	if err != nil {
		return core.WizardSpec{}, err
	}
	target.ProjectName = name
	discovery, err := backup.Discover(ctx, c.cfg.Backend, target, name)
	if err != nil {
		return core.WizardSpec{}, err
	}
	selectable := backup.SelectableVolumes(discovery.Sources)
	if len(selectable) == 0 {
		return core.WizardSpec{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryRestoreVolumeUnknown,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnknown,
			Detail:      "현재 스택에 이름 있는 데이터 볼륨이 없습니다.",
			NextActions: []string{msgVolumeUnknownAction},
		})
	}

	artifactStep := core.WizardStep{
		Name: "backup", Title: msgWizardArtifact, Prompt: msgWizardArtifactWhy,
	}
	artifacts, _ := discoverArtifacts(cwd)
	if len(artifacts) == 0 {
		artifactStep.AllowCustom = true
		artifactStep.CustomHint = "예: ./dcraft-backups/dcraft-proj-volume-data-20260101T000000Z"
		artifactStep.Default = msgWizardArtifactDef
	} else {
		for _, art := range artifacts {
			artifactStep.Options = append(artifactStep.Options, core.WizardOption{
				Value: art.Path,
				Label: fmt.Sprintf("%s · %s → %s · %s", filepath.Base(art.Path), art.Manifest.Source.VolumeKey, art.Manifest.Source.VolumeName, art.Manifest.CreatedAt.Local().Format("2006-01-02 15:04")),
			})
		}
	}

	volumeStep := core.WizardStep{
		Name: "volume", Title: msgWizardVolume, Prompt: msgWizardVolumeWhy,
		Options: []core.WizardOption{{Value: "", Label: msgWizardVolumeAuto}},
	}
	for _, src := range selectable {
		volumeStep.Options = append(volumeStep.Options, core.WizardOption{
			Value: src.Key,
			Label: fmt.Sprintf("%s → %s", src.Key, src.Name),
		})
	}
	c.cacheWizard(wizardCache{projectName: name, target: target, discovery: discovery})
	return core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			artifactStep,
			volumeStep,
			{
				Name: "safety", Title: msgWizardSafety, Prompt: msgWizardSafetyWhy,
				AllowCustom: true, CustomHint: msgWizardRecheck, Default: msgWizardSafetyDef,
			},
		},
	}, nil
}

// WizardPreview describes the effect of the collected answers without creating
// anything or stopping a service. Run re-resolves everything.
func (c *Controller) WizardPreview(answers map[string]string) (string, error) {
	cache := c.cachedWizard()
	if cache == nil {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "복원 설정을 다시 불러오지 못했습니다.",
			NextActions: []string{"복원 화면을 다시 열어 주세요."},
		})
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	artifact, err := ValidateArtifact(answers["backup"], cwd)
	if err != nil {
		return "", artifactError(err)
	}
	source, err := selectVolumeSource(cache.discovery.Sources, answers["volume"], artifact.Manifest)
	if err != nil {
		return "", err
	}
	explicit := false
	dest := NewVolumeDestination(c.cfg.Backend, source, cache.target, cache.projectName)
	warnings, err := dest.Match(artifact.Manifest, explicit)
	if err != nil {
		return "", targetError(err)
	}
	safetyDir, err := resolveSafetyDir(answers["safety"], cache.projectName, artifact.Path, cwd, false)
	if err != nil {
		return "", safetyLocationError(err)
	}
	if err := ValidateLocation(dest, artifact.Path, safetyDir); err != nil {
		return "", safetyLocationError(err)
	}
	sel := targetSelection{
		discovery: cache.discovery, source: source, dest: dest, safetyDir: safetyDir,
		explicit: explicit, warnings: warnings,
	}
	return buildPreview(cache.projectName, artifact, sel, c.cfg.Clock.Now()), nil
}

type wizardCache struct {
	projectName string
	target      core.Target
	discovery   backup.Discovery
}

func (c *Controller) cacheWizard(cache wizardCache) {
	c.wizardMu.Lock()
	defer c.wizardMu.Unlock()
	c.wizardCache = &cache
}

func (c *Controller) cachedWizard() *wizardCache {
	c.wizardMu.Lock()
	defer c.wizardMu.Unlock()
	return c.wizardCache
}

// discoverArtifacts scans the current folder and the conventional backup
// folder. Only complete, supported artifacts are offered.
func discoverArtifacts(cwd string) ([]backup.Artifact, error) {
	var all []backup.Artifact
	seen := map[string]bool{}
	for _, root := range []string{cwd, filepath.Join(cwd, "dcraft-backups")} {
		artifacts, err := backup.List(root)
		if err != nil {
			continue
		}
		for _, art := range artifacts {
			if art.Manifest.Source.Kind != data.KindVolume || seen[art.Path] {
				continue
			}
			seen[art.Path] = true
			all = append(all, art)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Manifest.CreatedAt.After(all[j].Manifest.CreatedAt) })
	if len(all) > 20 {
		all = all[:20]
	}
	return all, nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
