package core_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestEngineVerifierPanicBecomesInternalError(t *testing.T) {
	feature := &verifyingFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
			Section: core.SectionOperate,
		}},
		verify: func(context.Context, core.Invocation, any) error {
			panic("verifier exploded")
		},
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "update"})

	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q, want failure", res.Outcome)
	}
	if !hasCategory(res, core.CategoryInternal) {
		t.Fatalf("diagnostics = %+v, want internal_error", res.Diagnostics)
	}
	if hasCategory(res, core.CategoryVerificationFailed) {
		t.Fatalf("panic was misreported as a verification failure: %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "verify"); st.Status != core.StageFailed {
		t.Fatalf("verify stage = %q, want failed", st.Status)
	}
	if res.Data != nil {
		t.Fatalf("data = %#v on failure, want omitted", res.Data)
	}
	if len(h.state.records) != 0 {
		t.Fatalf("panicking verifier recorded history: %+v", h.state.records)
	}
}

func TestEngineVerificationFailureOmitsData(t *testing.T) {
	feature := &verifyingFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
			Section: core.SectionOperate,
		}},
		verify: func(context.Context, core.Invocation, any) error {
			return core.Fail(core.Diagnostic{
				Category: core.CategoryVerificationFailed,
				Severity: core.SeverityError,
				Message:  "확인 실패",
			})
		},
	}
	res := newHarness(t, feature).run(t, core.Request{Feature: "update"})

	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q, want failure", res.Outcome)
	}
	if res.Data != nil {
		t.Fatalf("data = %#v on failure, want omitted", res.Data)
	}
}

func TestEngineCancellationAfterRunOmitsData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feature := &verifyingFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
			Section: core.SectionOperate,
		}},
		verify: func(context.Context, core.Invocation, any) error {
			cancel()
			return nil
		},
	}
	h := newHarness(t, feature)
	sink := core.SinkFunc(func(core.Stage) {})
	res := h.engine.Run(ctx, core.Request{Feature: "update"}, sink)

	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q, want canceled", res.Outcome)
	}
	if res.Data != nil {
		t.Fatalf("data = %#v on cancellation, want omitted", res.Data)
	}
}

func TestEngineHistoryRecordHasNoOpenStage(t *testing.T) {
	feature := &testFeature{desc: core.Descriptor{
		Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
		Section: core.SectionOperate,
	}}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "update"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if st := stageByName(t, res, "history"); st.Status != core.StageSucceeded {
		t.Fatalf("result history stage = %q, want succeeded", st.Status)
	}
	if len(h.state.records) != 1 {
		t.Fatalf("history records = %+v", h.state.records)
	}
	rec := h.state.records[0]
	if len(rec.Stages) == 0 {
		t.Fatal("history record has no stages")
	}
	for _, st := range rec.Stages {
		if st.Status == core.StageStarted {
			t.Fatalf("history record contains an open stage: %+v", rec.Stages)
		}
		if st.Name == "history" {
			t.Fatalf("history record contains the record-writing stage: %+v", rec.Stages)
		}
	}
	last := rec.Stages[len(rec.Stages)-1]
	if last.Name != "execute" || last.Status != core.StageSucceeded {
		t.Fatalf("record final stage = %+v, want execute/succeeded", last)
	}
}
