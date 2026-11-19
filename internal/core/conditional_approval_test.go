package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// conditionalFeature asks for an independent approval based on its own
// read-only inspection of the request (for example an existing destination
// file). The engine must enforce it exactly like a descriptor approval.
type conditionalFeature struct {
	*testFeature
	kind    core.ApprovalKind
	err     error
	plan    string
	planned bool
	ran     bool
}

func (f *conditionalFeature) RequiredApproval(ctx context.Context, in core.Invocation) (core.ApprovalKind, error) {
	return f.kind, f.err
}

func (f *conditionalFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.ran = true
	return "saved", nil
}

func (f *conditionalFeature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	f.planned = true
	return core.Plan{Text: f.plan}, nil
}

func mutatingDescriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name:    name,
		Title:   "최초 구성",
		Summary: "Compose 파일을 생성합니다.",
		Usage:   "dcraft " + name,
	}
}

func TestEngineEnforcesConditionalApprovalOnlyWhenTheFeatureNeedsIt(t *testing.T) {
	feature := &conditionalFeature{
		testFeature: &testFeature{desc: mutatingDescriptor("init")},
		kind:        core.ApprovalFileOverwrite,
	}
	h := newHarness(t, feature)

	res := h.run(t, core.Request{Feature: "init"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q, want failure", res.Outcome)
	}
	if res.RequiredApproval != core.ApprovalFileOverwrite {
		t.Fatalf("required approval = %q, want file_overwrite", res.RequiredApproval)
	}
	if !hasCategory(res, core.CategoryApprovalRequired) {
		t.Fatalf("diagnostics = %+v, want approval_required", res.Diagnostics)
	}
	if res.ExitCode() != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode())
	}
	if feature.ran {
		t.Fatal("feature ran without its conditionally required approval")
	}

	feature.ran = false
	res = h.run(t, core.Request{
		Feature:   "init",
		Approvals: []core.ApprovalKind{core.ApprovalFileOverwrite},
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("approved outcome = %q diagnostics=%+v", res.Outcome, res.Diagnostics)
	}
	if !feature.ran {
		t.Fatal("feature did not run after the approval")
	}
}

func TestEngineStillRunsAWizardStepsPreviewWithoutTheConditionalApproval(t *testing.T) {
	feature := &conditionalFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true,
		}},
		kind: core.ApprovalFileOverwrite,
	}
	h := newHarness(t, feature)

	res := h.run(t, core.Request{
		Feature: "init",
		Options: map[string]string{"wizard": "steps"},
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("steps outcome = %q diagnostics=%+v", res.Outcome, res.Diagnostics)
	}
	if !feature.ran {
		t.Fatal("steps run did not reach the feature")
	}
}

func TestEngineReportsConditionalApprovalInThePlan(t *testing.T) {
	feature := &conditionalFeature{
		testFeature: &testFeature{desc: mutatingDescriptor("init")},
		kind:        core.ApprovalFileOverwrite,
		plan:        "덮어쓸 파일: /project/compose.yaml",
	}
	h := newHarness(t, feature)

	report := h.engine.Plan(context.Background(), core.Request{Feature: "init"}, nil)
	if report.Failed() {
		t.Fatalf("plan failed: %+v", report.Diagnostics)
	}
	if len(report.RequiredApprovals) != 1 || report.RequiredApprovals[0] != core.ApprovalFileOverwrite {
		t.Fatalf("required approvals = %v, want file_overwrite", report.RequiredApprovals)
	}
	if !feature.planned || !strings.Contains(report.Text, "/project/compose.yaml") {
		t.Fatalf("plan text = %q", report.Text)
	}
	if feature.ran {
		t.Fatal("plan ran the feature")
	}
}

func TestEngineRequiresDescriptorAndConditionalApprovalsIndependently(t *testing.T) {
	feature := &conditionalFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name:     "init",
			Title:    "최초 구성",
			Summary:  "설정",
			Usage:    "dcraft init",
			Approval: core.ApprovalInstall,
		}},
		kind: core.ApprovalFileOverwrite,
	}
	h := newHarness(t, feature)

	res := h.run(t, core.Request{
		Feature:   "init",
		Approvals: []core.ApprovalKind{core.ApprovalInstall},
	})
	if res.Outcome != core.OutcomeFailure || res.RequiredApproval != core.ApprovalFileOverwrite {
		t.Fatalf("outcome=%q required=%q, want the conditional approval first", res.Outcome, res.RequiredApproval)
	}
	if feature.ran {
		t.Fatal("feature ran without both approvals")
	}

	res = h.run(t, core.Request{
		Feature:   "init",
		Approvals: []core.ApprovalKind{core.ApprovalInstall, core.ApprovalFileOverwrite},
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("both approvals outcome = %q diagnostics=%+v", res.Outcome, res.Diagnostics)
	}
}

func TestEngineSurfacesConditionalApprovalInspectionErrorsBeforePreflight(t *testing.T) {
	feature := &conditionalFeature{
		testFeature: &testFeature{desc: mutatingDescriptor("init")},
		err: core.Fail(core.Diagnostic{
			Category:    core.CategoryInvalidOption,
			Severity:    core.SeverityError,
			Message:     "대상 경로를 확인할 수 없습니다.",
			NextActions: []string{"경로를 수정하세요."},
		}),
	}
	h := newHarness(t, feature)

	res := h.run(t, core.Request{Feature: "init"})
	if res.Outcome != core.OutcomeFailure || !hasCategory(res, core.CategoryInvalidOption) {
		t.Fatalf("outcome=%q diagnostics=%+v", res.Outcome, res.Diagnostics)
	}
	if h.preflight.called {
		t.Fatal("preflight ran after the conditional approval inspection failed")
	}
	if feature.ran {
		t.Fatal("feature ran after its own inspection failed")
	}
}
