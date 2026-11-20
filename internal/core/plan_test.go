package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type planningFeature struct {
	*testFeature
	plan func(ctx context.Context, in core.Invocation) (core.Plan, error)
}

func (f *planningFeature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	return f.plan(ctx, in)
}

func installDescriptor() core.Descriptor {
	return core.Descriptor{
		Name:     "install-compose",
		Section:  core.SectionConfig,
		Title:    "Compose 설치",
		Summary:  "누락된 Compose를 설치합니다.",
		Usage:    "dcraft install-compose --approve install",
		Approval: core.ApprovalInstall,
		Requires: core.Requirements{core.RequirementDockerCLI},
		// install-compose must run in a folder without Compose files.
		TargetOptional: true,
	}
}

func TestEngineChecksExactlyDeclaredPreflightCapabilities(t *testing.T) {
	feature := &testFeature{desc: installDescriptor()}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{
		Feature:   "install-compose",
		Approvals: []core.ApprovalKind{core.ApprovalInstall},
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if len(h.preflight.requires) != 1 || h.preflight.requires[0] != core.RequirementDockerCLI {
		t.Fatalf("preflight requirements = %v, want only docker_cli", h.preflight.requires)
	}
}

func TestEngineDefaultsToFullPreflightRequirements(t *testing.T) {
	feature := &testFeature{desc: readOnlyDescriptor("status")}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	for _, want := range []core.Requirement{
		core.RequirementDockerCLI, core.RequirementEngine, core.RequirementCompose,
	} {
		if !h.preflight.requires.Has(want) {
			t.Fatalf("default requirements %v missing %q", h.preflight.requires, want)
		}
	}
}

func TestEnginePassesTargetOptionalToResolver(t *testing.T) {
	install := &testFeature{desc: installDescriptor()}
	h := newHarness(t, install)
	h.run(t, core.Request{Feature: "install-compose", Approvals: []core.ApprovalKind{core.ApprovalInstall}})
	if !h.resolver.got.AllowMissingConfiguration {
		t.Fatal("install-compose did not allow a folder without Compose configuration")
	}

	status := &testFeature{desc: readOnlyDescriptor("status")}
	h2 := newHarness(t, status)
	h2.run(t, core.Request{Feature: "status"})
	if h2.resolver.got.AllowMissingConfiguration {
		t.Fatal("status unexpectedly allowed a folder without Compose configuration")
	}
}

func TestEnginePlanPreviewsWithoutApprovalOrRun(t *testing.T) {
	ran := false
	install := &planningFeature{
		testFeature: &testFeature{
			desc: installDescriptor(),
			run: func(context.Context, core.Invocation) (any, error) {
				ran = true
				return "installed", nil
			},
		},
		plan: func(ctx context.Context, in core.Invocation) (core.Plan, error) {
			return core.RunStageValue(in.Reporter, "plan", "설치 계획", func() (core.Plan, error) {
				return core.Plan{Text: "출처: https://example.test/v1\n버전: v1\n대상 경로: /plugins/docker-compose"}, nil
			})
		},
	}
	h := newHarness(t, install)
	var stages []core.Stage
	report := h.engine.Plan(context.Background(), core.Request{Feature: "install-compose"}, core.SinkFunc(func(s core.Stage) {
		stages = append(stages, s)
	}))
	if report.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", report.Outcome, report.Diagnostics)
	}
	if ran {
		t.Fatal("Plan ran the feature")
	}
	if !strings.Contains(report.Text, "/plugins/docker-compose") {
		t.Fatalf("plan text = %q", report.Text)
	}
	if got := stageNamesReport(report); strings.Join(got, ",") != "resolve_target,preflight,plan" {
		t.Fatalf("stages = %v", got)
	}
	if !h.preflight.requires.Has(core.RequirementDockerCLI) || len(h.preflight.requires) != 1 {
		t.Fatalf("plan preflight requirements = %v", h.preflight.requires)
	}
	if !h.resolver.got.AllowMissingConfiguration {
		t.Fatal("plan target did not allow a folder without Compose configuration")
	}
	if len(stages) == 0 {
		t.Fatal("plan progress sink received no stage events")
	}
}

func TestEnginePlanFailureKeepsDiagnosticsAndSkipsRun(t *testing.T) {
	ran := false
	install := &planningFeature{
		testFeature: &testFeature{
			desc: installDescriptor(),
			run:  func(context.Context, core.Invocation) (any, error) { ran = true; return nil, nil },
		},
		plan: func(ctx context.Context, in core.Invocation) (core.Plan, error) {
			return core.Plan{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeReleaseUnavailable,
				Severity:    core.SeverityError,
				Message:     "릴리스 정보를 확인하지 못했습니다.",
				NextActions: []string{"네트워크 연결을 확인한 뒤 다시 시도하세요."},
			})
		},
	}
	h := newHarness(t, install)
	report := h.engine.Plan(context.Background(), core.Request{Feature: "install-compose"}, nil)
	if report.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if ran {
		t.Fatal("plan failure still ran the feature")
	}
	if !reportHasCategory(report, core.CategoryComposeReleaseUnavailable) {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
	if st := planStage(t, report, "plan"); st.Status != core.StageFailed {
		t.Fatalf("plan stage = %q", st.Status)
	}
}

func TestEnginePlanPreflightFailureSkipsPlanner(t *testing.T) {
	planned := false
	install := &planningFeature{
		testFeature: &testFeature{desc: installDescriptor()},
		plan: func(context.Context, core.Invocation) (core.Plan, error) {
			planned = true
			return core.Plan{Text: "plan"}, nil
		},
	}
	h := newHarness(t, install)
	h.preflight.report = core.PreflightReport{
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryDockerMissing, Severity: core.SeverityError,
			Message: "Docker CLI를 찾을 수 없습니다.",
		}},
	}
	report := h.engine.Plan(context.Background(), core.Request{Feature: "install-compose"}, nil)
	if report.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if planned {
		t.Fatal("planner ran after fatal preflight")
	}
	if !reportHasCategory(report, core.CategoryDockerMissing) {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestEnginePlanWithoutPlannerIsUnsupported(t *testing.T) {
	feature := &testFeature{desc: readOnlyDescriptor("status")}
	h := newHarness(t, feature)
	report := h.engine.Plan(context.Background(), core.Request{Feature: "status"}, nil)
	if report.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	diag := report.Diagnostics[0]
	if diag.Category != core.CategoryInternal || !strings.Contains(diag.Message, "계획") {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestEnginePlanCancellationIsReportedWithoutRunning(t *testing.T) {
	install := &planningFeature{
		testFeature: &testFeature{desc: installDescriptor()},
		plan: func(ctx context.Context, in core.Invocation) (core.Plan, error) {
			return core.Plan{}, ctx.Err()
		},
	}
	h := newHarness(t, install)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := h.engine.Plan(ctx, core.Request{Feature: "install-compose"}, nil)
	if report.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if !reportHasCategory(report, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func stageNamesReport(report core.PlanReport) []string {
	names := make([]string, 0, len(report.Stages))
	for _, s := range report.Stages {
		names = append(names, s.Name)
	}
	return names
}

func reportHasCategory(report core.PlanReport, cat core.Category) bool {
	for _, d := range report.Diagnostics {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func planStage(t *testing.T, report core.PlanReport, name string) core.Stage {
	t.Helper()
	for _, s := range report.Stages {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("stage %q not found in %v", name, stageNamesReport(report))
	return core.Stage{}
}
