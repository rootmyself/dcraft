// Package rollback implements the explicit `dcraft rollback` operation: return
// to the single previous verified-good deployment recorded by `dcraft update`.
//
// It restores images and the recorded execution configuration only. It never
// restores DB/volume data, external systems or script side effects, never runs
// deploy hooks, never pulls or builds, and never invents a past version.
package rollback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/readiness"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// Stable stage names published for both interfaces and machine consumers.
const (
	StageLoadRecord = "load_record"
	StagePrecheck   = "precheck"
	StageApply      = "apply"
	StageVerify     = "verify"
	StageReport     = "report"
)

// lockStore is the part of the per-computer state store rollback needs. The
// concrete *state.Store implements it; tests use the real store with a temp
// directory so lock and record behavior are not mocked.
type lockStore interface {
	AcquireStackLock(ctx context.Context, target core.Target, holder state.LockHolder) (*state.StackLock, error)
	LoadGoodVersion(target core.Target) (state.GoodVersion, bool, error)
}

// Controller owns rollback behavior. CLI and TUI both reach it through the
// common engine; the interfaces never reimplement a safety rule.
type Controller struct {
	updater  stack.Updater
	applier  stack.Rollbacker
	identity stack.Identity
	getenv   func(string) string
	store    core.StateStore
	clock    core.Clock
	pid      int
	// awsChecker proves a recorded or current AWS reference is currently
	// resolvable. It returns no value. The application wires *secretref.Runtime.
	awsChecker AWSSecretChecker

	// Test seams: the readiness loop uses the injected clock and sleep so the
	// bounded behavior is exercised without real waits.
	poll   time.Duration
	settle time.Duration
	now    func() time.Time
	sleep  func(time.Duration)
}

// New builds the controller from the published narrow boundaries.
func New(updater stack.Updater, applier stack.Rollbacker, identity stack.Identity, getenv func(string) string, store core.StateStore, clock core.Clock) *Controller {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return &Controller{
		updater:  updater,
		applier:  applier,
		identity: identity,
		getenv:   getenv,
		store:    store,
		clock:    clock,
		pid:      os.Getpid(),
		now:      time.Now,
	}
}

// AWSSecretChecker proves an AWS reference is currently resolvable without
// returning or persisting the value. *secretref.Runtime implements it.
type AWSSecretChecker interface {
	CheckAWS(ctx context.Context, ref secretref.Reference) error
}

// SetAWSSecretChecker wires the AWS availability boundary. It is optional for
// projects without AWS references and for tests.
func (c *Controller) SetAWSSecretChecker(checker AWSSecretChecker) { c.awsChecker = checker }

// Run executes one previous-good rollback. Stages: load_record, precheck,
// apply, verify, report. The record is read under the same per-stack lock as
// update and the lock is held until the run finishes.
func (c *Controller) Run(ctx context.Context, in core.Invocation, opts Options) (Payload, error) {
	if c == nil || c.updater == nil || c.applier == nil || c.identity == nil || c.store == nil {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoController,
			NextActions: []string{msgNoControllerAct},
		})
	}
	ls, ok := c.store.(lockStore)
	if !ok {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoState,
			NextActions: []string{msgNoStateAct},
		})
	}
	opts = opts.withDefaults()
	clock := c.clock
	if in.Clock != nil {
		clock = in.Clock
	}

	var lock *state.StackLock
	var rec state.GoodVersion
	err := core.RunStage(in.Reporter, StageLoadRecord, msgStageLoadRecord, func() error {
		var err error
		lock, err = ls.AcquireStackLock(ctx, in.Target, state.LockHolder{
			Action: "rollback", PID: c.pid, StartedAt: clock.Now(),
		})
		if err != nil {
			var busy *state.BusyError
			if errors.As(err, &busy) {
				return core.Fail(stackBusyDiagnostic(busy))
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return core.FailCause(err, stateFailedDiagnostic(err))
		}
		var found bool
		rec, found, err = ls.LoadGoodVersion(in.Target)
		if err != nil {
			return core.FailCause(err, stateFailedDiagnostic(err))
		}
		if !found {
			return core.Fail(noRecordDiagnostic(in.Target))
		}
		if err := validateRecord(rec, in.Target); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}
	defer func() { _ = lock.Release() }()

	var p plan
	err = core.RunStage(in.Reporter, StagePrecheck, msgStagePrecheck, func() error {
		var err error
		p, err = c.precheck(ctx, in, rec)
		return err
	})
	if err != nil {
		return Payload{}, err
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return Payload{}, ctxErr
	}

	// The apply stage is managed manually so the observed state is attached to
	// the stage even when the Compose process is interrupted; RunStage cannot
	// carry a detail for a canceled stage.
	if in.Reporter != nil {
		in.Reporter.Begin(StageApply, msgStageApply)
	}
	applyResult, applyErr := c.applier.ApplyRollback(ctx, p.target, p.services, p.pins, recordedSecretRefs(rec))
	if applyErr != nil {
		if ctx.Err() != nil || errors.Is(applyErr, context.Canceled) || errors.Is(applyErr, context.DeadlineExceeded) {
			detail := c.observedStateAfterCancel(p.target)
			if in.Reporter != nil {
				in.Reporter.End(StageApply, core.StageCanceled, detail)
			}
			return Payload{}, ctxError(ctx, applyErr)
		}
		detail := c.observedState(ctx, p.target)
		if in.Reporter != nil {
			in.Reporter.End(StageApply, core.StageFailed, detail)
		}
		return Payload{}, c.applyFailureWithState(applyErr, applyResult.Output, detail)
	}
	if in.Reporter != nil {
		in.Reporter.End(StageApply, core.StageSucceeded, "")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Payload{}, ctxErr
	}

	if in.Reporter != nil {
		in.Reporter.Begin(StageVerify, msgStageVerify)
	}
	outcome, err := c.observe(ctx, p.target, p.defs, p.baseline, opts.Timeout)
	if err != nil {
		if in.Reporter != nil {
			status := core.StageFailed
			if isCanceled(ctx, err) {
				status = core.StageCanceled
			}
			in.Reporter.End(StageVerify, status, "")
		}
		return Payload{}, ctxError(ctx, err)
	}
	if mismatches := imageMismatches(p.pins, outcome.Services); len(mismatches) > 0 {
		if in.Reporter != nil {
			in.Reporter.End(StageVerify, core.StageFailed, strings.Join(mismatches, "; "))
		}
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryRollbackVerifyFailed,
			Severity:    core.SeverityError,
			Message:     msgVerifyImageMismatch,
			Detail:      truncate(strings.Join(mismatches, "; "), 900),
			NextActions: []string{msgVerifyAct1, msgVerifyAct2},
		})
	}
	if !readiness.AllSatisfied(outcome.Services) {
		if in.Reporter != nil {
			in.Reporter.End(StageVerify, core.StageFailed, readiness.Summary(outcome.Services))
		}
		return Payload{}, readinessFailure(outcome, opts.Timeout)
	}
	if in.Reporter != nil {
		in.Reporter.End(StageVerify, core.StageSucceeded, readiness.Summary(outcome.Services))
	}

	var payload Payload
	err = core.RunStage(in.Reporter, StageReport, msgStageReport, func() error {
		notices := append([]string{}, p.notices...)
		notices = append(notices, msgPlanRecordNote, msgPlanNotRestoredValue)
		payload = Payload{Rollback: Report{
			ProjectName:      rec.Target.ProjectName,
			Environment:      rec.Target.Environment,
			Scope:            "stack",
			Services:         append([]string{}, p.services...),
			Files:            append([]string{}, rec.Target.Files...),
			EnvFiles:         append([]string{}, rec.Target.EnvFiles...),
			Profiles:         append([]string{}, rec.Target.Profiles...),
			RecordedAt:       rec.RecordedAt,
			Images:           append([]stack.RollbackPin{}, p.pins...),
			Applied:          true,
			Verified:         outcome.Verified,
			ImageIDsVerified: true,
			AlreadyAtVersion: p.alreadyAtVersion,
			Readiness:        outcome.Services,
			RecordStatus:     RecordUnchanged,
			DataRestored:     false,
			HooksRun:         false,
			Notices:          nonNil(notices),
		}}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}
	return payload, nil
}

// applyFailureWithState reports a failed apply with the state already observed
// for the stage. Tool output may contain values echoed by Compose, so it is
// truncated before it becomes a diagnostic, exactly like update.
func (c *Controller) applyFailureWithState(cause error, toolOutput, observed string) error {
	var output []string
	if strings.TrimSpace(toolOutput) != "" {
		output = append(output, strings.TrimSpace(toolOutput))
	}
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		output = append(output, cause.Error())
	}
	detail := strings.TrimSpace(strings.Join(output, "\n") + "\n" + observed)
	return core.FailCause(cause, core.Diagnostic{
		Category:    core.CategoryRollbackFailed,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgApplyFailed, msgStageApply),
		Detail:      truncate(detail, 900),
		NextActions: []string{msgApplyAct1, msgApplyAct2, msgApplyAct3},
	})
}

// observedStateAfterCancel reads the current state with a short independent
// context so a canceled operation can still report what it left behind.
func (c *Controller) observedStateAfterCancel(target core.Target) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.observedState(ctx, target)
}

func (c *Controller) observedState(ctx context.Context, target core.Target) string {
	containers, err := c.updater.Containers(ctx, target)
	if err != nil || ctx.Err() != nil {
		return msgStateUnknown
	}
	if len(containers) == 0 {
		return msgStateNoContainer
	}
	running, restarts, exited := 0, 0, 0
	for _, container := range containers {
		if container.Running {
			running++
		}
		restarts += container.RestartCount
		if strings.EqualFold(container.State, "exited") {
			exited++
		}
	}
	return fmt.Sprintf("컨테이너 %d개 중 실행 %d개 · 재시작 합계 %d회 · 종료 %d개", len(containers), running, restarts, exited)
}

func isCanceled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return ctx.Err() != nil
}

func ctxError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}
