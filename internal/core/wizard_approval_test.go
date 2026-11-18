package core_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestWizardStepsSkipApprovalOnlyForWizardFeatures(t *testing.T) {
	run := func(t *testing.T, wizard bool, options map[string]string) (core.Result, int) {
		t.Helper()
		runs := 0
		feature := &testFeature{
			desc: core.Descriptor{
				Name: "restore", Title: "데이터 복원", Summary: "복원", Usage: "dcraft restore",
				Approval: core.ApprovalDataRestore, Wizard: wizard,
			},
			run: func(ctx context.Context, in core.Invocation) (any, error) {
				runs++
				return core.WizardSpec{Title: "설정"}, nil
			},
		}
		h := newHarness(t, feature)
		res := h.run(t, core.Request{
			Feature: "restore",
			Target:  core.TargetRequest{Path: "/project"},
			Options: options,
		})
		return res, runs
	}

	res, runs := run(t, true, map[string]string{"wizard": "steps"})
	if res.Outcome != core.OutcomeSuccess || runs != 1 {
		t.Fatalf("wizard steps = %+v, runs=%d", res, runs)
	}

	res, runs = run(t, false, map[string]string{"wizard": "steps"})
	if res.Outcome != core.OutcomeFailure || runs != 0 {
		t.Fatalf("non-wizard steps = %+v, runs=%d", res, runs)
	}
	if !hasDiag(res, core.CategoryApprovalRequired) {
		t.Fatalf("non-wizard diagnostics = %+v", res.Diagnostics)
	}
}

func hasDiag(res core.Result, category core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}
