package core_test

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func wizardFeatureDescriptor() core.Descriptor {
	return core.Descriptor{
		Name:           "backup",
		Title:          "데이터 백업",
		Summary:        "데이터를 백업합니다.",
		Usage:          "dcraft backup",
		RecordsHistory: true,
		Wizard:         true,
	}
}

// A wizard "steps" run is a read-only preview: it must not write a success
// history entry for work the user has not confirmed yet. The apply run is the
// recorded operation.
func TestWizardStepsRunDoesNotWriteHistory(t *testing.T) {
	feature := &testFeature{desc: wizardFeatureDescriptor()}
	h := newHarness(t, feature)

	steps := h.run(t, core.Request{Feature: "backup", Options: map[string]string{"wizard": "steps"}})
	if steps.Outcome != core.OutcomeSuccess {
		t.Fatalf("steps outcome = %q diagnostics=%+v", steps.Outcome, steps.Diagnostics)
	}
	if len(h.state.records) != 0 {
		t.Fatalf("steps run wrote history: %+v", h.state.records)
	}

	apply := h.run(t, core.Request{Feature: "backup", Options: map[string]string{"wizard": "apply"}})
	if apply.Outcome != core.OutcomeSuccess {
		t.Fatalf("apply outcome = %q", apply.Outcome)
	}
	if len(h.state.records) != 1 {
		t.Fatalf("apply run history records = %d, want 1", len(h.state.records))
	}
	if h.state.records[0].Action != "backup" {
		t.Fatalf("history action = %q", h.state.records[0].Action)
	}
}
