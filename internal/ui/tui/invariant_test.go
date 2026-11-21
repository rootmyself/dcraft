package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// The invariants the redesign promised not to change. Each test names the
// contract it protects, so a future layout change that breaks one fails with the
// reason instead of a diff.

// The TUI never renders through the CLI presenter: a feature that implements
// core.ViewPresenter must not have its string Present invoked by the TUI, and a
// feature that implements only Present must render that string unchanged. This
// is what keeps the CLI's text independent of the TUI redesign.
func TestInvariantTheTUINeverCallsTheCLIPresenterForADocument(t *testing.T) {
	both := &bothFeature{desc: descriptor("status", true), doc: statusDoc(), body: "CLI-STRING"}
	m := newTestModel(t, both)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)

	view := m.View()
	if strings.Contains(view, "CLI-STRING") {
		t.Fatalf("the TUI rendered the CLI presenter string:\n%s", view)
	}
	if both.presented != 0 {
		t.Fatalf("the TUI called Present %d time(s); the CLI path must stay separate", both.presented)
	}
	// The string path is still the fallback for a feature without a document.
	onlyString := &stubFeature{desc: descriptor("status", true), body: "문자열 본문"}
	m2 := newTestModel(t, onlyString)
	m2.theme = *plainTheme()
	initModel(t, m2)
	resize(t, m2, 80, 24)
	if !strings.Contains(m2.View(), "문자열 본문") {
		t.Fatalf("a Present-only feature lost its body:\n%s", m2.View())
	}
}

// The shared Korean failure text is the same one the CLI prints. The stage rows
// differ (the TUI timeline vs. the CLI list), but the outcome, target,
// operation ID and diagnostics must be identical, because support and the docs
// quote them.
func TestInvariantFailureWordingMatchesTheSharedKoreanText(t *testing.T) {
	res := core.Result{
		SchemaVersion: core.SchemaVersion,
		OperationID:   "op_invariant",
		Action:        "up",
		Outcome:       core.OutcomeFailure,
		Target:        core.Target{ProjectDir: "/work/acme-platform"},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결할 수 없습니다.",
			Detail:  "failed to connect",
			NextActions: []string{
				"Docker Desktop을 시작하세요.",
			},
		}},
	}

	// The CLI's text, produced by the shared package the CLI itself calls.
	cliText := view.Failure(res)

	// The TUI's text for the same result.
	d := goldenDescriptor(goldenDescriptorSpec{
		name: "up", title: "스택 시작", summary: "현재 폴더 스택을 시작합니다.",
		section: core.SectionOperate,
	}, false)
	d.Hooks = core.HookUpUpdate
	d.RecordsHistory = true
	failing := &stubFeature{desc: d, outcome: core.OutcomeFailure}
	features := replaceFeature(goldenBaseFeatures(), "up", failing)
	m := newGoldenModel(t, features...)
	m.active = failing
	m.result = &res
	m.stages = nil
	m.phase = phaseReady
	resize(t, m, 120, 40)

	tuiText := ansi.Strip(m.View())
	for _, want := range []string{
		"작업 실패: up",
		"대상: /work/acme-platform",
		"작업 ID: op_invariant",
		"문제: Docker Engine에 연결할 수 없습니다.",
		"failed to connect",
		"다음 행동:",
		"1. Docker Desktop을 시작하세요.",
	} {
		if !strings.Contains(cliText, want) {
			t.Fatalf("the CLI text changed; missing %q:\n%s", want, cliText)
		}
		if !strings.Contains(tuiText, want) {
			t.Fatalf("the TUI text diverged from the shared failure text; missing %q:\n%s", want, tuiText)
		}
	}
	if !strings.Contains(tuiText, "작업 실패") {
		t.Fatalf("the TUI failure title changed:\n%s", tuiText)
	}
}

// Declining an approval runs nothing and says so. The wording is the existing
// msgApprovalDeclined constant; the redesign only changed where it is drawn.
// An ordinary confirmation has no approval to decline, so this uses an
// approval-gated planner: the screen the user returns to is the canceled
// result.
func TestInvariantDecliningAnApprovalRunsNothingAndSaysSo(t *testing.T) {
	install := &stubPlanner{stubFeature: &stubFeature{desc: approvalDescriptor("install-compose"), body: "설치 결과"}}
	m := newTestModel(t, install)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 30)
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want the approval panel", m.phase)
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))

	if install.runs != 0 {
		t.Fatalf("esc ran the feature %d time(s)", install.runs)
	}
	if install.planRuns != 1 {
		t.Fatalf("esc started a second plan: %d", install.planRuns)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeCanceled {
		t.Fatalf("declining did not produce a canceled result: %+v", m.result)
	}
	// The sentence wraps, so compare on the text with whitespace removed.
	flat := strings.Join(strings.Fields(ansi.Strip(m.View())), " ")
	for _, want := range []string{msgApprovalDeclined, msgApprovalDeclinedAction} {
		if !strings.Contains(flat, strings.Join(strings.Fields(want), " ")) {
			t.Fatalf("decline screen missing %q:\n%s", want, flat)
		}
	}
}

// Approval is still a single deliberate key: space and the arrows must not run
// an operation. This is the documented divergence from the decision sheet's
// `[space] 승인` footer.
func TestInvariantApprovalStaysOneKey(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeySpace},
		{Type: tea.KeyDown},
		{Type: tea.KeyUp},
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune("y")},
	} {
		m := newTestModel(t, status, up)
		m.theme = *plainTheme()
		openConfirm(t, m)
		nm, cmd := m.Update(key)
		m = nm.(*Model)
		feed(t, m, execute(t, cmd))
		if up.runs != 0 {
			t.Fatalf("%q ran the operation", key.String())
		}
		if m.phase != phaseConfirm {
			t.Fatalf("%q left the confirmation (phase %v)", key.String(), m.phase)
		}
	}

	// The footer advertises the keys that act, and no accept key besides enter.
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	resize(t, m, 120, 36)
	view := m.View()
	if !strings.Contains(view, "[enter] 실행") || !strings.Contains(view, "[esc] 취소") {
		t.Fatalf("confirm footer lost its mandatory keys:\n%s", view)
	}
	if strings.Contains(view, "[space] 승인") {
		t.Fatalf("the footer advertises a space approval that does not exist:\n%s", view)
	}
}

// Quitting while a mutating run is in flight is still refused, and ctrl+c still
// requests a safe cancellation instead of quitting.
func TestInvariantQuitSafetyWhileMutating(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = *(nm.(*Model))
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want loading", m.phase)
	}

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)
	if cmd != nil && isQuit(t, cmd) {
		t.Fatal("q quit during a mutating run")
	}
	if !strings.Contains(m.View(), "일부 변경이 남을 수 있습니다") {
		t.Fatalf("the quit refusal does not explain the risk:\n%s", m.View())
	}

	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = nm.(*Model)
	if cmd != nil && isQuit(t, cmd) {
		t.Fatal("ctrl+c quit instead of requesting cancellation")
	}
	if !strings.Contains(m.View(), "중단을 요청했습니다") {
		t.Fatalf("cancellation was not reported:\n%s", m.View())
	}
}

// A read-only run keeps the old quick-quit behaviour.
func TestInvariantReadOnlyQuitIsNotBlocked(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	m.phase = phaseLoading
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	_ = nm
	if !isQuit(t, cmd) {
		t.Fatal("q did not quit a read-only run")
	}
}

// The service scope still never widens silently: the confirmation echoes the
// checked subset, and the run carries exactly those names.
func TestInvariantServiceScopeNeverWidens(t *testing.T) {
	m := scopeModel(t, scopeServices())
	resize(t, m, 120, 36)
	pressDown(m) // api
	pressSpace(m)
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirmation", m.phase)
	}
	row, ok := modalRow(m.View(), "범위:")
	if !ok || !strings.Contains(row, "api") {
		t.Fatalf("confirmation lost the chosen scope:\n%s", m.View())
	}
	if strings.Contains(row, "스택 전체") {
		t.Fatalf("a subset widened to the whole stack:\n%s", m.View())
	}
	if len(m.req.Services) != 1 || m.req.Services[0] != "api" {
		t.Fatalf("request services = %v", m.req.Services)
	}
}

// The TUI never emits an SGR sequence with the colorless theme, at every size,
// for every main screen. The frame structure is what remains.
func TestInvariantNoEscapeSequencesInColorlessTheme(t *testing.T) {
	for _, screen := range goldenScreens() {
		screen := screen
		t.Run(screen.name, func(t *testing.T) {
			m := screen.build(t)
			m.theme = visual.Plain()
			for _, size := range goldenSizes {
				resize(t, m, size[0], size[1])
				frame := m.View()
				if strings.Contains(frame, "\x1b") {
					t.Fatalf("%dx%d: colorless frame carries an escape sequence", size[0], size[1])
				}
			}
		})
	}
}

// Every state badge keeps an icon and a Korean label, so no state is encoded by
// color alone. This is the accessibility invariant on the real status document.
func TestInvariantEveryStateBadgeCarriesIconAndLabel(t *testing.T) {
	doc := status.New(nil).Document(goldenStatusResult(goldenSnapshot()))
	lines := visual.RenderDocument(visual.Plain(), doc, 120)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"✓ 실행 중", "◐ 재시작 중", "■ 중지됨", "○ 미생성",
		"정상", "비정상",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("status document missing the icon+label pair %q:\n%s", want, joined)
		}
	}
	// A bare icon with no Korean label is not a state statement.
	for _, line := range lines {
		for _, icon := range []string{"✓", "◐", "■", "○", "▲"} {
			trimmed := strings.TrimSpace(strings.ReplaceAll(line, icon, ""))
			if strings.Contains(line, icon) && trimmed == "" {
				t.Fatalf("row is an icon with no label: %q", line)
			}
		}
	}
}
