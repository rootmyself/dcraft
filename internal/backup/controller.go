package backup

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
	Locks   state.Locker
	Clock   core.Clock
	Tool    Tool
	PID     int
}

// Controller owns the backup operation and the shared discovery used by plan,
// interactive selection and later tickets.
type Controller struct {
	cfg Config

	wizardMu         sync.Mutex
	wizardCacheValue *wizardPreviewCache
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
	if c.cfg.Backend == nil || c.cfg.Names == nil || c.cfg.Locks == nil {
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
	redact := func(text string) string {
		return c.cfg.Backend.Redact(ctx, in.Target, text)
	}

	// 1. Resolve the user-chosen output path before anything is stopped. The
	// directory is not created yet: bind containment must be checked first so
	// dcraft never writes the backup store inside a source folder.
	var outputPlan string
	err = core.RunStage(in.Reporter, "output", msgStageOutput, func() error {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return outputError(cwdErr)
		}
		dir, dirErr := ResolveOutputPath(in.Option("output"), cwd)
		if dirErr != nil {
			return outputError(dirErr)
		}
		outputPlan = dir
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
		acquired, lockErr := c.cfg.Locks.AcquireStackLock(ctx, in.Target, state.LockHolder{
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
		sources, sErr := d.Select(in.OptionList("volume"), in.OptionList("bind"))
		if sErr != nil {
			return selectionError(sErr, d.Sources)
		}
		selected = sources
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 5. Re-resolve immediately before any stop. A plan or interactive preview
	// is only advisory; a changed volume or bind identity must never be
	// archived or attributed to the stack the user saw.
	err = core.RunStage(in.Reporter, "recheck_selection", msgStageRecheck, func() error {
		fresh, freshErr := Discover(ctx, c.cfg.Backend, target, projectName)
		if freshErr != nil {
			return freshErr
		}
		freshSelected, selectErr := fresh.Select(in.OptionList("volume"), in.OptionList("bind"))
		if selectErr != nil {
			return selectionError(selectErr, fresh.Sources)
		}
		if !sameSelection(selected, freshSelected) {
			return selectionChangedError(selected, freshSelected)
		}
		discovery = fresh
		selected = freshSelected
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 5.1 Validate host folders and prove the output location does not lie
	// inside a selected source. The output directory is created only after the
	// containment check, still before any service is stopped.
	var outputDir string
	err = core.RunStage(in.Reporter, "check_paths", msgStageCheckPaths, func() error {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return outputError(cwdErr)
		}
		if bindErr := CheckBindSources(selected, outputPlan); bindErr != nil {
			return bindSelectionError(bindErr)
		}
		dir, dirErr := ValidateOutputDir(outputPlan, cwd)
		if dirErr != nil {
			return outputError(dirErr)
		}
		outputDir = dir
		// Recheck after creation with the final symlink-resolved path.
		if bindErr := CheckBindSources(selected, outputDir); bindErr != nil {
			return bindSelectionError(bindErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	affected := discovery.AffectedServices(selected)
	running := discovery.RunningAmong(affected)

	// 6. Refuse anything that cannot be stopped or restored safely.
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

	// 7. Stop only the affected services that were running.
	if len(running) > 0 {
		stopped = append(stopped, running...)
		err = core.RunStage(in.Reporter, "stop_consumers", msgStageStop, func() error {
			res, stopErr := c.cfg.Backend.StopServices(ctx, target, running)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if stopErr != nil {
				return stopError(errors.New(redact(stopErr.Error())), redact(res.Output))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// 8. Archive each selected volume. One failure stops the run; earlier
	// published artifacts are kept and named in the failure.
	for _, src := range selected {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		artifact, archiveErr := c.archiveOne(ctx, in, target, outputDir, src, projectName, affected, running, stopped, clock)
		if archiveErr != nil {
			return nil, annotatePublished(archiveErr, artifacts)
		}
		artifacts = append(artifacts, artifact)
	}

	// 9. Restore only the services this operation stopped.
	if restoreErr := restore(); restoreErr != nil {
		return nil, restoreErr
	}

	sources := make([]Source, 0, len(selected))
	for _, src := range selected {
		sources = append(sources, SourceFrom(src, projectName, target.Environment, target.ProjectDir))
	}
	var anyVolume, anyBind bool
	for _, src := range selected {
		switch src.Kind {
		case data.KindBind:
			anyBind = true
		default:
			anyVolume = true
		}
	}
	notices := []string{}
	if anyVolume {
		notices = append(notices, fmt.Sprintf(msgHelperImageNotice, c.cfg.Backend.HelperImage()))
	}
	if anyBind {
		notices = append(notices, msgBindHostArchiveNotice)
	}
	notices = append(notices, msgSensitiveNotice)
	if anyVolume {
		notices = append(notices, msgOwnershipNote)
	}
	if anyBind {
		notices = append(notices, msgBindOwnershipNote)
	}
	notices = append(notices, msgExcludedNotice, msgNoAutoDeleteNote)
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
	selected, err := discovery.Select(in.OptionList("volume"), in.OptionList("bind"))
	if err != nil {
		return core.Plan{}, selectionError(err, discovery.Sources)
	}
	if err := CheckBindSources(selected, outputDir); err != nil {
		return core.Plan{}, bindSelectionError(err)
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
	var anyVolume, anyBind bool
	for _, src := range selected {
		if src.Kind == data.KindBind {
			anyBind = true
		} else {
			anyVolume = true
		}
	}
	if anyBind {
		b.WriteString(msgPlanSharedNone)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, msgPlanServices+"\n", joinOrNone(affected))
	fmt.Fprintf(&b, msgPlanRunning+"\n", joinOrNone(running))
	fmt.Fprintf(&b, msgPlanStopped+"\n", joinOrNone(stoppedAlready))
	if anyVolume {
		fmt.Fprintf(&b, msgPlanHelper+"\n", c.cfg.Backend.HelperImage())
	}
	if anyBind {
		b.WriteString(msgPlanHostTar)
		b.WriteString("\n")
		b.WriteString(msgPlanBindOwnership)
		b.WriteString("\n")
	}
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
	redact := func(text string) string {
		return c.cfg.Backend.Redact(ctx, target, text)
	}
	base := ArtifactName(SourceFrom(src, projectName, target.Environment, target.ProjectDir), clock.Now())
	final, err := UniqueArtifactPath(outputDir, base)
	if err != nil {
		return ArtifactReport{}, outputError(err)
	}
	partial := final + PartialSuffix
	if err := os.Mkdir(partial, 0o700); err != nil {
		return ArtifactReport{}, archiveFailure("", err, partial)
	}
	cleanup := func() string {
		if removeErr := os.RemoveAll(partial); removeErr != nil {
			return fmt.Sprintf(msgPartialLocation+" ("+msgPartialKept+")", partial)
		}
		return msgPartialCleaned
	}

	archiveTitle := msgStageArchive + ": " + src.Key
	if src.Kind == data.KindBind {
		archiveTitle = msgStageArchiveBind + ": " + src.Name
	}
	err = core.RunStage(in.Reporter, "archive", archiveTitle, func() error {
		var archiveErr error
		var output string
		if src.Kind == data.KindBind {
			archiveErr = ArchiveHostDir(ctx, src.Name, filepath.Join(partial, DataFile))
		} else {
			var res data.OpResult
			res, archiveErr = c.cfg.Backend.ArchiveVolume(ctx, src.Name, partial)
			output = res.Output
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if archiveErr != nil {
			if src.Kind == data.KindBind {
				return bindArchiveError(errors.New(redact(archiveErr.Error())), partial)
			}
			return archiveFailure(redact(output), errors.New(redact(archiveErr.Error())), partial)
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
			return integrityError(errors.New(redact(inspectErr.Error())), partial)
		}
		var hashErr error
		size, sum, hashErr = fileSHA256(filepath.Join(partial, DataFile))
		if hashErr != nil {
			return integrityError(errors.New(redact(hashErr.Error())), partial)
		}
		if syncErr := syncFile(filepath.Join(partial, DataFile)); syncErr != nil {
			return archiveFailure("", syncErr, partial)
		}
		ownershipNote := msgOwnershipNote
		if src.Kind == data.KindBind {
			ownershipNote = msgBindOwnershipNote
		}
		manifest = Manifest{
			Schema:        Schema,
			FormatVersion: FormatVersion,
			// The completion marker is written only after the artifact is
			// published. A partial directory is never marked complete.
			Complete:  false,
			CreatedAt: clock.Now().UTC(),
			Tool:      c.cfg.Tool,
			Source:    SourceFrom(src, projectName, target.Environment, target.ProjectDir),
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
			OwnershipNote: ownershipNote,
		}
		if writeErr := WriteManifest(partial, manifest); writeErr != nil {
			return archiveFailure("", writeErr, partial)
		}
		if syncErr := syncDir(partial); syncErr != nil {
			return archiveFailure("", syncErr, partial)
		}
		return nil
	})
	if err != nil {
		return ArtifactReport{}, decoratePartial(err, cleanup())
	}

	published := false
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
		published = true
		if syncErr := syncDir(outputDir); syncErr != nil {
			return core.FailCause(syncErr, core.Diagnostic{
				Category:    core.CategoryBackupFailed,
				Severity:    core.SeverityError,
				Message:     msgPublishFailed,
				Detail:      fmt.Sprintf("%s\n%s", syncErr.Error(), fmt.Sprintf(msgPartialLocation, final)),
				NextActions: []string{msgPublishFailedAction},
			})
		}
		return nil
	})
	if err != nil {
		if published {
			return ArtifactReport{}, decoratePublished(err, final)
		}
		return ArtifactReport{}, decoratePartial(err, cleanup())
	}

	// Write the completion marker last, in the final location, then re-verify.
	// A failure here demotes the artifact back to `.partial` so it can never be
	// mistaken for a complete backup.
	err = core.RunStage(in.Reporter, "complete", msgStageComplete, func() error {
		manifest.Complete = true
		if writeErr := WriteManifest(final, manifest); writeErr != nil {
			return invalidatePublished(final, writeErr)
		}
		if syncErr := syncDir(final); syncErr != nil {
			return invalidatePublished(final, syncErr)
		}
		if _, verifyErr := Verify(final); verifyErr != nil {
			return invalidatePublished(final, errors.New(redact(verifyErr.Error())))
		}
		return nil
	})
	if err != nil {
		return ArtifactReport{}, err
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
			redactedOutput := c.cfg.Backend.Redact(ctx, target, res.Output)
			return restoreError(errors.New(c.cfg.Backend.Redact(ctx, target, err.Error())), services, artifacts, redactedOutput)
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
	var unknownBind *UnknownBindError
	var ambiguous *AmbiguousBindError
	var bindAsVolume *BindAsVolumeError
	switch {
	case errors.Is(err, ErrVolumeRequired):
		available := []string{}
		for _, src := range SelectableSources(sources) {
			if src.Kind == data.KindBind {
				available = append(available, src.Name)
			} else {
				available = append(available, src.Key)
			}
		}
		diag := core.Diagnostic{
			Category:    core.CategoryBackupVolumeRequired,
			Severity:    core.SeverityError,
			Message:     msgSourceRequired,
			NextActions: []string{msgSourceRequiredAction, msgSourceListAction},
		}
		if len(available) > 0 {
			diag.Detail = "사용 가능한 데이터 원본: " + strings.Join(available, ", ")
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
	case errors.As(err, &unknownBind):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBackupBindUnknown,
			Severity:    core.SeverityError,
			Message:     msgBindUnknown,
			Detail:      unknownBind.Error(),
			NextActions: []string{msgBindUnknownAction},
		})
	case errors.As(err, &ambiguous):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBackupBindUnknown,
			Severity:    core.SeverityError,
			Message:     msgBindAmbiguous,
			Detail:      ambiguous.Error(),
			NextActions: []string{msgBindAmbiguousAction},
		})
	case errors.As(err, &bindAsVolume):
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryBackupVolumeUnknown,
			Severity:    core.SeverityError,
			Message:     msgVolumeUnknown,
			Detail:      bindAsVolume.Error(),
			NextActions: []string{msgBindInsteadOfVolumeAction},
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

// bindSelectionError maps a host-folder path failure to a stable category. An
// unsafe selection (filesystem root or output inside the source) is refused
// before any stop and never touches the source.
func bindSelectionError(err error) error {
	var pathErr *BindPathError
	if errors.As(err, &pathErr) {
		if pathErr.Reason == BindReasonOutputInside {
			return core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryBackupBindUnsafe,
				Severity:    core.SeverityError,
				Message:     msgBindOutputInside,
				Detail:      err.Error(),
				NextActions: []string{msgBindOutputInsideAction, msgBindOutputInsideNoStop},
			})
		}
		if pathErr.Reason == BindReasonFilesystemRoot {
			return core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryBackupBindUnsafe,
				Severity:    core.SeverityError,
				Message:     msgBindRootUnsafe,
				Detail:      err.Error(),
				NextActions: []string{msgBindRootUnsafeAction},
			})
		}
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryBackupBindInvalid,
			Severity:    core.SeverityError,
			Message:     msgBindInvalid,
			Detail:      err.Error(),
			NextActions: []string{msgBindInvalidAction, msgBindInvalidPermissions},
		})
	}
	if errors.Is(err, ErrOutputInvalid) {
		return outputError(err)
	}
	return err
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

func bindArchiveError(err error, location string) error {
	detail := err.Error()
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "no space left") || strings.Contains(lower, "write error") {
		detail += "\n" + fmt.Sprintf(msgPartialLocation, location)
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryBackupFailed,
			Severity:    core.SeverityError,
			Message:     msgArchiveDiskFull,
			Detail:      detail,
			NextActions: []string{msgArchiveDiskFullNext},
		})
	}
	detail += "\n" + fmt.Sprintf(msgPartialLocation, location)
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryBackupBindInvalid,
		Severity:    core.SeverityError,
		Message:     msgBindArchiveFailed,
		Detail:      detail,
		NextActions: []string{msgBindInvalidAction, msgBindInvalidPermissions},
	})
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

// WizardSteps implements the read-only first half of the interactive flow. It
// lists named volumes and declared bind folders in one keyboard-driven multi
// selection and asks for one output directory. It creates nothing and never
// stops a service.
func (c *Controller) WizardSteps(ctx context.Context, in core.Invocation) (core.WizardSpec, error) {
	if c.cfg.Backend == nil || c.cfg.Names == nil {
		return core.WizardSpec{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	target := in.Target
	name, err := c.effectiveName(ctx, target)
	if err != nil {
		return core.WizardSpec{}, err
	}
	target.ProjectName = name
	discovery, err := Discover(ctx, c.cfg.Backend, target, name)
	if err != nil {
		return core.WizardSpec{}, err
	}
	selectable := SelectableSources(discovery.Sources)
	if len(selectable) == 0 {
		return core.WizardSpec{}, selectionError(ErrVolumeRequired, discovery.Sources)
	}
	shared := map[string][]data.ContainerConsumer{}
	options := make([]core.WizardOption, 0, len(selectable))
	for _, src := range selectable {
		value := "volume:" + src.Key
		label := "볼륨 " + DescribeSource(src)
		if src.Kind == data.KindBind {
			value = "bind:" + src.Name
			label = DescribeSource(src)
			if _, statErr := os.Lstat(src.Name); statErr != nil {
				label += " · 경로를 읽을 수 없음"
			}
			outside, consumerErr := discovery.OutsideConsumers(ctx, c.cfg.Backend, src)
			if consumerErr != nil {
				return core.WizardSpec{}, consumerErr
			}
			shared[src.Name] = outside
			if len(outside) > 0 {
				label += " · 다른 스택 공유: " + consumerNames(outside)
			} else {
				label += " · 다른 스택 공유 없음"
			}
		}
		options = append(options, core.WizardOption{Value: value, Label: label})
	}
	c.cacheWizardPreview(discovery, shared)
	return core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{
				Name:    "sources",
				Title:   msgWizardSourceTitle,
				Prompt:  msgWizardSourcePrompt,
				Options: options,
				Multi:   true,
			},
			{
				Name:        "output",
				Title:       msgWizardOutputTitle,
				Prompt:      msgWizardOutputPrompt,
				AllowCustom: true,
				CustomHint:  msgWizardOutputHint,
				Default:     msgWizardOutputDefault,
			},
		},
	}, nil
}

// WizardPreview describes the collected answers using the last read-only
// discovery. It is a preview only: Run re-resolves everything and refuses a
// changed selection before stopping anything.
func (c *Controller) WizardPreview(answers map[string]string) string {
	text, _ := c.wizardPreview(answers)
	return text
}

func (c *Controller) wizardPreview(answers map[string]string) (string, error) {
	output := strings.TrimSpace(answers["output"])
	if output == "" {
		output = msgWizardOutputDefault
	}
	volumes, binds := ParseWizardSources(answers["sources"])
	var b strings.Builder
	b.WriteString(msgPlanHeader)
	b.WriteString("\n\n")
	cached := c.wizardCache()
	var selected []DataSource
	if cached != nil {
		chosen, err := cached.discovery.Select(volumes, binds)
		if err != nil {
			return "", selectionError(err, cached.discovery.Sources)
		}
		selected = chosen
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return "", outputError(cwdErr)
		}
		outputPath, resolveErr := ResolveOutputPath(output, cwd)
		if resolveErr != nil {
			return "", outputError(resolveErr)
		}
		if bindErr := CheckBindSources(selected, outputPath); bindErr != nil {
			return "", bindSelectionError(bindErr)
		}
	}
	if cached != nil && len(selected) > 0 {
		fmt.Fprintf(&b, "프로젝트: %s\n", cached.discovery.ProjectName)
	}
	b.WriteString(msgPlanSources)
	b.WriteString("\n")
	if len(selected) == 0 {
		for _, key := range volumes {
			fmt.Fprintf(&b, "  - 볼륨 %s\n", key)
		}
		for _, path := range binds {
			fmt.Fprintf(&b, "  - 폴더 %s\n", path)
		}
		b.WriteString(msgWizardRecheck)
		b.WriteString("\n")
	} else {
		for _, src := range selected {
			fmt.Fprintf(&b, "  - %s\n", DescribeSource(src))
			if src.Kind == data.KindBind && cached != nil {
				outside := cached.shared[src.Name]
				if len(outside) > 0 {
					fmt.Fprintf(&b, "    "+msgPlanSharedSome+"\n", consumerNames(outside))
				} else {
					b.WriteString("    " + msgPlanSharedNone + "\n")
				}
			}
		}
		affected := cached.discovery.AffectedServices(selected)
		running := cached.discovery.RunningAmong(affected)
		stopped := difference(affected, running)
		fmt.Fprintf(&b, msgPlanServices+"\n", joinOrNone(affected))
		fmt.Fprintf(&b, msgPlanRunning+"\n", joinOrNone(running))
		fmt.Fprintf(&b, msgPlanStopped+"\n", joinOrNone(stopped))
		hasBind := false
		hasVolume := false
		for _, src := range selected {
			if src.Kind == data.KindBind {
				hasBind = true
			} else {
				hasVolume = true
			}
		}
		if hasVolume {
			fmt.Fprintf(&b, msgPlanHelper+"\n", c.cfg.Backend.HelperImage())
		}
		if hasBind {
			b.WriteString(msgPlanHostTar)
			b.WriteString("\n")
			b.WriteString(msgPlanBindOwnership)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, msgPlanOutput+"\n", output)
	b.WriteString(msgPlanStopStep)
	b.WriteString("\n")
	b.WriteString(msgPlanSensitive)
	b.WriteString("\n")
	b.WriteString(msgPlanNoAutoDelete)
	return b.String(), nil
}

// ParseWizardSources splits the mixed TUI answer into volume keys and bind
// paths. Values are prefixed so a host path can never be mistaken for a
// Compose volume key.
func ParseWizardSources(raw string) (volumes, binds []string) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "volume:"):
			if value := strings.TrimSpace(strings.TrimPrefix(line, "volume:")); value != "" {
				volumes = append(volumes, value)
			}
		case strings.HasPrefix(line, "bind:"):
			if value := strings.TrimSpace(strings.TrimPrefix(line, "bind:")); value != "" {
				binds = append(binds, value)
			}
		}
	}
	return volumes, binds
}

func consumerNames(consumers []data.ContainerConsumer) string {
	var names []string
	for _, consumer := range consumers {
		name := consumer.Name
		if consumer.Project != "" {
			name += " (" + consumer.Project + ")"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type wizardPreviewCache struct {
	discovery Discovery
	shared    map[string][]data.ContainerConsumer
}

func (c *Controller) cacheWizardPreview(d Discovery, shared map[string][]data.ContainerConsumer) {
	c.wizardMu.Lock()
	defer c.wizardMu.Unlock()
	c.wizardCacheValue = &wizardPreviewCache{discovery: d, shared: shared}
}

func (c *Controller) wizardCache() *wizardPreviewCache {
	c.wizardMu.Lock()
	defer c.wizardMu.Unlock()
	return c.wizardCacheValue
}

func sameSelection(a, b []DataSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Name != b[i].Name || a[i].Declared != b[i].Declared ||
			a[i].Kind != b[i].Kind || a[i].Anonymous != b[i].Anonymous ||
			a[i].External != b[i].External ||
			!sameStrings(a[i].Services, b[i].Services) ||
			!sameStrings(a[i].DeclaredPaths, b[i].DeclaredPaths) ||
			!sameStrings(a[i].MountTargets, b[i].MountTargets) {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func selectionChangedError(before, after []DataSource) error {
	describe := func(sources []DataSource) string {
		var parts []string
		for _, src := range sources {
			parts = append(parts, src.Key+"="+src.Name)
		}
		return strings.Join(parts, ", ")
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryBackupSelectionChanged,
		Severity:    core.SeverityError,
		Message:     msgSelectionChanged,
		Detail:      msgSelectionChangedBefore + describe(before) + "\n" + msgSelectionChangedAfter + describe(after),
		NextActions: []string{msgSelectionChangedAction},
	})
}

// annotatePublished keeps already valid artifacts visible when a later volume
// fails. They are never deleted to make the run look atomic.
func annotatePublished(err error, artifacts []ArtifactReport) error {
	if len(artifacts) == 0 {
		return err
	}
	var ce *core.Error
	if !errors.As(err, &ce) || len(ce.Diagnostics) == 0 {
		return err
	}
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		paths = append(paths, artifact.Path)
	}
	ce.Diagnostics[0].Detail = strings.TrimSpace(ce.Diagnostics[0].Detail + "\n" + msgPublishedRemain + "\n" + strings.Join(paths, "\n"))
	return ce
}

func decoratePublished(err error, final string) error {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		ce.Diagnostics[0].Detail = strings.TrimSpace(ce.Diagnostics[0].Detail + "\n" + fmt.Sprintf(msgPublishedInvalid, final))
		return ce
	}
	return err
}
