package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type tuiFlipState struct {
	stubState
	calls int
}

func (s *tuiFlipState) PlanStateFingerprint(core.Target) string {
	s.calls++
	if s.calls <= 1 {
		return "plan-state"
	}
	return "changed-state"
}

type tuiPlanFeature struct {
	desc  core.Descriptor
	plans int
	runs  int
}

func (f *tuiPlanFeature) Descriptor() core.Descriptor { return f.desc }

func (f *tuiPlanFeature) Plan(context.Context, core.Invocation) (core.Plan, error) {
	f.plans++
	return core.Plan{Text: "설치 계획"}, nil
}

func (f *tuiPlanFeature) Run(context.Context, core.Invocation) (any, error) {
	f.runs++
	return nil, nil
}

func TestTUIBindsTheRunToTheApprovedPlanFingerprint(t *testing.T) {
	feature := &tuiPlanFeature{desc: core.Descriptor{
		Name: "install-compose", Title: "설치", Summary: "설치.", Usage: "dcraft install-compose",
		Section: core.SectionConfig,
		Primary: true, Approval: core.ApprovalInstall,
	}}
	reg := core.NewRegistry()
	if err := reg.Register(feature); err != nil {
		t.Fatal(err)
	}
	state := &tuiFlipState{}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, state, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui" }

	m := NewModel(context.Background(), Options{Engine: engine, Registry: reg})
	initModel(t, m)
	if m.phase != phaseConfirm || m.pendingApproval != core.ApprovalInstall {
		t.Fatalf("phase = %v approval = %q; view:\n%s", m.phase, m.pendingApproval, m.View())
	}
	if m.planFingerprint == "" {
		t.Fatal("TUI did not keep the plan fingerprint")
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // the panel starts on cancel
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if feature.runs != 0 {
		t.Fatal("TUI ran the feature with a stale approved plan")
	}
	if m.result == nil || m.result.Outcome != core.OutcomeFailure {
		t.Fatalf("result = %+v", m.result)
	}
	var stale bool
	for _, d := range m.result.Diagnostics {
		if d.Category == core.CategoryPlanStale {
			stale = true
		}
	}
	if !stale {
		t.Fatalf("diagnostics = %+v, want plan_stale", m.result.Diagnostics)
	}
	if state.calls < 2 {
		t.Fatalf("TUI did not bind a plan fingerprint to the run: calls=%d", state.calls)
	}
}
