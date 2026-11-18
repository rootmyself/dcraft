// Package lifecycle implements the four flat stack lifecycle commands for
// both interfaces: up, stop, down and restart. It owns automatic project-name
// allocation, local stack ownership identity and same-stack change
// coordination, all through the published core, stack and state boundaries.
//
// It deliberately does not build, pull, check health, run hooks, back up data
// or roll back. Those are later tickets; this package only starts, stops,
// removes and restarts an existing Compose stack.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

// Action is one lifecycle operation. The values are stable machine data.
type Action string

const (
	ActionUp      Action = "up"
	ActionStop    Action = "stop"
	ActionDown    Action = "down"
	ActionRestart Action = "restart"
)

// Valid reports whether the action is one of the four lifecycle operations.
func (a Action) Valid() bool {
	switch a {
	case ActionUp, ActionStop, ActionDown, ActionRestart:
		return true
	default:
		return false
	}
}

// Korean returns the Korean action name used in guidance.
func (a Action) Korean() string {
	switch a {
	case ActionUp:
		return "시작"
	case ActionStop:
		return "중지"
	case ActionDown:
		return "제거"
	case ActionRestart:
		return "재시작"
	default:
		return string(a)
	}
}

// Stable stage names published for both interfaces and machine consumers.
const (
	StageResolveProject = "resolve_project"
	StageAllocateName   = "allocate_name"
	StageCoordinate     = "coordinate"
	StageObserve        = "observe"
)

// Report is the machine-readable data of a successful lifecycle operation. It
// is published under data.lifecycle. VolumesRemoved is always false: these
// commands never delete data volumes.
type Report struct {
	Action            string          `json:"action"`
	ProjectName       string          `json:"project_name"`
	ProjectNameSource string          `json:"project_name_source"`
	Scope             string          `json:"scope"`
	Services          []string        `json:"services"`
	VolumesRemoved    bool            `json:"volumes_removed"`
	ConfigApplied     bool            `json:"config_applied"`
	State             *stack.Snapshot `json:"state,omitempty"`
	Notices           []string        `json:"notices"`
}

// Payload is the feature result payload. JSON consumers read data.lifecycle.
type Payload struct {
	Lifecycle Report `json:"lifecycle"`
}

// Controller is the shared implementation consumed by all four features.
type Controller struct {
	life     stack.Lifecycle
	identity stack.Identity
	backend  stack.Backend
	store    *state.Store
	clock    core.Clock
	pid      int
}

// New builds the controller. All boundaries are narrow interfaces or the
// per-computer store; the Compose adapter supplies lifecycle, identity and
// inspection.
func New(life stack.Lifecycle, identity stack.Identity, backend stack.Backend, store *state.Store, clock core.Clock) *Controller {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &Controller{
		life: life, identity: identity, backend: backend,
		store: store, clock: clock, pid: os.Getpid(),
	}
}

// Run executes one lifecycle operation. Stages are reported in order:
// resolve_project, coordinate, allocate_name, <action>, observe. The change
// lock is taken before the name claim so a blocked operation changes nothing.
func (c *Controller) Run(ctx context.Context, in core.Invocation, action Action) (Payload, error) {
	if !action.Valid() {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgUnknownAction,
			NextActions: []string{msgUnknownActionNext},
		})
	}
	if c.life == nil || c.identity == nil || c.backend == nil || c.store == nil {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoLifecycle,
			NextActions: []string{msgNoLifecycleNext},
		})
	}
	clock := c.clock
	if in.Clock != nil {
		clock = in.Clock
	}
	target := in.Target
	var notices []string

	services, err := normalizeServices(in.Services)
	if err != nil {
		return Payload{}, err
	}
	if action == ActionDown && len(services) > 0 {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgDownIsProjectWide,
			NextActions: []string{msgDownIsProjectWideNext},
		})
	}

	declared := ""
	declaredSource := state.NameSource("")
	base := ""
	var allocation state.NameAllocation

	err = core.RunStage(in.Reporter, StageResolveProject, msgStageResolveProject, func() error {
		cfg, err := c.identity.ConfigInfo(ctx, target)
		if err != nil {
			return err
		}
		if err := validateServices(services, cfg.Services); err != nil {
			return err
		}
		explicit := strings.TrimSpace(target.ProjectName) != ""
		if explicit {
			declared = cfg.EffectiveName
			if declared == "" {
				declared = strings.TrimSpace(target.ProjectName)
			}
			declaredSource = state.NameSourceExplicit
			return nil
		}
		derived, err := c.identity.DerivedProjectName(ctx, target)
		if err != nil {
			return err
		}
		base = derived
		// Compose reports a resolved name even when the folder default was
		// used. A name that differs from Compose's own derived default was
		// declared by the user or environment and must be honored.
		if cfg.EffectiveName != "" && cfg.EffectiveName != derived {
			declared = cfg.EffectiveName
			declaredSource = state.NameSourceConfigured
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}

	// The change lock comes before the name claim. The lock key is already
	// known (project folder + environment), so a blocked operation fails as
	// stack_busy without persisting a rename claim that the next run would
	// adopt as a second project.
	var lock *state.StackLock
	err = core.RunStage(in.Reporter, StageCoordinate, msgStageCoordinate, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		lock, err = c.store.AcquireStackLock(ctx, target, state.LockHolder{
			Action: string(action), PID: c.pid, StartedAt: clock.Now(),
		})
		if err == nil {
			return nil
		}
		var busy *state.BusyError
		if errors.As(err, &busy) {
			return core.Fail(busyDiagnostic(busy))
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return core.FailCause(err, stateFailedDiagnostic(err))
	})
	if err != nil {
		return Payload{}, err
	}
	defer func() { _ = lock.Release() }()

	err = core.RunStage(in.Reporter, StageAllocateName, msgStageAllocateName, func() error {
		projects, err := c.identity.Projects(ctx)
		if err != nil {
			return err
		}
		external := make([]state.ExternalProject, 0, len(projects))
		for _, p := range projects {
			external = append(external, state.ExternalProject{Name: p.Name, ProjectDir: p.ProjectDir})
		}
		allocation, err = c.store.AllocateName(ctx, state.NameRequest{
			Target:         target,
			DeclaredName:   declared,
			DeclaredSource: declaredSource,
			BaseName:       base,
			External:       external,
		})
		if err != nil {
			return translateNameError(err, target)
		}
		if allocation.PreviousName != "" && allocation.PreviousName != allocation.Name {
			notices = append(notices, fmt.Sprintf(msgRenameNotice, allocation.PreviousName, allocation.Name))
		}
		return nil
	})
	if err != nil {
		return Payload{}, err
	}

	opTarget := target
	opTarget.ProjectName = allocation.Name

	actionTitle := stageTitle(action)
	var opResult stack.OpResult
	err = core.RunStage(in.Reporter, string(action), actionTitle, func() error {
		var callErr error
		switch action {
		case ActionUp:
			opResult, callErr = c.life.Up(ctx, opTarget, services)
		case ActionStop:
			opResult, callErr = c.life.Stop(ctx, opTarget, services)
		case ActionDown:
			opResult, callErr = c.life.Down(ctx, opTarget)
		case ActionRestart:
			opResult, callErr = c.life.Restart(ctx, opTarget, services)
		}
		if callErr == nil {
			return nil
		}
		if ctx.Err() != nil || errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			return callErr
		}
		return core.FailCause(callErr, c.failureDiagnostic(ctx, opTarget, action, opResult, callErr))
	})
	if err != nil {
		return Payload{}, err
	}
	notices = append(notices, opResult.Notices...)

	var snapshot *stack.Snapshot
	observeErr := core.RunStage(in.Reporter, StageObserve, msgStageObserve, func() error {
		snap, inspectErr := c.backend.Inspect(ctx, opTarget)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if inspectErr != nil {
			notices = append(notices, msgObserveFailed)
			return inspectErr
		}
		snapshot = &snap
		return nil
	})
	if observeErr != nil {
		if ctx.Err() != nil || errors.Is(observeErr, context.Canceled) || errors.Is(observeErr, context.DeadlineExceeded) {
			return Payload{}, observeErr
		}
		// The stack change already happened; a failed observation is one
		// failed stage plus a notice, not a false operation failure.
	}

	scope := "stack"
	selected := []string{}
	if len(services) > 0 {
		scope = "services"
		selected = append(selected, services...)
	}
	if notices == nil {
		notices = []string{}
	}
	return Payload{Lifecycle: Report{
		Action:            string(action),
		ProjectName:       allocation.Name,
		ProjectNameSource: string(allocation.Source),
		Scope:             scope,
		Services:          selected,
		VolumesRemoved:    false,
		ConfigApplied:     action == ActionUp,
		State:             snapshot,
		Notices:           notices,
	}}, nil
}

func normalizeServices(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, value := range raw {
		name := strings.TrimSpace(value)
		if name == "" {
			return nil, core.Fail(core.Diagnostic{
				Category:    core.CategoryServiceUnknown,
				Severity:    core.SeverityError,
				Message:     msgEmptyService,
				NextActions: []string{msgEmptyServiceNext},
			})
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func validateServices(selected, configured []string) error {
	if len(selected) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, name := range configured {
		known[name] = true
	}
	var unknown []string
	for _, name := range selected {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryServiceUnknown,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgUnknownService, strings.Join(unknown, ", ")),
		Detail:      msgAvailableServices + strings.Join(configured, ", "),
		NextActions: []string{msgUnknownServiceNextOne, msgUnknownServiceNextTwo},
	})
}

func translateNameError(err error, target core.Target) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var conflict *state.NameConflictError
	if errors.As(err, &conflict) {
		return core.Fail(nameConflictDiagnostic(conflict))
	}
	return core.FailCause(err, stateFailedDiagnostic(err))
}

func nameConflictDiagnostic(conflict *state.NameConflictError) core.Diagnostic {
	owner := conflict.OwnerDir
	if conflict.OwnerEnvironment != "" {
		owner += " (환경 " + conflict.OwnerEnvironment + ")"
	}
	if conflict.External {
		owner += " (dcraft 관리 밖의 기존 Compose 프로젝트)"
	}
	diag := core.Diagnostic{
		Category: core.CategoryProjectNameConflict,
		Severity: core.SeverityError,
		Message:  fmt.Sprintf(msgNameConflict, conflict.Name),
		Detail:   owner,
		NextActions: []string{
			msgNameConflictNextOne,
			msgNameConflictNextTwo,
			msgNameConflictNextThree,
		},
	}
	if conflict.Target.ProjectDir != "" {
		diag.Detail = fmt.Sprintf("요청한 폴더: %s; 사용 중인 폴더: %s", conflict.Target.ProjectDir, owner)
	}
	return diag
}

func busyDiagnostic(busy *state.BusyError) core.Diagnostic {
	detail := busy.Error()
	if busy.HasHolder && busy.Holder.StartedAt.IsZero() {
		detail = fmt.Sprintf("진행 중인 작업: %s (PID %d)", busy.Holder.Action, busy.Holder.PID)
	}
	return core.Diagnostic{
		Category:    core.CategoryStackBusy,
		Severity:    core.SeverityError,
		Message:     msgStackBusy,
		Detail:      detail,
		NextActions: []string{msgStackBusyNextOne, msgStackBusyNextTwo},
	}
}

func stateFailedDiagnostic(err error) core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryStateFailed,
		Severity:    core.SeverityError,
		Message:     msgStateFailed,
		Detail:      truncate(err.Error(), 500),
		NextActions: []string{msgStateFailedNextOne, msgStateFailedNextTwo},
	}
}

func (c *Controller) failureDiagnostic(ctx context.Context, target core.Target, action Action, result stack.OpResult, cause error) core.Diagnostic {
	output := result.Output
	notices := result.Notices
	stateLine := msgStateUnreadable
	if snap, err := c.backend.Inspect(ctx, target); err == nil && ctx.Err() == nil {
		stateLine = msgStateLed + summarizeState(snap)
	}
	detail := strings.TrimSpace(output)
	if detail == "" {
		detail = cause.Error()
	}
	if len(notices) > 0 {
		detail = strings.Join(notices, "\n") + "\n" + detail
	}
	return core.Diagnostic{
		Category:    core.CategoryLifecycleFailed,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgLifecycleFailed, action.Korean(), stateLine),
		Detail:      truncate(detail, 800),
		NextActions: recoveryActions(action),
	}
}

func stageTitle(action Action) string {
	switch action {
	case ActionUp:
		return "스택 시작"
	case ActionStop:
		return "스택 중지"
	case ActionDown:
		return "스택 제거"
	case ActionRestart:
		return "스택 재시작"
	default:
		return "스택 작업"
	}
}

func recoveryActions(action Action) []string {
	switch action {
	case ActionUp:
		return []string{msgRecoverUpOne, msgRecoverUpTwo, msgRecoverUpThree}
	case ActionStop:
		return []string{msgRecoverStopOne, msgRecoverStopTwo}
	case ActionDown:
		return []string{msgRecoverDownOne, msgRecoverDownTwo, msgRecoverDownThree}
	case ActionRestart:
		return []string{msgRecoverRestartOne, msgRecoverRestartTwo}
	default:
		return []string{msgRecoverGeneric}
	}
}

func summarizeState(snap stack.Snapshot) string {
	s := snap.Summary
	if s.Services == 0 && s.Instances == 0 {
		return msgStateEmpty
	}
	parts := []string{
		fmt.Sprintf("서비스 %d개", s.Services),
		fmt.Sprintf("실행 중 %d개", s.Running),
	}
	if s.Stopped > 0 {
		parts = append(parts, fmt.Sprintf("중지 %d개", s.Stopped))
	}
	if s.NotCreated > 0 {
		parts = append(parts, fmt.Sprintf("컨테이너 없음 %d개", s.NotCreated))
	}
	if s.Healthy > 0 {
		parts = append(parts, fmt.Sprintf("healthy %d개", s.Healthy))
	}
	if s.Unverified > 0 {
		parts = append(parts, fmt.Sprintf("앱 확인 안 됨 %d개", s.Unverified))
	}
	if s.Unhealthy > 0 {
		parts = append(parts, fmt.Sprintf("unhealthy %d개", s.Unhealthy))
	}
	return strings.Join(parts, " · ")
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}
