package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// planState is a state store whose name-map fingerprint can change between the
// plan and the run, like an external writer updating names.json.
type planState struct {
	*fakeState
	fingerprints []string
	calls        int
}

func (s *planState) PlanStateFingerprint(core.Target) string {
	if len(s.fingerprints) == 0 {
		return ""
	}
	index := s.calls
	if index >= len(s.fingerprints) {
		index = len(s.fingerprints) - 1
	}
	s.calls++
	return s.fingerprints[index]
}

func fingerprintFeature(t *testing.T, ran *bool) *planningFeature {
	t.Helper()
	return &planningFeature{
		testFeature: &testFeature{
			desc: installDescriptor(),
			run: func(context.Context, core.Invocation) (any, error) {
				*ran = true
				return "installed", nil
			},
		},
		plan: func(ctx context.Context, in core.Invocation) (core.Plan, error) {
			return core.Plan{Text: "설치 계획"}, nil
		},
	}
}

func fingerprintHarness(t *testing.T, state *planState, feature core.Feature) (*engineHarness, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, feature)
	h.engine.State = state
	h.resolver.target = core.Target{
		ProjectDir: dir,
		Files:      []string{filepath.Join(dir, "compose.yaml")},
		EnvFiles:   []string{},
		Profiles:   []string{},
	}
	return h, dir
}

func TestApprovedPlanRunsWhenTheSelectionIsUnchanged(t *testing.T) {
	ran := false
	state := &planState{fakeState: &fakeState{}, fingerprints: []string{"one", "one"}}
	h, _ := fingerprintHarness(t, state, fingerprintFeature(t, &ran))
	req := core.Request{Feature: "install-compose", Approvals: []core.ApprovalKind{core.ApprovalInstall}}
	report := h.engine.Plan(context.Background(), req, nil)
	if report.Outcome != core.OutcomeSuccess || report.Fingerprint == "" {
		t.Fatalf("plan = %+v", report)
	}
	req.PlanFingerprint = report.Fingerprint
	res := h.engine.Run(context.Background(), req, nil)
	if res.Outcome != core.OutcomeSuccess || !ran {
		t.Fatalf("run = %+v ran=%v", res, ran)
	}
}

func TestPlanStaleWhenAComposeFileChangesAfterApproval(t *testing.T) {
	ran := false
	state := &planState{fakeState: &fakeState{}, fingerprints: []string{"one", "one"}}
	h, dir := fingerprintHarness(t, state, fingerprintFeature(t, &ran))
	req := core.Request{Feature: "install-compose", Approvals: []core.ApprovalKind{core.ApprovalInstall}}
	report := h.engine.Plan(context.Background(), req, nil)
	if report.Outcome != core.OutcomeSuccess {
		t.Fatalf("plan = %+v", report)
	}
	req.PlanFingerprint = report.Fingerprint
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := h.engine.Run(context.Background(), req, nil)
	assertPlanStale(t, res, ran, h)
}

func TestPlanStaleWhenProjectConfigChangesAfterApproval(t *testing.T) {
	ran := false
	state := &planState{fakeState: &fakeState{}, fingerprints: []string{"one", "one"}}
	h, dir := fingerprintHarness(t, state, fingerprintFeature(t, &ran))
	req := core.Request{Feature: "install-compose", Approvals: []core.ApprovalKind{core.ApprovalInstall}}
	report := h.engine.Plan(context.Background(), req, nil)
	req.PlanFingerprint = report.Fingerprint
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte("environments: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := h.engine.Run(context.Background(), req, nil)
	assertPlanStale(t, res, ran, h)
}

func TestPlanStaleWhenTheNameMapChangesAfterApproval(t *testing.T) {
	ran := false
	state := &planState{fakeState: &fakeState{}, fingerprints: []string{"one", "two"}}
	h, _ := fingerprintHarness(t, state, fingerprintFeature(t, &ran))
	req := core.Request{Feature: "install-compose", Approvals: []core.ApprovalKind{core.ApprovalInstall}}
	report := h.engine.Plan(context.Background(), req, nil)
	req.PlanFingerprint = report.Fingerprint
	res := h.engine.Run(context.Background(), req, nil)
	assertPlanStale(t, res, ran, h)
}

func assertPlanStale(t *testing.T, res core.Result, ran bool, h *engineHarness) {
	t.Helper()
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q, want failure", res.Outcome)
	}
	if ran {
		t.Fatal("feature ran despite a stale plan")
	}
	if len(h.state.records) != 0 {
		t.Fatalf("stale plan recorded history: %+v", h.state.records)
	}
	if !hasCategory(res, core.CategoryPlanStale) {
		t.Fatalf("diagnostics = %+v, want plan_stale", res.Diagnostics)
	}
	if code := res.ExitCode(); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryPlanStale && d.Message == "" {
			t.Fatalf("plan_stale diagnostic has no Korean message: %+v", d)
		}
	}
}
