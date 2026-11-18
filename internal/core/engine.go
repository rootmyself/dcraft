package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Engine is the common operation seam. CLI and TUI only construct a Request
// and render a Result; target resolution, preflight, approval policy, staged
// execution, verification, history and failure reporting all happen here.
type Engine struct {
	Registry  *Registry
	Resolver  Resolver
	Preflight Preflight
	State     StateStore
	Clock     Clock
	// Identity resolves the effective project name for read-only features. It
	// is strictly read-only; nil disables the lookup (tests and alternate
	// wiring keep the previous behavior).
	Identity IdentityReader
	// Hooks is the registered host-script service. It is consulted only for
	// features that declare a HookScope; nil is a wiring error for them.
	Hooks HookService
	// CWD returns the process working directory. Injected for tests.
	CWD func() (string, error)
	// NewOperationID returns the operation identifier.
	NewOperationID func() string
}

// NewEngine builds an engine with production defaults for clock, working
// directory and operation IDs. Tests override the exported fields.
func NewEngine(registry *Registry, resolver Resolver, preflight Preflight, state StateStore, clock Clock) *Engine {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Engine{
		Registry:       registry,
		Resolver:       resolver,
		Preflight:      preflight,
		State:          state,
		Clock:          clock,
		CWD:            os.Getwd,
		NewOperationID: generateOperationID,
	}
}

// SupportsEnvironments reports whether the configured resolver can list named
// environment aliases. The TUI uses it to decide whether a selection screen is
// possible; the resolver remains the single source of the catalog.
func (e *Engine) SupportsEnvironments() bool {
	if e == nil {
		return false
	}
	_, ok := e.Resolver.(EnvironmentSource)
	return ok
}

// Environments lists the aliases registered for the target folder. It performs
// no preflight and no stack change. A resolver without environment support
// yields an empty list, which keeps the no-alias Compose defaults intact.
func (e *Engine) Environments(ctx context.Context, req TargetRequest) ([]Environment, []Diagnostic, error) {
	source, ok := e.Resolver.(EnvironmentSource)
	if !ok {
		return nil, nil, nil
	}
	cwd, err := e.cwd()
	if err != nil {
		return nil, nil, FailCause(err, Diagnostic{
			Category:    CategoryTargetInvalid,
			Severity:    SeverityError,
			Message:     msgWorkingDirUnknown,
			NextActions: []string{msgWorkingDirAction},
		})
	}
	return source.Environments(ctx, req, cwd)
}

// Run executes one operation. It never returns a Go error: every outcome is
// expressed in the Result so both interfaces render the same truth.
func (e *Engine) Run(ctx context.Context, req Request, sink Sink) (res Result) {
	clock := e.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	res = Result{
		SchemaVersion: SchemaVersion,
		OperationID:   e.operationID(),
		Action:        req.Feature,
		Outcome:       OutcomeFailure,
		Target:        Target{ProjectDir: req.Target.Path, Environment: req.Target.Environment},
		Stages:        []Stage{},
		Diagnostics:   []Diagnostic{},
		StartedAt:     clock.Now(),
	}
	res.Target.Normalize()
	defer func() {
		res.Target.Normalize()
		res.FinishedAt = clock.Now()
		if res.Stages == nil {
			res.Stages = []Stage{}
		}
		if res.Diagnostics == nil {
			res.Diagnostics = []Diagnostic{}
		}
	}()

	rec := &recorder{stages: &res.Stages, clock: clock, sink: sink}

	if err := ctx.Err(); err != nil {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnostic(&res)
		return res
	}

	feature, ok := e.lookup(req.Feature)
	if !ok {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     fmt.Sprintf("등록되지 않은 작업입니다: %s", req.Feature),
			NextActions: []string{"dcraft --help로 사용 가능한 명령을 확인하세요."},
		})
		return res
	}
	desc := feature.Descriptor()

	if err := ctx.Err(); err != nil {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnostic(&res)
		return res
	}

	var target Target
	var warnings []Diagnostic
	resolveErr := e.resolveTarget(ctx, rec, req, desc, &target, &warnings)
	if len(target.ProjectDir) > 0 || target.Environment != "" || len(target.Files) > 0 {
		res.Target = target
	}
	res.Diagnostics = append(res.Diagnostics, warnings...)
	if resolveErr != nil {
		e.applyFailure(ctx, &res, resolveErr, nil)
		return res
	}

	// A wizard "steps" run only collects answers for a later, explicitly
	// approved "apply" run. It returns a WizardSpec and performs no stack
	// change, so both the descriptor and conditional approvals belong to
	// apply, not to the menu preview. The TUI routes an approval-gated apply
	// through the generic plan + confirmation screen.
	required, approvalErr := e.requiredApproval(ctx, req, desc, feature, target)
	if approvalErr != nil {
		e.applyFailure(ctx, &res, approvalErr, feature)
		return res
	}
	if required != ApprovalNone {
		if err := ctx.Err(); err != nil {
			res.Outcome = OutcomeCanceled
			addCanceledDiagnostic(&res)
			return res
		}
		res.RequiredApproval = required
		res.Outcome = OutcomeFailure
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Category:    CategoryApprovalRequired,
			Severity:    SeverityError,
			Message:     approvalRequiredMessage(required),
			Detail:      string(required),
			NextActions: approvalRequiredActions(required),
		})
		return res
	}

	var pf PreflightReport
	preflightErr := e.runPreflight(ctx, rec, desc, &pf)
	res.Tools = pf.Tools
	if preflightErr != nil {
		if isCanceled(ctx, preflightErr) {
			res.Outcome = OutcomeCanceled
			addCanceledDiagnostic(&res)
			return res
		}
		res.Diagnostics = append(res.Diagnostics, pf.Diagnostics...)
		res.Outcome = OutcomeFailure
		if !HasError(res.Diagnostics) {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Category:    CategoryInternal,
				Severity:    SeverityError,
				Message:     msgPreflightFailed,
				Detail:      errDetail(preflightErr),
				NextActions: []string{msgPreflightFailedAction},
			})
		}
		return res
	}
	res.Diagnostics = append(res.Diagnostics, pf.Diagnostics...)

	// Read-only features must identify the same stack as stack-changing
	// features. Resolve the recorded/declared project name without writing or
	// locking anything; no stage is added because the target is already known.
	if desc.ReadOnly && e.Identity != nil && strings.TrimSpace(target.ProjectName) == "" {
		name, identErr := e.Identity.ReadProjectName(ctx, target)
		if ctxErr := ctx.Err(); ctxErr != nil {
			res.Outcome = OutcomeCanceled
			addCanceledDiagnostic(&res)
			return res
		}
		if identErr != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Category:    CategoryIdentityUnreadable,
				Severity:    SeverityWarning,
				Message:     msgIdentityUnreadable,
				Detail:      errDetail(identErr),
				NextActions: []string{msgIdentityUnreadableActionOne, msgIdentityUnreadableActionTwo},
			})
		} else if name != "" {
			target.ProjectName = name
			res.Target.ProjectName = name
		}
	}

	inv := Invocation{
		Target:      target,
		Services:    append([]string{}, req.Services...),
		Args:        append([]string{}, req.Args...),
		Options:     req.Options,
		OptionLists: req.OptionLists,
		Interactive: req.Interactive,
		Preflight:   pf,
		IO:          req.IO,
		JSON:        req.JSON,
		Reporter:    rec,
		Clock:       clock,
		State:       e.State,
	}

	hookPlan, hookErr := e.prepareHooks(ctx, rec, req, desc, clock, &res, &target)
	if hookErr != nil {
		e.applyFailure(ctx, &res, hookErr, feature)
		return res
	}
	inv.Target = target

	stageCountBefore := len(res.Stages)
	payload, runErr := e.runFeature(ctx, feature, inv)
	if len(res.Stages) == stageCountBefore {
		rec.Begin("execute", msgStageExecute)
		status := StageSucceeded
		if runErr != nil {
			status = StageFailed
			if isCanceled(ctx, runErr) {
				status = StageCanceled
			}
		}
		rec.End("execute", status, "")
	}
	if runErr != nil {
		e.applyFailure(ctx, &res, runErr, feature)
		return res
	}
	if ctx.Err() != nil {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnostic(&res)
		return res
	}

	if verifier, ok := feature.(Verifier); ok {
		verifyErr := RunStage(rec, "verify", msgStageVerify, func() error {
			return runVerify(ctx, verifier, inv, payload)
		})
		if verifyErr != nil {
			if isCanceled(ctx, verifyErr) {
				res.Outcome = OutcomeCanceled
				addCanceledDiagnostic(&res)
				return res
			}
			res.Outcome = OutcomeFailure
			res.Diagnostics = append(res.Diagnostics, verifyFailureDiagnostics(verifyErr)...)
			return res
		}
	}

	if ctx.Err() != nil {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnostic(&res)
		return res
	}

	// Data describes a completed feature result. It is attached only after
	// verification, cancellation and post-hook checks, so a failure or
	// cancellation can never carry a payload that looks like a success.
	if canceled := e.runHooksPost(ctx, rec, &res, res.Action, res.OperationID, target, hookEntriesForStage(hookPlan, HookStagePost)); canceled {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnostic(&res)
		return res
	}

	res.Data = payload

	// A wizard "steps" run only collects answers; the "apply" run is the
	// actual operation. Recording the preview would write a success history
	// entry for work the user has not confirmed yet.
	if desc.RecordsHistory && req.Options["wizard"] != "steps" {
		historyErr := RunStage(rec, "history", msgStageHistory, func() error {
			if e.State == nil {
				return errors.New("state store is not configured")
			}
			return e.State.AppendOperation(target, OperationRecord{
				OperationID: res.OperationID,
				Action:      res.Action,
				Outcome:     OutcomeSuccess,
				StartedAt:   res.StartedAt,
				FinishedAt:  clock.Now(),
				Stages:      historyStages(res.Stages),
				Diagnostics: append([]Diagnostic{}, res.Diagnostics...),
			})
		})
		if historyErr != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Category:    CategoryHistoryFailed,
				Severity:    SeverityWarning,
				Message:     msgHistoryFailed,
				Detail:      errDetail(historyErr),
				NextActions: []string{msgHistoryFailedAction},
			})
		}
	}

	res.Outcome = OutcomeSuccess
	return res
}

// resolveTarget runs the shared resolve_target stage. A feature marked
// TargetOptional resolves a folder without Compose files; every other feature
// keeps the full requirement.
func (e *Engine) resolveTarget(ctx context.Context, rec *recorder, req Request, desc Descriptor, target *Target, warnings *[]Diagnostic) error {
	return RunStage(rec, "resolve_target", msgStageResolveTarget, func() error {
		if e.Resolver == nil {
			return errors.New("target resolver is not configured")
		}
		cwd, err := e.cwd()
		if err != nil {
			return FailCause(err, Diagnostic{
				Category:    CategoryTargetInvalid,
				Severity:    SeverityError,
				Message:     msgWorkingDirUnknown,
				NextActions: []string{msgWorkingDirAction},
			})
		}
		targetReq := req.Target
		targetReq.AllowMissingConfiguration = desc.TargetOptional
		*target, *warnings, err = e.Resolver.Resolve(ctx, targetReq, cwd)
		return err
	})
}

// requiredApproval returns the independent approval a run is missing. The
// descriptor approval is checked first; a feature that implements
// ConditionalApproval is then asked whether its particular inputs need another
// one. A wizard "steps" run is the read-only preview that collects those
// answers, so it is exempt from the conditional check; the "apply" run is not.
func (e *Engine) requiredApproval(ctx context.Context, req Request, desc Descriptor, feature Feature, target Target) (ApprovalKind, error) {
	wizardSteps := desc.Wizard && req.Options["wizard"] == "steps"
	if required := desc.Approval; required != ApprovalNone && !wizardSteps && !HasApproval(req.Approvals, required) {
		return required, nil
	}
	conditional, ok := feature.(ConditionalApproval)
	if !ok || wizardSteps {
		return ApprovalNone, nil
	}
	kind, err := e.conditionalApproval(ctx, conditional, req, target)
	if err != nil {
		return ApprovalNone, err
	}
	if kind != ApprovalNone && !HasApproval(req.Approvals, kind) {
		return kind, nil
	}
	return ApprovalNone, nil
}

// conditionalApproval isolates a feature's read-only approval inspection from
// the engine: a panic becomes an internal error instead of crashing the UI.
func (e *Engine) conditionalApproval(ctx context.Context, feature ConditionalApproval, req Request, target Target) (kind ApprovalKind, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &recoveredPanicError{value: recovered}
		}
	}()
	inv := Invocation{
		Target:      target,
		Services:    append([]string{}, req.Services...),
		Args:        append([]string{}, req.Args...),
		Options:     req.Options,
		OptionLists: req.OptionLists,
		Interactive: req.Interactive,
		IO:          req.IO,
		JSON:        req.JSON,
		Clock:       e.Clock,
		State:       e.State,
	}
	return feature.RequiredApproval(ctx, inv)
}

// runPreflight checks exactly the capabilities the descriptor declared.
func (e *Engine) runPreflight(ctx context.Context, rec *recorder, desc Descriptor, pf *PreflightReport) error {
	return RunStage(rec, "preflight", msgStagePreflight, func() error {
		if e.Preflight == nil {
			return errors.New("preflight is not configured")
		}
		*pf = e.Preflight.Check(ctx, NormalizeRequirements(desc.Requires))
		if err := ctx.Err(); err != nil {
			return err
		}
		if HasError(pf.Diagnostics) {
			return Fail(pf.Diagnostics...)
		}
		return nil
	})
}

// Plan resolves the same target and preflight as Run and asks the feature for
// a read-only preview. It never consumes an approval and never calls Run, so
// an interactive interface can show the exact effect before collecting the
// approval. It returns a report instead of a Go error for the same reason Run
// does.
func (e *Engine) Plan(ctx context.Context, req Request, sink Sink) (report PlanReport) {
	clock := e.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	report = PlanReport{
		Feature:     req.Feature,
		Outcome:     OutcomeFailure,
		Target:      Target{ProjectDir: req.Target.Path, Environment: req.Target.Environment},
		Stages:      []Stage{},
		Diagnostics: []Diagnostic{},
	}
	report.Target.Normalize()
	defer func() {
		report.Target.Normalize()
		if report.Stages == nil {
			report.Stages = []Stage{}
		}
		if report.Diagnostics == nil {
			report.Diagnostics = []Diagnostic{}
		}
	}()

	rec := &recorder{stages: &report.Stages, clock: clock, sink: sink}
	if ctx.Err() != nil {
		report.Outcome = OutcomeCanceled
		addCanceledDiagnosticTo(&report.Diagnostics)
		return report
	}

	feature, ok := e.lookup(req.Feature)
	if !ok {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     fmt.Sprintf("등록되지 않은 작업입니다: %s", req.Feature),
			NextActions: []string{"dcraft --help로 사용 가능한 명령을 확인하세요."},
		})
		return report
	}
	desc := feature.Descriptor()
	planner, hasPlanner := feature.(Planner)
	if !hasPlanner && desc.Hooks == HookNone {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     msgPlanUnsupported,
			NextActions: []string{msgPlanUnsupportedAction},
		})
		return report
	}

	var target Target
	var warnings []Diagnostic
	resolveErr := e.resolveTarget(ctx, rec, req, desc, &target, &warnings)
	if len(target.ProjectDir) > 0 || target.Environment != "" || len(target.Files) > 0 {
		report.Target = target
	}
	report.Diagnostics = append(report.Diagnostics, warnings...)
	if resolveErr != nil {
		if isCanceled(ctx, resolveErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, failureDiagnostics(resolveErr)...)
		return report
	}

	// A plan lists every independent approval the run will need, including a
	// feature's conditionally required one. It never consumes an approval.
	required := uniqueApprovals(desc.Approval)
	if conditional, ok := feature.(ConditionalApproval); ok && !(desc.Wizard && req.Options["wizard"] == "steps") {
		kind, condErr := e.conditionalApproval(ctx, conditional, req, target)
		if condErr != nil {
			if isCanceled(ctx, condErr) {
				report.Outcome = OutcomeCanceled
				addCanceledDiagnosticTo(&report.Diagnostics)
				return report
			}
			report.Diagnostics = append(report.Diagnostics, failureDiagnostics(condErr)...)
			return report
		}
		required = uniqueApprovals(append(required, kind)...)
	}

	var pf PreflightReport
	preflightErr := e.runPreflight(ctx, rec, desc, &pf)
	report.Tools = pf.Tools
	if preflightErr != nil {
		if isCanceled(ctx, preflightErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, pf.Diagnostics...)
		if !HasError(report.Diagnostics) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Category:    CategoryInternal,
				Severity:    SeverityError,
				Message:     msgPreflightFailed,
				Detail:      errDetail(preflightErr),
				NextActions: []string{msgPreflightFailedAction},
			})
		}
		return report
	}
	report.Diagnostics = append(report.Diagnostics, pf.Diagnostics...)
	var hookText string
	if desc.Hooks != HookNone {
		if e.Hooks == nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Category:    CategoryInternal,
				Severity:    SeverityError,
				Message:     "호스트 스크립트 연결이 준비되지 않았습니다.",
				Detail:      "hook service is not configured",
				NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."},
			})
			return report
		}
		plan, hookErr := e.Hooks.Plan(ctx, target)
		if hookErr != nil {
			report.Diagnostics = append(report.Diagnostics, failureDiagnostics(hookErr)...)
			return report
		}
		hookText = hookPlanText(plan)
		report.Hooks = plan.Entries
		if plan.RequiresApproval {
			required = append(required, ApprovalScriptTrust)
		}
	}
	report.RequiredApprovals = uniqueApprovals(required...)

	inv := Invocation{
		Target:      target,
		Services:    append([]string{}, req.Services...),
		Args:        append([]string{}, req.Args...),
		Options:     req.Options,
		OptionLists: req.OptionLists,
		Interactive: req.Interactive,
		Preflight:   pf,
		IO:          req.IO,
		JSON:        req.JSON,
		Reporter:    rec,
		Clock:       clock,
		State:       e.State,
	}

	var planText string
	if hasPlanner {
		stageCountBefore := len(report.Stages)
		plan, planErr := e.runPlan(ctx, planner, inv)
		if len(report.Stages) == stageCountBefore {
			rec.Begin("plan", msgStagePlan)
			status := StageSucceeded
			if planErr != nil {
				status = StageFailed
				if isCanceled(ctx, planErr) {
					status = StageCanceled
				}
			}
			rec.End("plan", status, "")
		}
		if planErr != nil {
			if isCanceled(ctx, planErr) {
				report.Outcome = OutcomeCanceled
				addCanceledDiagnosticTo(&report.Diagnostics)
				return report
			}
			report.Diagnostics = append(report.Diagnostics, failureDiagnostics(planErr)...)
			return report
		}
		planText = plan.Text
	}
	if ctx.Err() != nil {
		report.Outcome = OutcomeCanceled
		addCanceledDiagnosticTo(&report.Diagnostics)
		return report
	}
	report.Text = strings.TrimSpace(strings.Join([]string{strings.TrimSpace(planText), hookText}, "\n\n"))
	report.Outcome = OutcomeSuccess
	return report
}

// Inputs resolves the same target and preflight as Run, then asks a feature
// that implements InputProvider which values an interactive interface must
// collect. It never checks an approval, never runs the feature and never
// writes history, so the TUI can show the request (service, instance, command)
// before anything is attached.
func (e *Engine) Inputs(ctx context.Context, req Request, sink Sink) (report InputsReport) {
	clock := e.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	report = InputsReport{
		Feature:     req.Feature,
		Outcome:     OutcomeFailure,
		Target:      Target{ProjectDir: req.Target.Path, Environment: req.Target.Environment},
		Stages:      []Stage{},
		Diagnostics: []Diagnostic{},
		Fields:      []InputField{},
	}
	report.Target.Normalize()
	defer func() {
		report.Target.Normalize()
		if report.Stages == nil {
			report.Stages = []Stage{}
		}
		if report.Diagnostics == nil {
			report.Diagnostics = []Diagnostic{}
		}
		if report.Fields == nil {
			report.Fields = []InputField{}
		}
	}()

	rec := &recorder{stages: &report.Stages, clock: clock, sink: sink}
	if ctx.Err() != nil {
		report.Outcome = OutcomeCanceled
		addCanceledDiagnosticTo(&report.Diagnostics)
		return report
	}

	feature, ok := e.lookup(req.Feature)
	if !ok {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     fmt.Sprintf("등록되지 않은 작업입니다: %s", req.Feature),
			NextActions: []string{"dcraft --help로 사용 가능한 명령을 확인하세요."},
		})
		return report
	}
	desc := feature.Descriptor()
	provider, ok := feature.(InputProvider)
	if !ok {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     msgInputsUnsupported,
			NextActions: []string{msgInputsUnsupportedAction},
		})
		return report
	}

	var target Target
	var warnings []Diagnostic
	resolveErr := e.resolveTarget(ctx, rec, req, desc, &target, &warnings)
	if len(target.ProjectDir) > 0 || target.Environment != "" || len(target.Files) > 0 {
		report.Target = target
	}
	report.Diagnostics = append(report.Diagnostics, warnings...)
	if resolveErr != nil {
		if isCanceled(ctx, resolveErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, failureDiagnostics(resolveErr)...)
		return report
	}

	var pf PreflightReport
	preflightErr := e.runPreflight(ctx, rec, desc, &pf)
	report.Tools = pf.Tools
	if preflightErr != nil {
		if isCanceled(ctx, preflightErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, pf.Diagnostics...)
		if !HasError(report.Diagnostics) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Category:    CategoryInternal,
				Severity:    SeverityError,
				Message:     msgPreflightFailed,
				Detail:      errDetail(preflightErr),
				NextActions: []string{msgPreflightFailedAction},
			})
		}
		return report
	}
	report.Diagnostics = append(report.Diagnostics, pf.Diagnostics...)

	// The form must see the same stack as Run. Read-only features resolve the
	// recorded/declared project identity here exactly as Run does, before the
	// feature lists services and instances.
	if desc.ReadOnly && e.Identity != nil && strings.TrimSpace(target.ProjectName) == "" {
		name, identErr := e.Identity.ReadProjectName(ctx, target)
		if ctxErr := ctx.Err(); ctxErr != nil {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		if identErr != nil {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Category:    CategoryIdentityUnreadable,
				Severity:    SeverityWarning,
				Message:     msgIdentityUnreadable,
				Detail:      errDetail(identErr),
				NextActions: []string{msgIdentityUnreadableActionOne, msgIdentityUnreadableActionTwo},
			})
		} else if name != "" {
			target.ProjectName = name
			report.Target.ProjectName = name
		}
	}

	inv := Invocation{
		Target:      target,
		Services:    append([]string{}, req.Services...),
		Args:        append([]string{}, req.Args...),
		Options:     req.Options,
		Interactive: req.Interactive,
		Preflight:   pf,
		IO:          req.IO,
		JSON:        req.JSON,
		Reporter:    rec,
		Clock:       clock,
		State:       e.State,
	}

	fields, inputErr := RunStageValue(rec, "inputs", msgStageInputs, func() ([]InputField, error) {
		return provider.Inputs(ctx, inv)
	})
	if inputErr != nil {
		if isCanceled(ctx, inputErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, failureDiagnostics(inputErr)...)
		return report
	}
	if ctx.Err() != nil {
		report.Outcome = OutcomeCanceled
		addCanceledDiagnosticTo(&report.Diagnostics)
		return report
	}
	if fields == nil {
		fields = []InputField{}
	}
	report.Fields = fields
	report.Outcome = OutcomeSuccess
	return report
}

func (e *Engine) runPlan(ctx context.Context, p Planner, in Invocation) (plan Plan, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("feature panic: %v", recovered)
		}
	}()
	return p.Plan(ctx, in)
}

func failureDiagnostics(err error) []Diagnostic {
	var ce *Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		return append([]Diagnostic{}, ce.Diagnostics...)
	}
	return []Diagnostic{{
		Category:    CategoryInternal,
		Severity:    SeverityError,
		Message:     msgInternalError,
		Detail:      errDetail(err),
		NextActions: []string{msgInternalErrorAction},
	}}
}

func (e *Engine) runFeature(ctx context.Context, f Feature, in Invocation) (payload any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &recoveredPanicError{value: recovered}
		}
	}()
	return f.Run(ctx, in)
}

// recoveredPanicError marks a panic raised by feature code. It is reported as
// internal_error (never as a feature-specific validation failure) while still
// travelling through the normal stage/failure path.
type recoveredPanicError struct {
	value any
}

func (e *recoveredPanicError) Error() string {
	return fmt.Sprintf("feature panic: %v", e.value)
}

func runVerify(ctx context.Context, v Verifier, in Invocation, payload any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &recoveredPanicError{value: recovered}
		}
	}()
	return v.Verify(ctx, in, payload)
}

func verifyFailureDiagnostics(err error) []Diagnostic {
	var panicErr *recoveredPanicError
	if errors.As(err, &panicErr) {
		return []Diagnostic{{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     msgInternalError,
			Detail:      errDetail(err),
			NextActions: []string{msgInternalErrorAction},
		}}
	}
	var ce *Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		return append([]Diagnostic{}, ce.Diagnostics...)
	}
	return []Diagnostic{{
		Category:    CategoryVerificationFailed,
		Severity:    SeverityError,
		Message:     msgVerificationFailed,
		Detail:      errDetail(err),
		NextActions: []string{msgVerificationFailedActionOne, msgVerificationFailedActionTwo},
	}}
}

// historyStages snapshots the completed stages for an operation record. An open
// stage (including the history stage itself, which is still being recorded)
// must not be persisted as if it had started and never finished.
func historyStages(stages []Stage) []Stage {
	out := make([]Stage, 0, len(stages))
	for _, st := range stages {
		if st.Status == StageStarted {
			continue
		}
		out = append(out, st)
	}
	return out
}

func (e *Engine) lookup(name string) (Feature, bool) {
	if e.Registry == nil {
		return nil, false
	}
	return e.Registry.Get(name)
}

func (e *Engine) cwd() (string, error) {
	if e.CWD == nil {
		return os.Getwd()
	}
	return e.CWD()
}

func (e *Engine) operationID() string {
	if e.NewOperationID == nil {
		return generateOperationID()
	}
	return e.NewOperationID()
}

func (e *Engine) applyFailure(ctx context.Context, res *Result, err error, feature Feature) {
	if isCanceled(ctx, err) {
		res.Outcome = OutcomeCanceled
		addCanceledDiagnosticFor(&res.Diagnostics, feature)
		return
	}
	res.Outcome = OutcomeFailure
	var ce *Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		res.Diagnostics = append(res.Diagnostics, ce.AllDiagnostics()...)
		return
	}
	res.Diagnostics = append(res.Diagnostics, Diagnostic{
		Category:    CategoryInternal,
		Severity:    SeverityError,
		Message:     msgInternalError,
		Detail:      errDetail(err),
		NextActions: []string{msgInternalErrorAction},
	})
}

type recorder struct {
	stages *[]Stage
	clock  Clock
	sink   Sink
}

func (r *recorder) Begin(name, title string) {
	st := Stage{Name: name, Title: title, Status: StageStarted, StartedAt: r.clock.Now()}
	*r.stages = append(*r.stages, st)
	r.emit(st)
}

func (r *recorder) End(name string, status StageStatus, detail string) {
	stages := *r.stages
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].Name != name || stages[i].Status != StageStarted {
			continue
		}
		finished := r.clock.Now()
		stages[i].Status = status
		stages[i].FinishedAt = &finished
		stages[i].Detail = detail
		r.emit(stages[i])
		return
	}
}

func (r *recorder) emit(st Stage) {
	if r.sink != nil {
		r.sink.StageChanged(st)
	}
}

func isCanceled(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return ctx.Err() != nil
}

func addCanceledDiagnostic(res *Result) {
	addCanceledDiagnosticTo(&res.Diagnostics)
}

// addCanceledDiagnosticFor lets a feature describe its own cancellation effect
// while the outcome and exit code stay the shared canceled contract.
func addCanceledDiagnosticFor(diags *[]Diagnostic, feature Feature) {
	if feature != nil {
		if reporter, ok := feature.(CanceledReporter); ok {
			for _, d := range *diags {
				if d.Category == CategoryCanceled {
					return
				}
			}
			d := reporter.CanceledDiagnostic()
			d.Category = CategoryCanceled
			d.Severity = SeverityWarning
			*diags = append(*diags, d)
			return
		}
	}
	addCanceledDiagnosticTo(diags)
}

func addCanceledDiagnosticTo(diags *[]Diagnostic) {
	for _, d := range *diags {
		if d.Category == CategoryCanceled {
			return
		}
	}
	*diags = append(*diags, Diagnostic{
		Category:    CategoryCanceled,
		Severity:    SeverityWarning,
		Message:     msgCanceled,
		NextActions: []string{msgCanceledActionOne, msgCanceledActionTwo},
	})
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	const max = 500
	if len(text) > max {
		text = text[:max] + "…"
	}
	return text
}

func generateOperationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("op_%d", time.Now().UnixNano())
	}
	return "op_" + hex.EncodeToString(b[:])
}
