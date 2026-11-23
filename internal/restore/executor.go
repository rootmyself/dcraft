package restore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
	"github.com/rootmyself/dcraft/internal/state"
)

// DataState is the honest state of the destination after an attempted restore.
type DataState string

const (
	// DataUntouched means the replacement did not start; original data is
	// still in place.
	DataUntouched DataState = "untouched"
	// DataUncertain means emptying/extraction/verification may have changed
	// only part of the content. Never report this as success.
	DataUncertain DataState = "uncertain_partial"
	// DataReplaced means the restored content matched the manifest.
	DataReplaced DataState = "replaced_verified"
)

// ExecutionRequest is the storage-independent half: a validated artifact, one
// prepared destination, the chosen safety directory and the observed service
// state. Ticket #19 builds the same request with a bind Destination.
type ExecutionRequest struct {
	Target      core.Target
	ProjectName string
	Artifact    Artifact
	Destination Destination
	SafetyDir   string
	Affected    []string
	Running     []string
	Explicit    bool
	// ExplicitPath permits a documented bind-folder path mapping. It is
	// independent of the source (project/environment) mapping flag.
	ExplicitPath bool
	// Warnings are the read-only warnings observed during selection (for
	// example an unknown current size). They are carried into the report.
	Warnings []string
	// Locked reports that the caller already holds the per-stack change lock
	// for the whole sequence. Named-volume restore acquires it around
	// discovery and the wizard recheck; #19 may use the same flag after its
	// own selection. False makes Executor acquire and release the lock
	// itself.
	Locked bool
}

// ExecutionReport is the observable outcome of one safe replacement. It is
// also used for failure and cancellation details.
type ExecutionReport struct {
	ProjectName      string         `json:"project_name"`
	TargetKind       data.Kind      `json:"target_kind"`
	VolumeKey        string         `json:"volume_key,omitempty"`
	VolumeName       string         `json:"volume_name,omitempty"`
	HostPath         string         `json:"host_path,omitempty"`
	ArtifactPath     string         `json:"artifact_path"`
	BackupContent    ContentSummary `json:"backup_content"`
	CurrentContent   ContentSummary `json:"current_content"`
	SafetyBackup     string         `json:"safety_backup,omitempty"`
	AffectedServices []string       `json:"affected_services"`
	RunningBefore    []string       `json:"running_before"`
	Stopped          []string       `json:"stopped"`
	Restarted        []string       `json:"restarted"`
	Stage            string         `json:"stage"`
	DataState        DataState      `json:"data_state"`
	ExplicitMapping  bool           `json:"explicit_mapping"`
	PathMapping      bool           `json:"path_mapping,omitempty"`
	Warnings         []string       `json:"warnings"`
	Notices          []string       `json:"notices"`
}

// Executor owns the published safe-replace sequence. It never bypasses the
// stack lock, the safety backup or the extraction verification, and it never
// restarts services when data integrity is unknown.
type Executor struct {
	Backend data.Backend
	Locks   state.Locker
	Clock   core.Clock
	Tool    backup.Tool
	PID     int
	Space   SpaceChecker
}

func (e *Executor) clock() core.Clock {
	if e.Clock != nil {
		return e.Clock
	}
	return core.SystemClock{}
}

// Execute runs stop → safety backup → empty → extract → verify → state
// restore for one destination. It returns a report even when it fails.
func (e *Executor) Execute(ctx context.Context, in core.Invocation, req ExecutionRequest) (report ExecutionReport, err error) {
	report = ExecutionReport{
		ProjectName:      req.ProjectName,
		TargetKind:       req.Destination.Kind(),
		ArtifactPath:     req.Artifact.Path,
		BackupContent:    ContentSummary{Bytes: req.Artifact.Manifest.Totals.Bytes, Entries: int64(req.Artifact.Manifest.Totals.Entries)},
		AffectedServices: append([]string{}, req.Affected...),
		RunningBefore:    append([]string{}, req.Running...),
		Stopped:          []string{},
		Restarted:        []string{},
		DataState:        DataUntouched,
		ExplicitMapping:  req.Explicit,
		PathMapping:      req.ExplicitPath,
		Warnings:         append([]string{}, req.Warnings...),
		Notices:          []string{},
	}
	if report.TargetKind == data.KindBind {
		report.HostPath = req.Destination.Name()
	} else {
		report.VolumeKey = req.Destination.Key()
		report.VolumeName = req.Destination.Name()
	}
	if e == nil || e.Backend == nil || e.Locks == nil || req.Destination == nil {
		return report, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	redact := func(text string) string {
		return e.Backend.Redact(ctx, req.Target, text)
	}

	var lock *state.StackLock
	if !req.Locked {
		err = core.RunStage(in.Reporter, "coordinate", msgStageCoordinate, func() error {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			acquired, lockErr := e.Locks.AcquireStackLock(ctx, req.Target, state.LockHolder{
				Action: "restore", PID: e.PID, StartedAt: e.clock().Now(),
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
			return report, err
		}
		defer func() { _ = lock.Release() }()
	}

	finalized := false
	defer func() {
		if finalized || report.DataState != DataUntouched || len(report.Stopped) == 0 {
			return
		}
		if restoreErr := e.restoreState(ctx, in, req.Target, report.Stopped, &report.Restarted); restoreErr != nil {
			err = mergeErrors(err, restoreErr)
		}
	}()

	// Everything below the lock is authoritative: the artifact and mapping
	// are re-read immediately before any service stops.
	report.Stage = "inspect_consumers"
	err = core.RunStage(in.Reporter, "inspect_consumers", msgStageConsumers, func() error {
		warnings, matchErr := req.Destination.Match(req.Artifact.Manifest, req.Explicit)
		if matchErr != nil {
			report.Warnings = appendUniqueWarnings(report.Warnings, warnings)
			return targetError(matchErr)
		}
		report.Warnings = appendUniqueWarnings(report.Warnings, warnings)
		if prepareErr := req.Destination.Prepare(ctx); prepareErr != nil {
			return targetError(prepareErr)
		}
		unsafe, consumerErr := req.Destination.UnsafeConsumers(ctx)
		if consumerErr != nil {
			return consumerErr
		}
		if len(unsafe) > 0 {
			return outsideConsumerError(unsafe)
		}
		runtime, runtimeErr := e.Backend.ServiceRuntime(ctx, req.Target)
		if runtimeErr != nil {
			return runtimeErr
		}
		if paused := pausedServices(runtime, req.Affected); len(paused) > 0 {
			return pausedServiceError(paused)
		}
		return nil
	})
	if err != nil {
		return report, err
	}

	// Free-space precheck happens before any stop. When the current size
	// cannot be measured the check is advisory; the safety backup itself is
	// still mandatory and aborts before replacement.
	report.Stage = "safety_backup"
	err = core.RunStage(in.Reporter, "safety_check", msgStageSafetyCheck, func() error {
		usage, usageErr := req.Destination.Usage(ctx)
		if usageErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("현재 데이터 크기 확인 실패: %s", redact(usageErr.Error())))
			return nil
		}
		report.CurrentContent = usage
		checker := e.Space
		if checker == nil {
			checker = DiskSpace{}
		}
		available, spaceErr := checker.AvailableBytes(req.SafetyDir)
		if spaceErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("안전 백업 공간 확인 실패: %s", redact(spaceErr.Error())))
			return nil
		}
		if needed := requiredSpace(usage); available < needed {
			return spaceError(needed, available)
		}
		return nil
	})
	if err != nil {
		return report, err
	}

	if len(req.Running) > 0 {
		report.Stage = "stop_consumers"
		report.Stopped = append(report.Stopped, req.Running...)
		err = core.RunStage(in.Reporter, "stop_consumers", msgStageStop, func() error {
			res, stopErr := e.Backend.StopServices(ctx, req.Target, req.Running)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if stopErr != nil {
				return stopError(redactedCause(stopErr, redact(stopErr.Error())), redact(res.Output))
			}
			return nil
		})
		if err != nil {
			return report, err
		}
	}

	report.Stage = "safety_backup"
	stack := safetyStack(req.ProjectName, req.Affected, req.Running, report.Stopped)
	err = core.RunStage(in.Reporter, "safety_backup", msgStageSafetyBackup, func() error {
		safety, safetyErr := CreateSafetyBackup(ctx, req.Destination, req.SafetyDir, stack, e.Tool, e.clock())
		report.SafetyBackup = safety.Path
		if safetyErr != nil {
			return safetyError(redactedCause(safetyErr, redact(safetyErr.Error())), safety.Path)
		}
		return nil
	})
	if err != nil {
		return report, err
	}

	report.Stage = "replace"
	if hashErr := CheckArtifactData(req.Artifact); hashErr != nil {
		return report, artifactChangedError(hashErr, &report)
	}
	report.DataState = DataUncertain
	err = core.RunStage(in.Reporter, "replace", msgStageReplace, func() error {
		res, emptyErr := req.Destination.Empty(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if emptyErr != nil {
			return replaceError("empty", redactedCause(emptyErr, redact(emptyErr.Error())), redact(res.Output), &report)
		}
		res, extractErr := req.Destination.Extract(ctx, req.Artifact.Path)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if extractErr != nil {
			return replaceError("extract", redactedCause(extractErr, redact(extractErr.Error())), redact(res.Output), &report)
		}
		return nil
	})
	if err != nil {
		return report, err
	}

	report.Stage = "verify"
	err = core.RunStage(in.Reporter, "verify", msgStageVerify, func() error {
		res, verifyErr := req.Destination.Verify(ctx, req.Artifact.Manifest)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if verifyErr != nil {
			return verifyError(redactedCause(verifyErr, redact(verifyErr.Error())), redact(res.Output), &report)
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	report.DataState = DataReplaced

	report.Stage = "restore_state"
	if len(report.Stopped) > 0 {
		if restoreErr := e.restoreState(ctx, in, req.Target, report.Stopped, &report.Restarted); restoreErr != nil {
			return report, restoreErr
		}
	}
	finalized = true
	if report.TargetKind == data.KindBind {
		report.Notices = append(report.Notices,
			msgOverwrite, msgBindReplace, msgInterrupt, msgSafetyNotice, msgBindOwnershipNotice,
			msgNoHooks, msgNoAutoDelete, msgOnlyRunning,
		)
	} else {
		report.Notices = append(report.Notices,
			msgOverwrite, msgInterrupt, msgSafetyNotice, msgOwnership,
			msgNoHooks, msgNoAutoDelete, msgOnlyRunning,
		)
	}
	return report, nil
}

func (e *Executor) restoreState(ctx context.Context, in core.Invocation, target core.Target, services []string, restarted *[]string) error {
	if len(services) == 0 {
		return nil
	}
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	return core.RunStage(in.Reporter, "restore_state", msgStageRestoreState, func() error {
		res, err := e.Backend.StartServices(restoreCtx, target, services)
		if err != nil {
			return restoreStateError(e.Backend.Redact(restoreCtx, target, res.Output), redactedCause(err, e.Backend.Redact(restoreCtx, target, err.Error())), services)
		}
		*restarted = append(*restarted, services...)
		return nil
	})
}

// appendUniqueWarnings avoids reporting the same mapping warning twice when a
// controller already carried it from selection into the execution request.
func appendUniqueWarnings(existing, extra []string) []string {
	for _, warning := range extra {
		found := false
		for _, current := range existing {
			if current == warning {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, warning)
		}
	}
	return existing
}

func pausedServices(runtime []data.ServiceRuntime, affected []string) []string {
	want := map[string]bool{}
	for _, s := range affected {
		want[s] = true
	}
	var out []string
	for _, svc := range runtime {
		if want[svc.Service] && svc.State == "paused" {
			out = append(out, svc.Service)
		}
	}
	return out
}

func busyError(busy *state.BusyError) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryStackBusy,
		Severity:    core.SeverityError,
		Message:     "다른 dcraft 작업이 같은 스택을 변경하고 있음. 복원을 시작하지 않았음.",
		Detail:      busy.Error(),
		NextActions: []string{"진행 중인 작업이 끝난 뒤 다시 실행.", "dcraft status로 현재 상태 확인."},
	})
}

func stateError(err error) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     "스택 잠금 상태 확인 실패.",
		Detail:      err.Error(),
		NextActions: []string{"상태 폴더(DCRAFT_STATE_DIR)의 권한과 남은 공간 확인."},
	}
}

func targetError(err error) error {
	category := core.CategoryRestoreTargetInvalid
	message := msgTargetInvalid
	actions := []string{msgTargetInvalidAction}
	switch {
	case errors.Is(err, ErrTargetMismatch):
		category = core.CategoryRestoreTargetMismatch
		message = msgTargetMismatch
		actions = []string{msgTargetMismatchAction}
	case errors.Is(err, ErrTargetUnsupported):
		category = core.CategoryRestoreTargetUnsupported
		message = msgTargetUnsupported
		actions = []string{msgTargetUnsupportedAction}
	}
	return core.FailCause(err, core.Diagnostic{
		Category: category, Severity: core.SeverityError, Message: message,
		Detail: err.Error(), NextActions: actions,
	})
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
		Category:    core.CategoryRestoreConsumerUnsafe,
		Severity:    core.SeverityError,
		Message:     msgConsumerUnsafe,
		Detail:      strings.Join(lines, "\n"),
		NextActions: []string{msgConsumerUnsafeAction},
	})
}

func pausedServiceError(services []string) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryRestoreConsumerUnsafe,
		Severity:    core.SeverityError,
		Message:     msgPausedUnsafe,
		Detail:      strings.Join(services, ", "),
		NextActions: []string{msgPausedUnsafeAction},
	})
}

func spaceError(needed, available int64) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryRestoreSafetyFailed,
		Severity:    core.SeverityError,
		Message:     msgSpaceInsufficient,
		Detail:      fmt.Sprintf("필요 약 %d bytes, 사용 가능 %d bytes", needed, available),
		NextActions: []string{msgSpaceInsufficientAction},
	})
}

func stopError(err error, output string) error {
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreFailed,
		Severity:    core.SeverityError,
		Message:     msgStopFailed,
		Detail:      strings.TrimSpace(output + "\n" + err.Error()),
		NextActions: []string{msgStopFailedAction},
	})
}

func safetyError(err error, path string) error {
	detail := err.Error()
	if path != "" {
		detail += "\n" + fmt.Sprintf(msgSafetyPartial, path)
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreSafetyFailed,
		Severity:    core.SeverityError,
		Message:     msgSafetyFailed,
		Detail:      detail,
		NextActions: []string{msgSafetyFailedAction},
	})
}

func replaceError(stage string, err error, output string, report *ExecutionReport) error {
	detail := failureDetail(report, "replace/"+stage)
	detail = strings.TrimSpace(detail + "\n" + output + "\n" + err.Error())
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreFailed,
		Severity:    core.SeverityError,
		Message:     msgReplaceFailed,
		Detail:      detail,
		NextActions: []string{msgReplaceFailedAction},
	})
}

func artifactChangedError(err error, report *ExecutionReport) error {
	detail := failureDetail(report, "replace/artifact")
	detail = strings.TrimSpace(detail + "\n" + err.Error())
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreIntegrityFailed,
		Severity:    core.SeverityError,
		Message:     msgIntegrityFailed,
		Detail:      detail,
		NextActions: []string{msgIntegrityFailedAction},
	})
}

func verifyError(err error, output string, report *ExecutionReport) error {
	detail := failureDetail(report, "verify")
	detail = strings.TrimSpace(detail + "\n" + output + "\n" + err.Error())
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreVerifyFailed,
		Severity:    core.SeverityError,
		Message:     msgVerifyFailed,
		Detail:      detail,
		NextActions: []string{msgVerifyFailedAction},
	})
}

func restoreStateError(output string, err error, services []string) error {
	detail := strings.TrimSpace(output + "\n" + err.Error())
	detail += "\n서비스: " + strings.Join(services, ", ")
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryRestoreStateFailed,
		Severity:    core.SeverityError,
		Message:     msgRestoreStateFailed,
		Detail:      detail,
		NextActions: []string{msgRestoreStateFailedAction},
	})
}

// failureDetail is the honest failure paragraph shared by every late failure:
// stage, data state, artifact, safety backup and recovery action.
func failureDetail(report *ExecutionReport, stage string) string {
	state := DataUntouched
	if report != nil && report.DataState != "" {
		state = report.DataState
	}
	var b strings.Builder
	fmt.Fprintf(&b, msgStageLabel+"\n", stage)
	fmt.Fprintf(&b, msgDataStateLabel+"\n", state)
	if report != nil {
		if report.ArtifactPath != "" {
			fmt.Fprintf(&b, msgArtifactLocation+"\n", report.ArtifactPath)
		}
		if report.SafetyBackup != "" {
			fmt.Fprintf(&b, msgSafetyLocationLabel+"\n", report.SafetyBackup)
		}
	}
	switch state {
	case DataUntouched:
		b.WriteString(msgRecoveryUntouched)
	case DataReplaced:
		if report != nil && report.SafetyBackup != "" {
			fmt.Fprintf(&b, msgRecoverySafety, report.SafetyBackup)
		}
	default:
		if report != nil && report.SafetyBackup != "" {
			fmt.Fprintf(&b, msgRecoverySafety, report.SafetyBackup)
		} else {
			b.WriteString(msgRecoveryNoSafety)
		}
	}
	return strings.TrimSpace(b.String())
}

// redactedCause keeps the error chain (notably context.Canceled) while
// replacing the message with a redacted copy for diagnostics.
type redactedErr struct {
	message string
	cause   error
}

func (e redactedErr) Error() string { return e.message }
func (e redactedErr) Unwrap() error { return e.cause }

func redactedCause(err error, message string) error {
	if err == nil {
		return nil
	}
	return redactedErr{message: message, cause: err}
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
		Category: core.CategoryRestoreFailed,
		Severity: core.SeverityError,
		Message:  base.Error() + "; " + extra.Error(),
	})
}

// lastReport is a tiny concurrency-safe holder used for cancellation text.
type reportHolder struct {
	mu  sync.Mutex
	rep ExecutionReport
}

func (h *reportHolder) set(rep ExecutionReport) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rep = rep
}

func (h *reportHolder) get() ExecutionReport {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rep
}
