package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// openConfirm drives the menu to the plain confirmation modal of a mutating
// feature that has no plan.
func openConfirm(t *testing.T, m *Model) {
	t.Helper()
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = *(nm.(*Model))
}

func styledTarget(m *Model) {
	m.target = core.TargetRequest{
		Path:        "/Users/npnl/src/acme/acme-platform",
		Environment: "staging",
		Files:       []string{"compose.yaml", "compose.staging.yaml"},
		Profiles:    []string{"observability", "debug"},
	}
}

// modalBorders counts the panel's top and bottom border rows. Only a border
// exactly ModalWidth cells wide counts: the sidebar has a border of its own, and
// a resize must not make the panel's border ambiguous.
func modalBorders(th visual.Theme, lines []string, termWidth int) (top, bottom int) {
	want := visual.ModalWidth(termWidth)
	corner := func(line, left, right string) bool {
		i := strings.Index(line, left)
		if i < 0 {
			return false
		}
		rest := line[i:]
		if !strings.Contains(rest, right) {
			return false
		}
		return visual.Width(ansi.Strip(strings.SplitN(rest, right, 2)[0]))+1 == want
	}
	for _, line := range lines {
		if corner(line, th.Glyphs.TL, th.Glyphs.TR) {
			top++
		}
		if corner(line, th.Glyphs.BL, th.Glyphs.BR) {
			bottom++
		}
	}
	return top, bottom
}

// The confirmation is a centered panel: every line fits the terminal, the
// panel appears once, and the frame keeps its exact height.
func TestConfirmModalIsCenteredAndFitsEveryWidth(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	for _, size := range [][2]int{{120, 36}, {80, 24}, {48, 20}} {
		m := newTestModel(t, status, up)
		m.theme = *plainTheme()
		styledTarget(m)
		openConfirm(t, m)
		resize(t, m, size[0], size[1])
		lines := viewLines(t, m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if got := visual.Width(line); got > size[0] {
				t.Fatalf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, line)
			}
		}
		if top, bottom := modalBorders(m.theme, lines, size[0]); top != 1 || bottom != 1 {
			t.Fatalf("%dx%d: panel borders top=%d bottom=%d:\n%s", size[0], size[1], top, bottom, strings.Join(lines, "\n"))
		}
		// The panel is somewhere in the body, never over the header or footer.
		if strings.Contains(lines[0], m.theme.Glyphs.TL) {
			t.Fatalf("%dx%d: panel starts on the header row", size[0], size[1])
		}
		if strings.Contains(lines[len(lines)-1], m.theme.Glyphs.BL) {
			t.Fatalf("%dx%d: panel ends on the footer row", size[0], size[1])
		}
	}
}

// The nine approved fields are always present and in the approved order, at
// every width.
func TestConfirmModalKeepsTheApprovedFieldOrder(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	for _, size := range [][2]int{{120, 36}, {48, 20}} {
		m := newTestModel(t, status, up)
		m.theme = *plainTheme()
		styledTarget(m)
		openConfirm(t, m)
		resize(t, m, size[0], size[1])
		view := m.View()
		// The risk badge is decoration and may sit in the top border; the
		// approved order is about the fields and the pinned risk statement.
		order := []string{"작업:", "대상:", "실행환경:", "Compose 파일:", "프로필:", "프로젝트 이름:", "범위:", "효과:", "스택을 변경합니다"}
		last := -1
		for _, needle := range order {
			idx := strings.Index(view, needle)
			if idx < 0 {
				t.Fatalf("%dx%d: %q is missing:\n%s", size[0], size[1], needle, view)
			}
			if idx < last {
				t.Fatalf("%dx%d: %q is out of order:\n%s", size[0], size[1], needle, view)
			}
			last = idx
		}
	}
}

// Risk, target and scope survive the narrowest approved width. They are not the
// part that gets cut.
func TestConfirmModalKeepsRiskTargetAndScopeAt48(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	styledTarget(m)
	openConfirm(t, m)
	// A plan far longer than the panel, so the scroll band really overflows.
	m.planText = strings.Repeat("  - web: 이미지 취득(nginx:1.27) 후 적용\n", 20)
	resize(t, m, 48, 20)
	view := m.View()
	// The target path is shortened from its end with an explicit marker (a
	// path must not wrap); the label and a readable prefix stay.
	for _, want := range []string{"대상:", "/Users/", "범위:", "스택 전체", "위험", "스택을 변경합니다"} {
		if !strings.Contains(view, want) {
			t.Fatalf("48x20 modal missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("overflowing modal does not state its scroll position:\n%s", view)
	}
	// The whole plan is still reachable inside the panel.
	for i := 0; i < 40; i++ {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = nm.(*Model)
	}
	if !strings.Contains(m.View(), "이미지 취득") {
		t.Fatalf("plan is unreachable:\n%s", m.View())
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 48 {
			t.Fatalf("row %d too wide: %q", i, line)
		}
	}
}

// Every independent approval kind is listed with its existing Korean label.
func TestConfirmModalListsEveryApprovalKindInKorean(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	install := &stubPlanner{stubFeature: &stubFeature{desc: approvalDescriptor2("install-compose"), body: "설치 본문"}}
	m := newTestModel(t, status, install)
	m.theme = *plainTheme()
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirm", m.phase)
	}
	m.pendingApprovals = []core.ApprovalKind{
		core.ApprovalInstall, core.ApprovalScriptTrust, core.ApprovalPreviousGood,
		core.ApprovalDataRestore, core.ApprovalFileOverwrite,
	}
	resize(t, m, 120, 36)
	view := m.View()
	for _, kind := range m.pendingApprovals {
		if !strings.Contains(view, kind.KoreanLabel()) {
			t.Fatalf("approval %q (%s) is not listed:\n%s", kind.KoreanLabel(), kind, view)
		}
	}
	if !strings.Contains(view, "필요한 승인") {
		t.Fatalf("approval section heading missing:\n%s", view)
	}
}

// esc declines: nothing runs, and the result says so.
func TestDecliningTheModalLeavesANothingWasRunResult(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	install := &stubPlanner{stubFeature: &stubFeature{desc: approvalDescriptor2("install-compose"), body: "설치 본문"}}
	m := newTestModel(t, status, install)
	m.theme = *plainTheme()
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirm", m.phase)
	}
	planRuns := install.planRuns
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if install.runs != 0 {
		t.Fatalf("declining ran the feature: %d", install.runs)
	}
	if install.planRuns != planRuns {
		t.Fatalf("declining started another plan: %d -> %d", planRuns, install.planRuns)
	}
	view := m.View()
	if !strings.Contains(view, "아무것도 실행하지 않았습니다") || !strings.Contains(view, "변경된 내용은 없습니다") {
		t.Fatalf("decline left no result:\n%s", view)
	}
	if strings.Contains(view, "승인 확인") {
		t.Fatalf("modal is still open after declining:\n%s", view)
	}
}

// An ordinary confirmation has no approval to decline: esc returns to the
// screen the user came from and keeps its result.
func TestClosingAnOrdinaryConfirmationKeepsThePreviousResult(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	before := m.result
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if up.runs != 0 {
		t.Fatalf("closing the confirmation ran the feature: %d", up.runs)
	}
	if m.result != before {
		t.Fatalf("closing an ordinary confirmation replaced the result: %+v", m.result)
	}
	if !strings.Contains(m.View(), "상태 본문") {
		t.Fatalf("previous result lost:\n%s", m.View())
	}
}

// space is the approved second acknowledge key; it approves exactly like enter.
func TestSpaceApprovesLikeEnter(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if up.runs != 1 {
		t.Fatalf("space did not approve: runs = %d", up.runs)
	}
}

// The wizard's approval apply lands on the same modal; there is exactly one
// approval surface.
func TestWizardApprovalUsesTheSameConfirmModal(t *testing.T) {
	stub := &wizardStub{
		desc: core.Descriptor{
			Name: "restore", Title: "데이터 복원", Summary: "요약", Usage: "dcraft restore",
			Section: core.SectionData, Wizard: true, Approval: core.ApprovalDataRestore,
		},
		steps: core.WizardSpec{
			Title: "데이터 복원 설정",
			Steps: []core.WizardStep{{
				Name: "volume", Title: "복원 대상 볼륨 선택", Prompt: "볼륨",
				Options: []core.WizardOption{{Value: "data", Label: "data → proj_data"}},
			}},
		},
		preview: "복원 계획\n대상: data → proj_data",
	}
	wizard := &approvalWizardStub{wizardStub: stub, planText: "복원 계획\n영향 서비스: app"}
	m := newWizardModel(t, wizard)
	m.theme = *plainTheme()
	openWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirm; view:\n%s", m.phase, m.View())
	}
	resize(t, m, 120, 36)
	lines := viewLines(t, m)
	if top, bottom := modalBorders(m.theme, lines, 120); top != 1 || bottom != 1 {
		t.Fatalf("wizard approval is not the shared modal:\n%s", strings.Join(lines, "\n"))
	}
	for _, want := range []string{"승인 확인", "데이터 복원", "data_restore", "영향 서비스: app", "[enter] 실행", "[esc] 취소"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("wizard approval modal missing %q:\n%s", want, m.View())
		}
	}
	if stub.applied {
		t.Fatal("wizard applied before the approval")
	}
}

// Help lists the keys of the screen it was opened from, closes with esc, and
// runs nothing.
func TestHelpListsOnlyCurrentScreenKeysAndEscCloses(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	runs := status.runs

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	if !m.helpOpen {
		t.Fatal("? did not open help")
	}
	view := m.View()
	for _, want := range []string{"도움말", "[enter] 실행", "[q] 종료"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu help missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[space] 승인") {
		t.Fatalf("menu help listed a confirm-only key:\n%s", view)
	}
	// The overlay's own escape is a keycap, in the footer only.
	lines := viewLines(t, m)
	if !strings.Contains(lines[len(lines)-1], "[esc] 닫기") {
		t.Fatalf("help footer does not state the way out: %q", lines[len(lines)-1])
	}
	if n := strings.Count(view, "[esc] 닫기"); n != 1 {
		t.Fatalf("%d esc chips for the help overlay:\n%s", n, view)
	}
	if status.runs != runs {
		t.Fatalf("help ran the feature")
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.helpOpen {
		t.Fatal("esc did not close help")
	}
	if run := status.runs; run != runs {
		t.Fatalf("closing help ran the feature: %d -> %d", runs, run)
	}

	// On the confirmation modal the help lists the confirm keys and not the
	// menu's.
	openConfirm(t, m)
	resize(t, m, 120, 36)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	view = m.View()
	for _, want := range []string{"[enter] 실행", "[esc] 취소", "[space] 승인"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirm help missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[/] 찾기") || strings.Contains(view, "[r] 새로고침") {
		t.Fatalf("confirm help listed menu keys:\n%s", view)
	}
	if up.runs != 0 {
		t.Fatalf("help from the modal ran the feature: %d", up.runs)
	}
}

// The keycap footer stays the only keycap surface: the panel body repeats no
// chips.
func TestModalDoesNotDuplicateKeycapsInThePanel(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	resize(t, m, 120, 36)
	lines := viewLines(t, m)
	rows := 0
	for _, line := range lines {
		if strings.Contains(line, "[enter] 실행") {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("%d rows carry the enter chip:\n%s", rows, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[len(lines)-1], "[esc] 취소") || !strings.Contains(lines[len(lines)-1], "[space] 승인") {
		t.Fatalf("confirm footer is not the approved row: %q", lines[len(lines)-1])
	}
}

// Resizing while the modal is open reflows it: same state, one frame, no
// overlap.
func TestResizeWhileTheModalIsOpenKeepsTheFrame(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	styledTarget(m)
	openConfirm(t, m)
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v", m.phase)
	}
	for _, size := range [][2]int{{120, 36}, {48, 20}, {200, 50}, {80, 24}, {40, 18}} {
		resize(t, m, size[0], size[1])
		if m.phase != phaseConfirm {
			t.Fatalf("%dx%d: resize left the confirmation", size[0], size[1])
		}
		lines := viewLines(t, m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows:\n%s", size[0], size[1], len(lines), strings.Join(lines, "\n"))
		}
		for i, line := range lines {
			if visual.Width(line) > size[0] {
				t.Fatalf("%dx%d row %d too wide: %q", size[0], size[1], i, line)
			}
		}
		if top, bottom := modalBorders(m.theme, lines, size[0]); top != 1 || bottom != 1 {
			t.Fatalf("%dx%d: panel borders top=%d bottom=%d:\n%s", size[0], size[1], top, bottom, strings.Join(lines, "\n"))
		}
		if !strings.Contains(m.View(), "스택을 변경합니다") {
			t.Fatalf("%dx%d: risk was lost on resize:\n%s", size[0], size[1], m.View())
		}
		if !strings.Contains(m.View(), "대상") || !strings.Contains(m.View(), "범위") {
			t.Fatalf("%dx%d: target or scope was lost on resize:\n%s", size[0], size[1], m.View())
		}
	}
}

// The help overlay is itself a centered panel: it never widens the frame.
func TestHelpOverlayFitsNarrowWidths(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 48, 20)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 48 {
			t.Fatalf("help row %d too wide: %q", i, line)
		}
	}
	if top, bottom := modalBorders(m.theme, viewLines(t, m), 48); top != 1 || bottom != 1 {
		t.Fatalf("help is not a single panel:\n%s", m.View())
	}
}

// A read-only run in flight does not offer help: the approved busy row has no
// help key, and the overlay must not claim a key that does nothing.
func TestHelpIsNotOfferedWhileAnOperationRuns(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	if m.helpOpen {
		t.Fatal("help opened during a mutating run")
	}
	if strings.Contains(m.View(), "도움말") {
		t.Fatalf("busy screen advertises help:\n%s", m.View())
	}
}

// approvalDescriptor2 is a non-primary approval descriptor: a second primary
// would be rejected by the registry.
func approvalDescriptor2(name string) core.Descriptor {
	d := approvalDescriptor(name)
	d.Primary = false
	return d
}

// A very short terminal still states the risk: the panel drops decoration
// before it drops the statement that the stack changes.
func TestConfirmModalKeepsRiskEvenWhenVeryShort(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	styledTarget(m)
	openConfirm(t, m)
	m.planText = strings.Repeat("  - web: 이미지 취득 후 적용\n", 30)
	for _, size := range [][2]int{{48, 10}, {48, 8}, {80, 6}, {40, 7}} {
		resize(t, m, size[0], size[1])
		lines := viewLines(t, m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if visual.Width(line) > size[0] {
				t.Fatalf("%dx%d row %d too wide: %q", size[0], size[1], i, line)
			}
		}
		if !strings.Contains(m.View(), "스택을 변경합니다") {
			t.Fatalf("%dx%d: risk disappeared:\n%s", size[0], size[1], m.View())
		}
	}
}

// The dim contract on the real model: the header and the keycap footer keep
// their exact rows and contrast, and the body rows outside the panel are the
// muted paint of the same text.
func TestConfirmModalDimsOnlyTheBodyOnTheRealFrame(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = visual.Resolve(visual.Inputs{
		ThemeSetting: "dark", Profile: visual.ProfileTrueColor, DarkBackground: true, UTF8: true,
	})
	styledTarget(m)
	openConfirm(t, m)
	resize(t, m, 120, 36)

	modalFrame := strings.Split(m.View(), "\n")
	wantHeader := visual.Fit(visual.Header(m.theme, 120, m.facts), 120, m.theme.Glyphs.Ell)
	if modalFrame[0] != wantHeader {
		t.Fatalf("header contrast changed:\n got %q\nwant %q", modalFrame[0], wantHeader)
	}
	last := len(modalFrame) - 1
	wantFooter := visual.Fit(visual.Row(m.theme, m.footerItems(), 120), 120, m.theme.Glyphs.Ell)
	if modalFrame[last] != wantFooter {
		t.Fatalf("footer contrast changed:\n got %q\nwant %q", modalFrame[last], wantFooter)
	}
	// Neither band is the muted paint of itself: they are not dimmed.
	for _, row := range []string{modalFrame[0], modalFrame[last]} {
		text := ansi.Strip(row)
		if text != "" && row == m.theme.Paint(visual.TokenMuted, text) {
			t.Fatalf("a frame band was dimmed: %q", row)
		}
	}

	// A body row outside the panel is exactly the muted paint of the
	// undimmed text: content kept, contrast lowered.
	bgModel := m.backgroundModel()
	bgModel.theme = m.theme
	plainFrame := bgModel.composeFrame(bgModel.renderBody())
	// The sidebar has a border of its own; the panel's border is the one that
	// is ModalWidth cells wide.
	panelWidth := visual.ModalWidth(120)
	panelTop := -1
	for i, line := range modalFrame {
		plain := ansi.Strip(line)
		start := strings.Index(plain, m.theme.Glyphs.TL)
		if start < 0 || !strings.Contains(plain[start:], m.theme.Glyphs.TR) {
			continue
		}
		border := strings.SplitN(plain[start:], m.theme.Glyphs.TR, 2)[0]
		if visual.Width(border)+1 != panelWidth {
			continue
		}
		panelTop = i
		break
	}
	if panelTop < 2 {
		t.Fatalf("panel top row not found:\n%s", strings.Join(modalFrame, "\n"))
	}
	row := 1
	want := m.theme.Paint(visual.TokenMuted, ansi.Strip(plainFrame[row]))
	if modalFrame[row] != want {
		t.Fatalf("body row %d is not the muted paint of the same text:\n got %q\nwant %q", row, modalFrame[row], want)
	}
	// The risk inside the panel is the danger-bold paint on the panel plate,
	// not a muted one. The label is a separate span, so the exact sequence is
	// checked rather than a whole-string helper.
	r, g, b := visual.HexRGB(m.theme.Palette.Danger)
	sr, sg, sb := visual.HexRGB(m.theme.Palette.Surface)
	dangerBold := fmt.Sprintf("\x1b[0;1;38;2;%d;%d;%d;48;2;%d;%d;%dm", r, g, b, sr, sg, sb)
	riskRow := ""
	for _, line := range modalFrame {
		if strings.Contains(ansi.Strip(line), "스택을 변경합니다") {
			riskRow = line
			break
		}
	}
	if riskRow == "" {
		t.Fatalf("risk row not found:\n%s", strings.Join(modalFrame, "\n"))
	}
	if !strings.Contains(riskRow, dangerBold+"위험 이 작업은") {
		t.Fatalf("risk label and sentence are not one danger-bold span:\n%q", riskRow)
	}
}

// A "?" typed into a free-text answer is text, not the help key. The overlay
// only opens where the footer advertises it.
func TestHelpNeverStealsATypedQuestionMark(t *testing.T) {
	stub := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Section: core.SectionConfig, Wizard: true,
		},
		steps: core.WizardSpec{Title: "비밀값 연결 설정", Steps: []core.WizardStep{{
			Name: "ref", Title: "원본 참조", Prompt: "참조를 입력하세요.", AllowCustom: true,
		}}},
	}
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	if !m.wizardTextInput() {
		t.Fatalf("phase = %v, want a free-text step", m.phase)
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	if m.helpOpen {
		t.Fatal("help opened over a free-text answer")
	}
	if m.wizardInput != "?" {
		t.Fatalf("typed %q into the answer, want %q", m.wizardInput, "?")
	}

	// The same key on a choice step opens the keymap instead.
	stub2 := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Section: core.SectionConfig, Wizard: true,
		},
		steps: core.WizardSpec{Title: "비밀값 연결 설정", Steps: []core.WizardStep{{
			Name: "service", Title: "서비스 선택", Prompt: "서비스를 선택하세요.",
			Options: []core.WizardOption{{Value: "web", Label: "web"}},
		}}},
	}
	m2 := newWizardModel(t, stub2)
	m2.theme = *plainTheme()
	openWizard(t, m2)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m2 = nm.(*Model)
	if !m2.helpOpen {
		t.Fatal("help did not open on a choice step")
	}
}

// A resize with the help panel open reflows it: still one panel, still the
// exact frame height.
func TestResizeWhileHelpIsOpenKeepsTheFrame(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, status, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = nm.(*Model)
	for _, size := range [][2]int{{48, 20}, {200, 50}, {80, 24}, {40, 18}} {
		resize(t, m, size[0], size[1])
		if !m.helpOpen {
			t.Fatalf("%dx%d: resize closed the help panel", size[0], size[1])
		}
		lines := viewLines(t, m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if visual.Width(line) > size[0] {
				t.Fatalf("%dx%d row %d too wide: %q", size[0], size[1], i, line)
			}
		}
		if top, bottom := modalBorders(m.theme, lines, size[0]); top != 1 || bottom != 1 {
			t.Fatalf("%dx%d: help borders top=%d bottom=%d:\n%s", size[0], size[1], top, bottom, strings.Join(lines, "\n"))
		}
		if !strings.Contains(lines[len(lines)-1], "[esc] 닫기") {
			t.Fatalf("%dx%d: help footer missing: %q", size[0], size[1], lines[len(lines)-1])
		}
	}
}
