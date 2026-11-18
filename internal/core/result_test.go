package core_test

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestExitCodesSeparateDecisionErrorsFromOperationFailures(t *testing.T) {
	cases := []struct {
		outcome core.Outcome
		diags   []core.Category
		want    int
	}{
		{core.OutcomeSuccess, nil, 0},
		{core.OutcomeCanceled, []core.Category{core.CategoryCanceled}, 130},
		{core.OutcomeFailure, nil, 1},
		{core.OutcomeFailure, []core.Category{core.CategoryLifecycleFailed}, 1},
		{core.OutcomeFailure, []core.Category{core.CategoryStackBusy}, 1},
		{core.OutcomeFailure, []core.Category{core.CategoryApprovalRequired}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryProjectNameConflict}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryServiceUnknown}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryInvalidOption}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryRestoreVolumeUnknown}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryRestoreBindUnknown}, 2},
		{core.OutcomeFailure, []core.Category{core.CategoryRestoreTargetMismatch}, 1},
	}
	for _, tc := range cases {
		res := core.Result{Outcome: tc.outcome}
		for _, cat := range tc.diags {
			res.Diagnostics = append(res.Diagnostics, core.Diagnostic{Category: cat, Severity: core.SeverityError})
		}
		if got := res.ExitCode(); got != tc.want {
			t.Fatalf("outcome=%s diagnostics=%v exit=%d, want %d", tc.outcome, tc.diags, got, tc.want)
		}
	}
}
