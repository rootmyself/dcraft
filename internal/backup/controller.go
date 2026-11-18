package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

// ProjectNameReader is the published effective-name lookup (internal/identity
// implements it). The backup feature is a mutating operation, so it resolves
// the same name read-only instead of allocating a new one.
type ProjectNameReader interface {
	ReadProjectName(ctx context.Context, target core.Target) (string, error)
}

// Config are the shared services this feature consumes.
type Config struct {
	Backend data.Backend
	Names   ProjectNameReader
	Store   *state.Store
	Clock   core.Clock
	Tool    Tool
	PID     int
}

// Controller owns the backup operation and the shared discovery used by plan,
// interactive selection and later tickets.
type Controller struct {
	cfg Config
}

// New builds the controller. A nil clock uses the system clock; a zero PID is
// replaced by the process id.
func New(cfg Config) *Controller {
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.PID == 0 {
		cfg.PID = os.Getpid()
	}
	return &Controller{cfg: cfg}
}

// Report is the machine-readable data under data.backup.
type Report struct {
	ProjectName      string           `json:"project_name"`
	OutputDir        string           `json:"output_dir"`
	Sources          []Source         `json:"sources"`
	AffectedServices []string         `json:"affected_services"`
	RunningBefore    []string         `json:"running_before"`
	Stopped          []string         `json:"stopped"`
	Restarted        []string         `json:"restarted"`
	Artifacts        []ArtifactReport `json:"artifacts"`
	SensitiveData    bool             `json:"sensitive_data"`
	Notices          []string         `json:"notices"`
}

// Payload is the feature payload. JSON consumers read data.backup.
type Payload struct {
	Backup Report `json:"backup"`
}

// ArtifactReport describes one published backup.
type ArtifactReport struct {
	Source            Source `json:"source"`
	Path              string `json:"path"`
	ManifestPath      string `json:"manifest_path"`
	SizeBytes         int64  `json:"size_bytes"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes"`
	SHA256            string `json:"sha256"`
	Entries           int    `json:"entries"`
	Complete          bool   `json:"complete"`
}

// Run implements the backup command. It validates the output location, takes
// the stack lock, identifies the stack, refuses unsafe consumers, stops only
// the running services that consume the selected volumes, archives each
// volume through the root helper, extracts and verifies the archive, writes
// the completion manifest last, publishes atomically, and then starts only the
// services it stopped. No part of this is bypassed on failure.
func (c *Controller) Run(ctx context.Context, in core.Invocation) (payload any, err error) {
	if c.cfg.Backend == nil || c.cfg.Names == nil || c.cfg.Store == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	clock := c.cfg.Clock
	if in.Clock != nil {
		clock = in.Clock
	}

	// 1. Validate the user-chosen output directory before anything is stopped.
	var outputDir string
	err = core.RunStage(in.Reporter, "output", msgStageOutput, func() error {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return outputError(cwdErr)
		}
		dir, dirErr := ValidateOutputDir(in.Option("output"), cwd)
		if dirErr != nil {
			return outputError(dirErr)
		}
		outputDir = dir
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 2. Coordinate with the same per-stack lock used by up/stop/down/restart.
	var lock *state.StackLock
	err = core.RunStage(in.Reporter, "coordinate", msgStageCoordinate, func() error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		acquired, lockErr := c.cfg.Store.AcquireStackLock(ctx, in.Target, state.LockHolder{
			Action: "backup", PID: c.cfg.PID, StartedAt: clock.Now(),
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

	// 3. Identify the same stack status/lifecycle use and pin it on every call.
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

	// 4. Discover data sources, observed states and the user's selection.
	var discovery Discovery
	var selected []data.DataSource
	err = core.RunStage(in.Reporter, "discover", msgStageDiscover, func() error {
		d, dErr := Discover(ctx, c.cfg.Backend, target, projectName)
		if dErr != nil {
			return dErr
		}
		discovery = d
		sources, sErr := d.Select(in.OptionList("volume"))
		if sErr != nil {
			return selectionError(sErr, d.Sources)
		}
		selected = sources
		return nil
	})
	if err != nil {
		return nil, err
	}

	affected := discovery.AffectedServices(selected)
	running := discovery.RunningAmong(affected)

	// 5. Refuse anything that cannot be stopped or restored safely.
	err = core.RunStage(in.Reporter, "inspect_consumers", msgStageConsumers, func() error {
		report, cErr := discovery.CheckConsumers(ctx, c.cfg.Backend, selected)
		if cErr != nil {
			return cErr
		}
		if len(report.Outside) > 0 {
			return outsideConsumerError(report.Outside)
		}
		if paused := discovery.ServicesInState(affected, "paused"); len(paused) > 0 {
			return pausedServiceError(paused)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	stopped := []string{}
	restarted := []string{}
	artifacts := []ArtifactReport{}
	restored := false
	restore := func() error {
		if restored || len(stopped) == 0 {
			restored = true
			return nil
		}
		restored = true
		return c.restoreState(ctx, in, target, stopped, artifacts, &restarted)
	}
	// Every failure path returns through this defer, so a panic or an early
	// error still restores the services this operation stopped.
	defer func() {
		if restored || len(stopped) == 0 {
			return
		}
		if restoreErr := restore(); restoreErr != nil {
			err = mergeErrors(err, restoreErr)
		}
	}()

	// 6. Stop only the affected services that were running.
	if len(running) > 0 {
		stopped = append(stopped, running...)
		err = core.RunStage(in.Reporter, "stop_consumers", msgStageStop, func() error {
			res, stopErr := c.cfg.Backend.StopServices(ctx, target, running)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if stopErr != nil {
				return stopError(stopErr, res.Output)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// 7. Archive each selected volume. One failure stops the run; earlier
	// published artifacts are kept and named in the failure.
	for _, src := range selected {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		artifact, archiveErr := c.archiveOne(ctx, in, target, outputDir, src, projectName, affected, running, stopped, clock)
		if archiveErr != nil {
			return nil, archiveErr
		}
		artifacts = append(artifacts, artifact)
	}

	// 8. Restore only the services this operation stopped.
	if restoreErr := restore(); restoreErr != nil {
		return nil, restoreErr
	}

	sources := make([]Source, 0, len(selected))
	for _, src := range selected {
		sources = append(sources, SourceFrom(src, projectName, target.Environment, target.ProjectDir))
	}
	notices := []string{
		fmt.Sprintf(msgHelperImageNotice, c.cfg.Backend.HelperImage()),
		msgSensitiveNotice,
		msgOwnershipNote,
		msgExcludedNotice,
		msgNoAutoDeleteNote,
	}
	if len(stopped) == 0 {
		notices = append(notices, msgStoppedOnlyOurs)
	}
	return Payload{Backup: Report{
		ProjectName:      projectName,
		OutputDir:        outputDir,
		Sources:          sources,
		AffectedServices: affected,
		RunningBefore:    running,
		Stopped:          stopped,
		Restarted:        restarted,
		Artifacts:        artifacts,
		SensitiveData:    true,
		Notices:          notices,
	}}, nil
}

// Plan implements core.Planner. It performs the same read-only discovery and
// refuses the same unsafe consumers, but creates no directory, takes no lock
// and changes nothing.
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
		return core.Plan{}, outputError(err)
	}
	outputDir, exists, err := resolveOutputDir(in.Option("output"), cwd)
	if err != nil {
		return core.Plan{}, outputError(err)
	}

	target := in.Target
	name, err := c.effectiveName(ctx, target)
	if err != nil {
		return core.Plan{}, err
	}
	target.ProjectName = name

	discovery, err := Discover(ctx, c.cfg.Backend, target, name)
	if err != nil {
		return core.Plan{}, err
	}
	selected, err := discovery.Select(in.OptionList("volume"))
	if err != nil {
		return core.Plan{}, selectionError(err, discovery.Sources)
	}
	report, err := discovery.CheckConsumers(ctx, c.cfg.Backend, selected)
	if err != nil {
		return core.Plan{}, err
	}
	if len(report.Outside) > 0 {
		return core.Plan{}, outsideConsumerError(report.Outside)
	}
	affected := discovery.AffectedServices(selected)
	if paused := discovery.ServicesInState(affected, "paused"); len(paused) > 0 {
		return core.Plan{}, pausedServiceError(paused)
	}
	running := discovery.RunningAmong(affected)
	stoppedAlready := difference(affected, running)

	var b strings.Builder
	b.WriteString(msgPlanHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, msgPlanProject+"\n", name)
	fmt.Fprintf(&b, msgPlanOutput+"\n", outputDir)
	b.WriteString(msgPlanFormat)
	b.WriteString("\n\n")
	b.WriteString(msgPlanSources)
	b.WriteString("\n")
	for _, src := range selected {
		fmt.Fprintf(&b, "  - %s\n", DescribeSource(src))
	}
	fmt.Fprintf(&b, msgPlanServices+"\n", joinOrNone(affected))
	fmt.Fprintf(&b, msgPlanRunning+"\n", joinOrNone(running))
	fmt.Fprintf(&b, msgPlanStopped+"\n", joinOrNone(stoppedAlready))
	fmt.Fprintf(&b, msgPlanHelper+"\n", c.cfg.Backend.HelperImage())
	b.WriteString(msgPlanStopStep)
	b.WriteString("\n")
	b.WriteString(msgPlanLock)
	b.WriteString("\n")
	if exists {
		b.WriteString(msgPlanOutputExists)
	} else {
		b.WriteString(msgPlanCreateDir)
	}
	b.WriteString("\n")
	b.WriteString(msgPlanSensitive)
	b.WriteString("\n")
	b.WriteString(msgPlanNoAutoDelete)
	return core.Plan{Text: b.String()}, nil
}

// Choices implements core.OptionChooser for the volume flag. It is read-only
// and returns only named volumes; anonymous and bind sources are not offered.
func (c *Controller) Choices(ctx context.Context, in core.Invocation, flag string) ([]core.OptionChoice, error) {
	if flag != "volume" {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnknown,
			NextActions: []string{msgVolumeUnknownAction},
		})
	}
	if c.cfg.Backend == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	target := in.Target
	if c.cfg.Names != nil {
		name, err := c.cfg.Names.ReadProjectName(ctx, target)
		if err != nil {
			return nil, identityError(err)
		}
		if name != "" {
			target.ProjectName = name
		}
	}
	sources, err := c.cfg.Backend.Sources(ctx, target)
	if err != nil {
		return nil, err
	}
	out := []core.OptionChoice{}
	for _, src := range SelectableVolumes(sources) {
		out = append(out, core.OptionChoice{
			Value:  src.Key,
			Label:  fmt.Sprintf("%s → %s", src.Key, src.Name),
			Detail: sourceDetail(src),
		})
	}
	return out, nil
}

func sourceDetail(src data.DataSource) string {
	parts := []string{}
	if len(src.Services) > 0 {
		parts = append(parts, "서비스 "+strings.Join(src.Services, ", "))
	}
	if len(src.MountTargets) > 0 {
		parts = append(parts, "마운트 "+strings.Join(src.MountTargets, ", "))
	}
	if src.External {
		parts = append(parts, "external 볼륨")
	}
	return strings.Join(parts, " · ")
}

func (c *Controller) effectiveName(ctx context.Context, target core.Target) (string, error) {
	name, err := c.cfg.Names.ReadProjectName(ctx, target)
	if err != nil {
		return "", identityError(err)
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
			Category:    core.CategoryBackupFailed,
			Severity:    core.SeverityError,
			Message:     msgIdentityFailed,
			NextActions: []string{msgIdentityFailedAction},
		})
	}
	return name, nil
}

func (c *Controller) archiveOne(ctx context.Context, in core.Invocation, target core.Target, outputDir string, src data.DataSource, projectName string, affected, running, stopped []string, clock core.Clock) (ArtifactReport, error) {
	base := artifactName(SourceFrom(src, projectName, target.Environment, target.ProjectDir), clock.Now())
	final, err := uniqueArtifactPath(outputDir, base)
	if err != nil {
		return ArtifactReport{}, outputError(err)
	}
	partial := final + PartialSuffix
	if err := os.MkdirAll(partial, 0o700); err != nil {
		return ArtifactReport{}, archiveFailure("", err, partial)
	}
	cleanup := func() string {
		if removeErr := os.RemoveAll(partial); removeErr != nil {
			return fmt.Sprintf(msgPartialLocation+" ("+msgPartialKept+")", partial)
		}
		return msgPartialCleaned
	}

	err = core.RunStage(in.Reporter, "archive", fmt.Sprintf("%s: %s", msgStageArchive, src.Key), func() error {
		res, archiveErr := c.cfg.Backend.ArchiveVolume(ctx, src.Name, partial)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if archiveErr != nil {
			return archiveFailure(res.Output, archiveErr, partial)
		}
		return nil
	})
	if err != nil {
		return ArtifactReport{}, decoratePartial(err, cleanup())
	}

	var manifest Manifest
	var size int64
	var sum string
	err = core.RunStage(in.Reporter, "verify_archive", msgStageVerifyArchive, func() error {
		inspected, inspectErr := InspectArchive(filepath.Join(partial, DataFile))
		if inspectErr != nil {
			return integrityError(inspectErr, partial)
		}
		var hashErr error
		size, sum, hashErr = fileSHA256(filepath.Join(partial, DataFile))
		if hashErr != nil {
			return integrityError(hashErr, partial)
		}
		manifest = Manifest{
			Schema:        Schema,
			FormatVersion: FormatVersion,
			Complete:      true,
			CreatedAt:     clock.Now().UTC(),
			Tool:          c.cfg.Tool,
			Source:        SourceFrom(src, projectName, target.Environment, target.ProjectDir),
			Stack: StackState{
				ProjectName:      projectName,
				Services:         append([]string{}, affected...),
				RunningBefore:    append([]string{}, running...),
				StoppedForBackup: append([]string{}, stopped...),
			},
			Data: ArchiveInfo{
				File: DataFile, Format: DataFormatTarGz, SizeBytes: size, SHA256: sum,
			},
			Entries:       inspected.Entries,
			Totals:        inspected.Totals,
			Sensitive:     true,
			OwnershipNote: msgOwnershipNote,
		}
		if writeErr := WriteManifest(partial, manifest); writeErr != nil {
			return archiveFailure("", writeErr, partial)
		}
		return nil
	})
	if err != nil {
		return ArtifactReport{}, decoratePartial(err, cleanup())
	}

	err = core.RunStage(in.Reporter, "publish", msgStagePublish, func() error {
		if _, statErr := os.Lstat(final); statErr == nil {
			return outputError(fmt.Errorf("%w: %s already exists", ErrOutputInvalid, final))
		}
		if renameErr := os.Rename(partial, final); renameErr != nil {
			return core.FailCause(renameErr, core.Diagnostic{
				Category:    core.CategoryBackupFailed,
				Severity:    core.SeverityError,
				Message:     msgPublishFailed,
				Detail:      fmt.Sprintf("%s\n%s", renameErr.Error(), fmt.Sprintf(msgPartialLocation, partial)),
				NextActions: []string{msgPublishFailedAction},
			})
		}
		return nil
	})
	if err != nil {
		return ArtifactReport{}, decoratePartial(err, cleanup())
	}

	artifactSize := size
	if info, statErr := os.Stat(filepath.Join(final, ManifestFile)); statErr == nil {
		artifactSize += info.Size()
	}
	return ArtifactReport{
		Source:            manifest.Source,
		Path:              final,
		ManifestPath:      filepath.Join(final, ManifestFile),
		SizeBytes:         size,
		ArtifactSizeBytes: artifactSize,
		SHA256:            sum,
		Entries:           manifest.Totals.Entries,
		Complete:          true,
	}, nil
}

func (c *Controller) restoreState(ctx context.Context, in core.Invocation, target core.Target, services []string, artifacts []ArtifactReport, restarted *[]string) error {
	// The restore must run even when the operation context was canceled.
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	return core.RunStage(in.Reporter, "restore_state", msgStageRestoreState, func() error {
		res, err := c.cfg.Backend.StartServices(restoreCtx, target, services)
		if err != nil {
			return restoreError(err, services, artifacts, res.Output)
		}
		*restarted = append(*restarted, services...)
		return nil
	})
}

func resolveOutputDir(raw, cwd string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, ErrOutputRequired
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("%w: %v", ErrOutputInvalid, err)
		}
		if raw == "~" {
			raw = home
		} else if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return abs, false, nil
		}
		return "", false, fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("%w: %s is not a directory", ErrOutputInvalid, abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrOutputInvalid, err)
	}
	return resolved, true, nil
}

func outputError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrOutputRequired) {
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBackupOutputRequired,
			Severity:    core.SeverityError,
			Message:     msgOutputRequired,
			NextActions: []string{msgOutputRequiredAction, msgOutputUsageAction},
		})
	}
	diag := core.Diagnostic{
		Category:    core.CategoryBackupOutputInvalid,
		Severity:    core.SeverityError,
		Message:     msgOutputInvalid,
		Detail:      err.Error(),
		NextActions: []string{msgOutputInvalidAction},
	}
	if strings.Contains(strings.ToLower(err.Error()), "no space left") {
		diag.Message = msgArchiveDiskFull
		diag.NextActions = []string{msgArchiveDiskFullNext}
	}
	return core.FailCause(err, diag)
}

func selectionError(err error, sources []DataSource) error {
	if err == nil {
		return nil
	}
	var unknown *UnknownVolumeError
	var unsupported *UnsupportedSourceError
	var invalid *InvalidVolumeNameError
	switch {
	case errors.Is(err, ErrVolumeRequired):
		available := []string{}
		for _, src := range SelectableVolumes(sources) {
			available = append(available, src.Key)
		}
		diag := core.Diagnostic{
			Category:    core.CategoryBackupVolumeRequired,
			Severity:    core.SeverityError,
			Message:     msgVolumeRequired,
			NextActions: []string{msgVolumeRequiredAction, msgVolumeListAction},
		}
		if len(available) > 0 {
			diag.Detail = "사용 가능한 볼륨 키: " + strings.Join(available, ", ")
		}
		return core.Fail(diag)
	case errors.As(err, &unknown):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBackupVolumeUnknown,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnknown,
			Detail:      unknown.Error(),
			NextActions: []string{msgVolumeUnknownAction},
		})
	case errors.As(err, &unsupported), errors.As(err, &invalid):
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryBackupVolumeUnsupported,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnsupported,
			Detail:      err.Error(),
			NextActions: []string{msgVolumeUnsupportedAction},
		})
	default:
		return err
	}
}

func outsideConsumerError(outside []data.ContainerConsumer) error {
	var lines []string
	for _, c := range outside {
		project := c.Project
		if project == "" {
			project = "(Compose 관리 밖)"
		}
		service := c.Service
		if service == "" {
			service = "-"
		}
		lines = append(lines, fmt.Sprintf("%s (프로젝트 %s, 서비스 %s, 상태 %s)", c.Name, project, service, c.State))
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryBackupConsumerUnsafe,
		Severity:    core.SeverityError,
		Message:     msgConsumerUnsafe,
		Detail:      strings.Join(lines, "\n"),
		NextActions: []string{msgConsumerUnsafeAction, msgConsumerUnsafeNoStop, msgConsumerUnsafeRetry},
	})
}

func pausedServiceError(services []string) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryBackupConsumerUnsafe,
		Severity:    core.SeverityError,
		Message:     msgPausedUnsafe,
		Detail:      strings.Join(services, ", "),
		NextActions: []string{msgPausedUnsafeAction, msgPausedUnsafeNoStop},
	})
}

func stopError(err error, output string) error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryBackupFailed,
		Severity:    core.SeverityError,
		Message:     msgStopFailed,
		Detail:      strings.TrimSpace(output + "\n" + err.Error()),
		NextActions: []string{msgStopFailedAction, msgStopFailedNoData},
	})
}

func archiveFailure(output string, err error, location string) error {
	message := msgArchiveFailed
	actions := []string{msgArchiveFailedAction}
	lower := strings.ToLower(output + " " + err.Error())
	if strings.Contains(lower, "no space left") || strings.Contains(lower, "write error") {
		message = msgArchiveDiskFull
		actions = []string{msgArchiveDiskFullNext}
	} else if strings.Contains(lower, "permission denied") && !strings.Contains(lower, "/backup/") {
		message = msgArchiveReadFailed
		actions = []string{msgArchiveReadRead, msgArchiveFailedAction}
	}
	detail := strings.TrimSpace(output)
	if err != nil {
		if detail != "" {
			detail += "\n"
		}
		detail += err.Error()
	}
	if location != "" {
		detail += "\n" + fmt.Sprintf(msgPartialLocation, location)
	}
	diag := core.Diagnostic{
		Category:    core.CategoryBackupFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: actions,
	}
	if err != nil {
		return core.FailCause(err, diag)
	}
	return core.Fail(diag)
}

func integrityError(err error, location string) error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryBackupIntegrityFailed,
		Severity:    core.SeverityError,
		Message:     msgIntegrityFailed,
		Detail:      fmt.Sprintf("%s\n%s", err.Error(), fmt.Sprintf(msgPartialLocation, location)),
		NextActions: []string{msgIntegrityFailedAction},
	})
}

func restoreError(err error, services []string, artifacts []ArtifactReport, output string) error {
	var paths []string
	for _, a := range artifacts {
		paths = append(paths, a.Path)
	}
	detail := strings.TrimSpace(output + "\n" + err.Error())
	detail += "\n서비스: " + strings.Join(services, ", ")
	if len(paths) > 0 {
		detail += "\n" + msgRestoreFailedData + "\n" + strings.Join(paths, "\n")
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryBackupStateRestoreFailed,
		Severity:    core.SeverityError,
		Message:     msgRestoreFailed,
		Detail:      detail,
		NextActions: []string{msgRestoreFailedAction},
	})
}

func decoratePartial(err error, cleanup string) error {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		ce.Diagnostics[0].Detail = strings.TrimSpace(ce.Diagnostics[0].Detail + "\n" + cleanup)
		return ce
	}
	return err
}

func mergeErrors(base, extra error) error {
	if extra == nil {
		return base
	}
	if base == nil {
		return extra
	}
	var baseErr, extraErr *core.Error
	if errors.As(base, &baseErr) && errors.As(extra, &extraErr) {
		baseErr.Diagnostics = append(baseErr.Diagnostics, extraErr.Diagnostics...)
		return baseErr
	}
	return core.FailCause(base, core.Diagnostic{
		Category: core.CategoryBackupFailed,
		Severity: core.SeverityError,
		Message:  base.Error() + "; " + extra.Error(),
	})
}

func identityError(err error) error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return err
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryBackupFailed,
		Severity:    core.SeverityError,
		Message:     msgIdentityFailed,
		Detail:      err.Error(),
		NextActions: []string{msgIdentityFailedAction},
	})
}

func busyError(busy *state.BusyError) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryStackBusy,
		Severity:    core.SeverityError,
		Message:     "다른 dcraft 작업이 같은 스택을 변경하고 있습니다. 백업을 시작하지 않았습니다.",
		Detail:      busy.Error(),
		NextActions: []string{"진행 중인 작업이 끝난 뒤 다시 실행하세요.", "dcraft status로 현재 상태를 확인하세요."},
	})
}

func stateError(err error) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     "스택 잠금 상태를 확인하지 못했습니다.",
		Detail:      err.Error(),
		NextActions: []string{"상태 폴더(DCRAFT_STATE_DIR)의 권한과 남은 공간을 확인하세요."},
	}
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

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return msgPlanNoneRunning
	}
	return strings.Join(values, ", ")
}
