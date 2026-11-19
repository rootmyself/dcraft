package core

import (
	"context"
	"fmt"
	"strings"
)

// ServiceStatus is one read-only fact about a Compose service in the current
// stack. It is presentation metadata for an interactive service-scope screen:
// a UI never calls Docker itself, and the selection is validated again by the
// feature that runs.
type ServiceStatus struct {
	// Name is the Compose service name (never a container instance).
	Name string `json:"name"`
	// State and Health are the published machine values of stack.ServiceState
	// and stack.Health.
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
	// Instances is the number of container instances of the service.
	Instances int `json:"instances"`
	// Configured is false when a container exists for a service that is not
	// part of the active Compose configuration. Such a service cannot be
	// selected for an operation.
	Configured bool `json:"configured"`
}

// ServiceCatalog is an optional read-only feature capability. A feature that
// can describe the current stack (status) implements it; Engine.Services asks
// the first read-only registered catalog, so the TUI never contacts Docker
// directly and no feature-specific branch is added to the shared assembly.
type ServiceCatalog interface {
	Services(ctx context.Context, in Invocation) ([]ServiceStatus, error)
}

// ServicesReport is the read-only outcome of Engine.Services. It uses the same
// target resolution, preflight requirements and stage model as Run, but it
// never runs the selected feature, checks an approval or writes history.
type ServicesReport struct {
	Feature     string          `json:"feature"`
	Outcome     Outcome         `json:"outcome"`
	Target      Target          `json:"target"`
	Tools       ToolInfo        `json:"tools"`
	Stages      []Stage         `json:"stages"`
	Diagnostics []Diagnostic    `json:"diagnostics"`
	Services    []ServiceStatus `json:"services"`
}

// Failed reports whether the service list cannot be offered to the user.
func (r ServicesReport) Failed() bool { return r.Outcome != OutcomeSuccess }

// Result converts a failed report into the common result shape so both
// interfaces render the same failure.
func (r ServicesReport) Result() Result {
	outcome := r.Outcome
	if outcome == "" {
		outcome = OutcomeFailure
	}
	res := Result{
		SchemaVersion: SchemaVersion,
		Action:        r.Feature,
		Outcome:       outcome,
		Target:        r.Target,
		Tools:         r.Tools,
		Stages:        r.Stages,
		Diagnostics:   r.Diagnostics,
	}
	if res.Stages == nil {
		res.Stages = []Stage{}
	}
	if res.Diagnostics == nil {
		res.Diagnostics = []Diagnostic{}
	}
	res.Target.Normalize()
	return res
}

// Services lists the current stack services for an interactive scope screen.
// It resolves the target and preflight exactly as Run does for the requesting
// feature, then asks the registered read-only ServiceCatalog (status) for the
// facts. It is a query, not an execution path: the selection still travels
// through Plan/confirm/Run as Request.Services.
func (e *Engine) Services(ctx context.Context, req Request, sink Sink) (report ServicesReport) {
	clock := e.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	report = ServicesReport{
		Feature:     req.Feature,
		Outcome:     OutcomeFailure,
		Target:      Target{ProjectDir: req.Target.Path, Environment: req.Target.Environment},
		Stages:      []Stage{},
		Diagnostics: []Diagnostic{},
		Services:    []ServiceStatus{},
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
		if report.Services == nil {
			report.Services = []ServiceStatus{}
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
	if !desc.SupportsServices {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     msgServicesUnsupported,
			NextActions: []string{msgServicesUnsupportedAction},
		})
		return report
	}
	catalog := e.serviceCatalog()
	if catalog == nil {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     msgServiceCatalogUnavailable,
			NextActions: []string{msgServiceCatalogUnavailableAction},
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

	// The catalog reads the same stack as Run. Resolve the recorded/declared
	// project identity without writing; a failure is a warning, never a
	// reason to invent a name.
	if e.Identity != nil && strings.TrimSpace(target.ProjectName) == "" {
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
		Interactive: req.Interactive,
		IO:          req.IO,
		JSON:        req.JSON,
		Reporter:    rec,
		Clock:       clock,
		State:       e.State,
	}
	services, catalogErr := RunStageValue(rec, "service_catalog", msgStageServiceCatalog, func() ([]ServiceStatus, error) {
		return callServiceCatalog(ctx, catalog, inv)
	})
	if catalogErr != nil {
		if isCanceled(ctx, catalogErr) {
			report.Outcome = OutcomeCanceled
			addCanceledDiagnosticTo(&report.Diagnostics)
			return report
		}
		report.Diagnostics = append(report.Diagnostics, failureDiagnostics(catalogErr)...)
		return report
	}
	if ctx.Err() != nil {
		report.Outcome = OutcomeCanceled
		addCanceledDiagnosticTo(&report.Diagnostics)
		return report
	}
	if services == nil {
		services = []ServiceStatus{}
	}
	report.Services = services
	report.Outcome = OutcomeSuccess
	return report
}

// callServiceCatalog isolates a catalog panic the same way Run and Plan
// isolate feature/verifier panics: the UI gets an internal_error failure
// instead of crashing.
func callServiceCatalog(ctx context.Context, catalog ServiceCatalog, in Invocation) (services []ServiceStatus, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &recoveredPanicError{value: recovered}
		}
	}()
	return catalog.Services(ctx, in)
}

// serviceCatalog returns the first registered read-only feature that can
// describe the current stack. Selection is capability-based, never by name.
func (e *Engine) serviceCatalog() ServiceCatalog {
	if e == nil || e.Registry == nil {
		return nil
	}
	for _, f := range e.Registry.All() {
		if !f.Descriptor().ReadOnly {
			continue
		}
		if catalog, ok := f.(ServiceCatalog); ok {
			return catalog
		}
	}
	return nil
}
