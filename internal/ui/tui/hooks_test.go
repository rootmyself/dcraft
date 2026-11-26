package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type stubHooks struct {
	plan     core.HookPlan
	approves int
	executes int
}

func (s *stubHooks) Plan(context.Context, core.Target) (core.HookPlan, error) {
	return s.plan, nil
}

func (s *stubHooks) Approve(context.Context, core.Target, core.HookPlan, time.Time) error {
	s.approves++
	return nil
}

func (s *stubHooks) Execute(context.Context, core.HookRequest) (core.HookExecution, error) {
	s.executes++
	return core.HookExecution{}, nil
}

func hookUpDescriptor() core.Descriptor {
	return core.Descriptor{
		Name: "up", Title: "시작", Summary: "스택 시작.", Usage: "dcraft up",
		Section:        core.SectionOperate,
		RecordsHistory: true, Hooks: core.HookUpUpdate,
	}
}

// newHookModel builds a menu with a read-only primary status feature and the
// hook-capable up feature, then opens the up plan screen exactly as a user
// would select it from the menu. Mutating features cannot be the TUI primary
// without an independent approval.
func newHookModel(t *testing.T, up *stubFeature, hooks *stubHooks) *Model {
	t.Helper()
	reg := core.NewRegistry()
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	if err := reg.Register(status); err != nil {
		t.Fatalf("Register status: %v", err)
	}
	if err := reg.Register(up); err != nil {
		t.Fatalf("Register up: %v", err)
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui_hooks" }
	engine.Hooks = hooks
	m := NewModel(context.Background(), Options{Engine: engine, Registry: reg})
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	return m
}

func untrustedHookPlan() core.HookPlan {
	return core.HookPlan{
		Entries: []core.HookEntry{{
			Path: "scripts/pre.sh", Stage: core.HookStagePre,
			SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}},
		RequiresApproval: true,
	}
}

func TestTUIHookPlanRequiresScriptTrustApproval(t *testing.T) {
	up := &stubFeature{desc: hookUpDescriptor(), body: "시작 본문"}
	hooks := &stubHooks{plan: untrustedHookPlan()}
	m := newHookModel(t, up, hooks)
	initModel(t, m)

	view := m.View()
	for _, want := range []string{"승인 확인", "스크립트 신뢰", "script_trust", "scripts/pre.sh", "0123456789abcdef"} {
		if !strings.Contains(view, want) {
			t.Fatalf("approval view missing %q:\n%s", want, view)
		}
	}
	if up.runs != 0 || hooks.executes != 0 || hooks.approves != 0 {
		t.Fatalf("untrusted script ran before approval: feature=%d exec=%d approve=%d", up.runs, hooks.executes, hooks.approves)
	}
}

func TestTUIDecliningScriptTrustRunsNothing(t *testing.T) {
	up := &stubFeature{desc: hookUpDescriptor(), body: "시작 본문"}
	hooks := &stubHooks{plan: untrustedHookPlan()}
	m := newHookModel(t, up, hooks)
	initModel(t, m)

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if up.runs != 0 || hooks.executes != 0 || hooks.approves != 0 {
		t.Fatalf("decline ran work: feature=%d exec=%d approve=%d", up.runs, hooks.executes, hooks.approves)
	}
	if !strings.Contains(m.View(), "아무것도 실행하지 않았음") {
		t.Fatalf("decline result not shown:\n%s", m.View())
	}
}

func TestTUIAcceptingScriptTrustApprovesAndRuns(t *testing.T) {
	up := &stubFeature{desc: hookUpDescriptor(), body: "시작 본문"}
	hooks := &stubHooks{plan: untrustedHookPlan()}
	m := newHookModel(t, up, hooks)
	initModel(t, m)

	cmd := approveFocused(t, m)
	feed(t, m, execute(t, cmd))
	if hooks.approves != 1 || hooks.executes != 1 || up.runs != 1 {
		t.Fatalf("approve=%d exec=%d feature=%d", hooks.approves, hooks.executes, up.runs)
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open the held result
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", m.result)
	}
	if m.result.RequiredApproval != "" {
		t.Fatalf("required approval leaked: %q", m.result.RequiredApproval)
	}
}

func TestTUITrustedHooksRunAfterOneConfirmation(t *testing.T) {
	up := &stubFeature{desc: hookUpDescriptor(), body: "시작 본문"}
	plan := untrustedHookPlan()
	plan.RequiresApproval = false
	plan.Entries[0].Trusted = true
	hooks := &stubHooks{plan: plan}
	m := newHookModel(t, up, hooks)
	initModel(t, m)

	view := m.View()
	if !strings.Contains(view, "scripts/pre.sh") || !strings.Contains(view, "sha256:") {
		t.Fatalf("hook plan not shown: %s\n", view)
	}
	if strings.Contains(view, "승인: 스크립트 신뢰") {
		t.Fatalf("trusted hooks still asked for approval:\n%s", view)
	}
	cmd := approveFocused(t, m)
	feed(t, m, execute(t, cmd))
	if up.runs != 1 || hooks.executes != 1 || hooks.approves != 0 {
		t.Fatalf("feature=%d exec=%d approve=%d", up.runs, hooks.executes, hooks.approves)
	}
}
