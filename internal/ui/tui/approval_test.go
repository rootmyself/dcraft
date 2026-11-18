package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type stubPlanner struct {
	*stubFeature
	plan     func(ctx context.Context, in core.Invocation) (core.Plan, error)
	planRuns int
}

func (f *stubPlanner) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	f.planRuns++
	if f.plan == nil {
		return core.Plan{Text: "출처: https://example.test\n버전: v1\n대상 경로: /plugins/docker-compose"}, nil
	}
	return f.plan(ctx, in)
}

func approvalDescriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name:     name,
		Title:    "설치 " + name,
		Summary:  "설치합니다.",
		Usage:    "dcraft " + name,
		Approval: core.ApprovalInstall,
		Primary:  true,
		Requires: core.Requirements{core.RequirementDockerCLI},
	}
}

func TestApprovalPlannerShowsPlanAndWaitsForConfirmation(t *testing.T) {
	install := &stubPlanner{
		stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과 본문"},
	}
	m := newTestModel(t, install)
	initModel(t, m)

	view := m.View()
	for _, want := range []string{"출처", "버전: v1", "/plugins/docker-compose", "승인", "enter 승인", "esc 취소"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation view missing %q:\n%s", want, view)
		}
	}
	if install.runs != 0 {
		t.Fatalf("feature ran before confirmation: %d", install.runs)
	}
	if install.planRuns != 1 {
		t.Fatalf("plan runs = %d, want 1", install.planRuns)
	}
}

func TestDecliningApprovalRunsNothing(t *testing.T) {
	install := &stubPlanner{stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "body"}}
	m := newTestModel(t, install)
	initModel(t, m)

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if install.runs != 0 {
		t.Fatalf("declining ran the feature: %d", install.runs)
	}
	if install.planRuns != 1 {
		t.Fatalf("declining started another plan: %d", install.planRuns)
	}
	view := m.View()
	if !strings.Contains(view, "아무것도 실행하지 않았습니다") {
		t.Fatalf("decline result missing:\n%s", view)
	}
}

func TestAcceptingApprovalRunsThroughTheEngineWithTheApproval(t *testing.T) {
	install := &stubPlanner{stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과 본문"}}
	m := newTestModel(t, install)
	initModel(t, m)

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if install.runs != 1 {
		t.Fatalf("accepted feature runs = %d, want 1", install.runs)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", m.result)
	}
	if !strings.Contains(m.View(), "설치 결과 본문") {
		t.Fatalf("result not shown:\n%s", m.View())
	}
}

func TestApprovalWithoutPlannerStillFailsInTheEngine(t *testing.T) {
	install := &stubFeature{desc: approvalDescriptor("install-compose"), body: "body"}
	m := newTestModel(t, install)
	initModel(t, m)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if install.runs != 0 {
		t.Fatalf("feature without a planner ran without an approval: %d", install.runs)
	}
	if m.result == nil || m.result.RequiredApproval != core.ApprovalInstall {
		t.Fatalf("result = %+v", m.result)
	}
}

func TestPlanFailureIsShownInsteadOfConfirmation(t *testing.T) {
	install := &stubPlanner{
		stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "body"},
		plan: func(ctx context.Context, in core.Invocation) (core.Plan, error) {
			return core.Plan{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryComposeReleaseUnavailable,
				Severity:    core.SeverityError,
				Message:     "공식 릴리스 정보를 확인하지 못했습니다.",
				NextActions: []string{"네트워크 연결을 확인하세요."},
			})
		},
	}
	m := newTestModel(t, install)
	initModel(t, m)
	view := m.View()
	if !strings.Contains(view, "작업 실패") || !strings.Contains(view, "릴리스 정보를 확인하지 못했습니다") {
		t.Fatalf("plan failure not shown:\n%s", view)
	}
	if strings.Contains(view, "enter 승인") {
		t.Fatalf("failed plan still offered confirmation:\n%s", view)
	}
	if install.runs != 0 {
		t.Fatalf("failed plan ran the feature: %d", install.runs)
	}
}

// An ordinary update is not approval-gated: the same read-only plan screen is
// shown as the preview, and one enter runs it without an extra approval kind.
func TestOrdinaryPlannerShowsPlanAndRunsWithoutApproval(t *testing.T) {
	upd := &stubPlanner{
		stubFeature: &stubFeature{
			desc: core.Descriptor{
				Name: "update", Title: "갱신", Summary: "현재 설정을 적용합니다.",
				Usage: "dcraft update", Primary: true, RecordsHistory: true,
			},
			body: "갱신 결과 본문",
		},
		plan: func(context.Context, core.Invocation) (core.Plan, error) {
			return core.Plan{Text: "예정 작업\n  - web: 이미지 취득(nginx:1.27) 후 적용"}, nil
		},
	}
	m := newTestModel(t, upd)
	initModel(t, m)

	view := m.View()
	for _, want := range []string{"예정 작업", "이미지 취득", "다음 작업", "enter 실행", "esc 취소"} {
		if !strings.Contains(view, want) {
			t.Fatalf("preview confirmation missing %q:\n%s", want, view)
		}
	}
	if upd.runs != 0 {
		t.Fatalf("ordinary update ran before confirmation: %d", upd.runs)
	}
	if upd.planRuns != 1 {
		t.Fatalf("plan runs = %d, want 1", upd.planRuns)
	}

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if upd.runs != 1 {
		t.Fatalf("confirmed update runs = %d, want 1", upd.runs)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", m.result)
	}
	if m.result.RequiredApproval != "" {
		t.Fatalf("ordinary update required an approval: %q", m.result.RequiredApproval)
	}
	if !strings.Contains(m.View(), "갱신 결과 본문") {
		t.Fatalf("result not shown:\n%s", m.View())
	}
}
