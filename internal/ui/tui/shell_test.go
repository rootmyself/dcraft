package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// plainTheme is a deterministic injected theme: no escape sequences ever.
func plainTheme() *visual.Theme {
	th := visual.Resolve(visual.Inputs{
		ThemeSetting: "dark", NoColor: true, Profile: visual.ProfileNone,
		DarkBackground: true, UTF8: true,
	})
	return &th
}

func viewLines(t *testing.T, m *Model) []string {
	t.Helper()
	view := strings.TrimSuffix(m.View(), "\n")
	return strings.Split(view, "\n")
}

func resize(t *testing.T, m *Model, w, h int) {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	*m = *(nm.(*Model))
}

// Every frame fits the terminal exactly, at every approved breakpoint. This is
// the structural width contract, checked on the real model rather than on a
// helper.
func TestViewFitsEveryApprovedBreakpoint(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true),
		body: strings.Join([]string{
			"프로젝트: acme-platform",
			"폴더: /Users/npnl/src/acme/acme-platform",
			"서비스 상태",
			strings.Repeat("아주 긴 설명 ", 12),
		}, "\n")}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	for _, size := range [][2]int{{40, 18}, {80, 24}, {120, 36}, {200, 50}} {
		m := newTestModel(t, status, logs)
		m.theme = *plainTheme()
		initModel(t, m)
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
		if !strings.Contains(lines[0], "dcraft") {
			t.Fatalf("%dx%d: header missing: %q", size[0], size[1], lines[0])
		}
		if !strings.Contains(lines[len(lines)-1], "종료") {
			t.Fatalf("%dx%d: footer missing: %q", size[0], size[1], lines[len(lines)-1])
		}
	}
}

// 48x18 keeps the required facts: the current target, the current stage and the
// risk sentence. The frame may drop decoration and truncate, never these.
func TestCompact48x18KeepsTargetStageAndRisk(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 48, 18)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirm", m.phase)
	}
	view := m.View()
	for _, want := range []string{msgConfirmTitle, "대상", "스택 전체", "스택을 변경하는", "[enter] 실행", "[esc] 취소"} {
		if !strings.Contains(view, want) {
			t.Fatalf("48x18 confirmation missing %q:\n%s", want, view)
		}
	}

	// The loading screen keeps the observed stage, even in compact.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	if cmd := m.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ = m.Update(stageMsg{stage: core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageStarted}})
	m = nm.(*Model)
	resize(t, m, 48, 18)
	if view := m.View(); !strings.Contains(view, "환경 확인") {
		t.Fatalf("48x18 loading dropped the stage:\n%s", view)
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 48 {
			t.Fatalf("loading row %d too wide: %q", i, line)
		}
	}
}

// A resize reflows immediately and loses neither the last result nor the
// current stage.
func TestResizePreservesResultAndStage(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "서비스 상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)

	for _, size := range [][2]int{{120, 36}, {40, 18}, {200, 50}, {80, 24}} {
		resize(t, m, size[0], size[1])
		if !strings.Contains(m.View(), "서비스 상태 본문") {
			t.Fatalf("%dx%d lost the last result:\n%s", size[0], size[1], m.View())
		}
		if m.result == nil {
			t.Fatalf("%dx%d dropped the result value", size[0], size[1])
		}
	}

	// Now the stage half: start a mutating run and resize mid-flight.
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m2 := newTestModel(t, status, up)
	m2.theme = *plainTheme()
	initModel(t, m2)
	resize(t, m2, 120, 36)
	if cmd := m2.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	nm, _ := m2.Update(stageMsg{stage: core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageStarted}})
	m2 = nm.(*Model)
	if m2.currentStage == nil || m2.currentStage.Title != "환경 확인" {
		t.Fatalf("stage not recorded: %+v", m2.currentStage)
	}
	for _, size := range [][2]int{{40, 18}, {80, 24}, {200, 50}} {
		resize(t, m2, size[0], size[1])
		if !strings.Contains(m2.View(), "환경 확인") {
			t.Fatalf("%dx%d lost the current stage:\n%s", size[0], size[1], m2.View())
		}
	}
}

// The footer is the phase keycap row, not the old hint sentence.
func TestFooterFollowsThePhase(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, status, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	if view := m.View(); !strings.Contains(view, "[↑↓] 이동") || !strings.Contains(view, "[r] 새로고침") {
		t.Fatalf("menu footer missing:\n%s", view)
	}

	// Confirm phase.
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m2 := newTestModel(t, status, up)
	m2.theme = *plainTheme()
	initModel(t, m2)
	nm, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m2 = nm.(*Model)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = nm.(*Model)
	resize(t, m2, 120, 36)
	view := m2.View()
	for _, want := range []string{"[enter] 실행", "[esc] 취소"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirm footer missing %q:\n%s", want, view)
		}
	}

	// Form phase.
	primary := &stubFeature{desc: descriptor("status", true), body: "상태"}
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{desc: inputDescriptor("attach"), mode: core.LiveAttach},
		fields: []core.InputField{{Key: "command", Label: "명령", Kind: core.InputText, Required: true}}}
	m3 := newTestModel(t, primary, input)
	m3.theme = *plainTheme()
	initModel(t, m3)
	nm, _ = m3.Update(tea.KeyMsg{Type: tea.KeyDown})
	m3 = nm.(*Model)
	nm, cmd := m3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m3 = nm.(*Model)
	feed(t, m3, execute(t, cmd))
	resize(t, m3, 120, 36)
	if view := m3.View(); !strings.Contains(view, "[tab] 다음 항목") || !strings.Contains(view, "[enter] 제출") {
		t.Fatalf("form footer missing:\n%s", view)
	}

	// Busy phase: q is refused, ctrl+c is the safe action.
	m4 := newTestModel(t, status, up)
	m4.theme = *plainTheme()
	initModel(t, m4)
	if cmd := m4.begin(up); cmd == nil {
		t.Fatal("begin returned no command")
	}
	resize(t, m4, 120, 36)
	view = m4.View()
	if !strings.Contains(view, "[ctrl+c] 안전 취소") || !strings.Contains(view, "[q] 종료 불가") {
		t.Fatalf("busy footer missing:\n%s", view)
	}
}

// Live screens advertise detach and stop, not menu navigation.
func TestFooterLivePhase(t *testing.T) {
	m := newTestModel(t, &stubFeature{desc: descriptor("status", true), body: "body"})
	m.theme = *plainTheme()
	m.live = &liveState{mode: core.LiveStream}
	if got := m.footerPhase(); got != visual.PhaseLive {
		t.Fatalf("live footer phase = %v", got)
	}
	resize(t, m, 120, 36)
	view := m.View()
	if !strings.Contains(view, "[q] 화면 닫기") || !strings.Contains(view, "[ctrl+] 접속 종료") {
		t.Fatalf("live footer missing:\n%s", view)
	}
	m.live = nil
}

// The header shows the stack identity and only the last observed tool facts; a
// redraw must not trigger a new check.
type countingPreflight struct {
	version string
	calls   int
}

func (p *countingPreflight) Check(ctx context.Context, req core.Requirements) core.PreflightReport {
	p.calls++
	return core.PreflightReport{Tools: core.ToolInfo{
		DockerVersion: p.version, ComposeVersion: "2.31.0", EndpointKind: "unix",
	}}
}

func TestHeaderShowsLastObservedFactsWithoutPolling(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	reg := core.NewRegistry()
	if err := reg.Register(status); err != nil {
		t.Fatal(err)
	}
	pre := &countingPreflight{version: "27.4.1"}
	engine := core.NewEngine(reg, stubResolver{}, pre, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_shell" }
	m := NewModel(context.Background(), Options{
		Engine: engine, Registry: reg, Theme: plainTheme(),
		Target: core.TargetRequest{Path: "/work/acme-platform", Environment: "staging"},
	})
	initModel(t, m)
	resize(t, m, 120, 36)

	header := viewLines(t, m)[0]
	for _, want := range []string{"dcraft", "acme-platform", "staging", "Docker 27.4.1", "Compose 2.31.0", "로컬"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q: %q", want, header)
		}
	}
	calls := pre.calls
	for i := 0; i < 5; i++ {
		_ = m.View()
	}
	if pre.calls != calls {
		t.Fatalf("rendering polled Docker: %d -> %d checks", calls, pre.calls)
	}
}

// A project name that only becomes known after the operation updates the
// header from the last report; no extra call is made.
func TestHeaderUpdatesFromTheLastReport(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)

	nm, _ := m.Update(resultMsg{res: core.Result{
		SchemaVersion: core.SchemaVersion, Action: "status", Outcome: core.OutcomeSuccess,
		Target: core.Target{ProjectDir: "/work/acme", ProjectName: "acme-prod", Environment: "prod"},
		Tools:  core.ToolInfo{DockerVersion: "28.0.1", ComposeVersion: "2.40.0"},
		Stages: []core.Stage{}, Diagnostics: []core.Diagnostic{},
	}})
	m = nm.(*Model)
	header := viewLines(t, m)[0]
	for _, want := range []string{"acme-prod", "prod", "Docker 28.0.1", "Compose 2.40.0"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header did not update with %q: %q", want, header)
		}
	}
}

// The footer is the only place keycaps appear; one per screen.
func TestOnlyOneFooterPerScreen(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	lines := viewLines(t, m)
	footerRows := 0
	for _, line := range lines {
		if strings.Contains(line, "[enter] 실행") && strings.Contains(line, "[q] 종료") {
			footerRows++
		}
	}
	if footerRows != 1 {
		t.Fatalf("%d footer rows:\n%s", footerRows, m.View())
	}
}

// Nothing in the frame emits an escape sequence when the resolved theme is
// colorless. That is what keeps --json and CLI text paths safe by construction:
// they never receive the TUI's view, and a colorless view holds no ANSI either.
func TestColorlessViewHasNoEscapes(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	if strings.Contains(m.View(), "\x1b") {
		t.Fatalf("colorless view has escapes:\n%q", m.View())
	}
}

// An ASCII theme (non-UTF-8 locale) replaces the ellipsis and the selection
// glyph everywhere in the frame. The marker proves that a cut value is visible
// as a cut: a long path is truncated in its key-value row, never wrapped.
func TestASCIIThemeInTheFrame(t *testing.T) {
	ascii := visual.Resolve(visual.Inputs{ThemeSetting: "dark", NoColor: true,
		Profile: visual.ProfileNone, DarkBackground: true, UTF8: false})
	long := "/Users/npnl/Documents/work/an-extremely-long-project-folder-name/compose.yaml"
	doc := &documentOnlyFeature{desc: descriptor("status", true), doc: core.Document{Blocks: []core.Block{
		{Kind: core.BlockTable, Table: &core.Table{
			Columns: []core.Column{{Header: "서비스", Weight: 30, Min: 12}, {Header: "이미지", Weight: 70, Min: 8}},
			Rows:    []core.Row{{Selectable: true, Cells: []core.Cell{{Text: "api"}, {Text: long, Detail: long}}}},
		}},
	}}}
	m := newTestModel(t, doc)
	m.theme = ascii
	initModel(t, m)
	resize(t, m, 48, 18)
	view := m.View()
	if strings.Contains(view, "…") {
		t.Fatalf("ASCII frame has a UTF-8 ellipsis:\n%s", view)
	}
	if !strings.Contains(view, "~") {
		t.Fatalf("ASCII frame has no truncation marker:\n%s", view)
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 48 {
			t.Fatalf("ASCII row %d too wide: %q", i, line)
		}
	}
}

// A long plan must not push the risk sentence out of a short frame: the risk
// is pinned inside the panel, above its bottom border, and the plan scrolls
// under it. The old truncation notice is gone: the screen states the position
// instead of apologising.
func TestCompactConfirmPinsRiskAboveThePanelBorder(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 48, 18)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	// A plan far longer than the frame.
	m.planText = strings.Repeat("  - web: 이미지 취득(nginx:1.27) 후 적용\n", 20)

	lines := viewLines(t, m)
	if len(lines) != 18 {
		t.Fatalf("rows = %d", len(lines))
	}
	bottom := -1
	for i, line := range lines {
		if strings.Contains(line, m.theme.Glyphs.BL) && strings.Contains(line, m.theme.Glyphs.BR) {
			bottom = i
			break
		}
	}
	if bottom < 1 {
		t.Fatalf("panel border missing:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[bottom-1], "스택을 변경하는") {
		t.Fatalf("risk is not the last row inside the panel: %q", lines[bottom-1])
	}
	if !strings.Contains(lines[17], "[enter] 실행") {
		t.Fatalf("footer missing: %q", lines[17])
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "생략") {
		t.Fatalf("the deleted truncation notice is still rendered:\n%s", joined)
	}
	if !strings.Contains(joined, "PgUp/PgDn") {
		t.Fatalf("overflow is not navigable:\n%s", joined)
	}
	// The whole plan is reachable: G scrolls to its last line.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = nm.(*Model)
	reached := m.View()
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 48 {
			t.Fatalf("row %d too wide: %q", i, line)
		}
	}
	if !strings.Contains(reached, "이미지 취득") {
		t.Fatalf("the end of the plan is unreachable:\n%s", reached)
	}
}

// A colored theme changes paint only: the visible characters, row count and
// widths are identical to the colorless frame.
func TestColoredFrameHasTheSameVisibleLayoutAsPlain(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "서비스 상태 본문"}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}

	plain := newTestModel(t, status, logs)
	plain.theme = *plainTheme()
	initModel(t, plain)
	resize(t, plain, 120, 36)

	colored := newTestModel(t, status, logs)
	colored.theme = visual.Resolve(visual.Inputs{
		ThemeSetting: "dark", Profile: visual.ProfileTrueColor, DarkBackground: true, UTF8: true,
	})
	initModel(t, colored)
	resize(t, colored, 120, 36)

	if !strings.Contains(colored.View(), "\x1b[") {
		t.Fatal("colored theme emitted no styling")
	}
	plainLines := viewLines(t, plain)
	coloredLines := viewLines(t, colored)
	if len(plainLines) != len(coloredLines) {
		t.Fatalf("row counts differ: %d vs %d", len(plainLines), len(coloredLines))
	}
	for i := range plainLines {
		if got := stripEscapes(coloredLines[i]); got != plainLines[i] {
			t.Fatalf("row %d differs:\n plain: %q\ncolored: %q", i, plainLines[i], got)
		}
	}
}

func stripEscapes(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Every status glyph the TUI draws comes from the theme, so a non-UTF-8 locale
// gets ASCII symbols while the Korean status words stay exact.
func TestStageGlyphFollowsTheTheme(t *testing.T) {
	ascii := visual.ASCIIGlyphs()
	utf8 := visual.UTF8Glyphs()
	cases := []struct {
		status core.StageStatus
		utf8   string
		ascii  string
	}{
		{core.StageSucceeded, utf8.OK, ascii.OK},
		{core.StageFailed, utf8.Danger, ascii.Danger},
		{core.StageCanceled, utf8.Stopped, ascii.Stopped},
		{core.StageStarted, utf8.Spin[0], ascii.Spin[0]},
		{core.StageSkipped, utf8.Dot, ascii.Dot},
	}
	for _, tc := range cases {
		if got := stageGlyph(visual.Resolve(visual.Inputs{UTF8: true, ThemeSetting: "dark"}), tc.status); got != tc.utf8 {
			t.Fatalf("%s UTF-8 glyph = %q, want %q", tc.status, got, tc.utf8)
		}
		if got := stageGlyph(visual.Resolve(visual.Inputs{UTF8: false, ThemeSetting: "dark"}), tc.status); got != tc.ascii {
			t.Fatalf("%s ASCII glyph = %q, want %q", tc.status, got, tc.ascii)
		}
	}
}

// Every timeline row keeps the shared Korean status wording ("진행 중", "완료",
// "예정") so the TUI and CLI never drift.
func TestTimelineRowsKeepTheKoreanStatusWords(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	cases := []struct {
		stage core.Stage
		want  string
	}{
		{core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageSucceeded}, "완료"},
		{core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageStarted}, "진행 중"},
		{core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageFailed}, "실패"},
		{core.Stage{Name: "preflight", Title: "환경 확인", Status: core.StageCanceled}, "중단"},
		{core.Stage{Name: "preflight", Title: "환경 확인"}, msgStagePending},
	}
	for _, tc := range cases {
		line := m.timelineRowLine(timelineRow{stage: tc.stage})
		for _, want := range []string{"환경 확인", tc.want} {
			if !strings.Contains(line, want) {
				t.Fatalf("timeline row missing %q: %q", want, line)
			}
		}
	}
}

// A glyph a shared renderer embeds (view.StageList's ✕) must still fall back in
// a non-UTF-8 locale. The frame applies the closed glyph table, so coverage
// does not depend on every producer remembering it.
func TestASCIIFrameReplacesForeignGlyphs(t *testing.T) {
	ascii := visual.Resolve(visual.Inputs{ThemeSetting: "dark", NoColor: true,
		Profile: visual.ProfileNone, DarkBackground: true, UTF8: false})
	status := &stubFeature{desc: descriptor("status", true), outcome: core.OutcomeFailure}
	m := newTestModel(t, status)
	m.theme = ascii
	initModel(t, m)
	resize(t, m, 80, 24)
	view := m.View()
	if strings.Contains(view, "✕") {
		t.Fatalf("ASCII frame kept a shared renderer's glyph:\n%s", view)
	}
	for _, r := range "╭╮╰╯─│▸…✓◐■○▲✕█·" {
		if strings.ContainsRune(view, r) {
			t.Fatalf("ASCII frame contains %q:\n%s", r, view)
		}
	}
	if !strings.Contains(view, "작업 실패") {
		t.Fatalf("Korean wording was damaged:\n%s", view)
	}
}

// The header states only facts it observed. A failed run that reports no tool
// versions must not erase the versions the last successful observation gave,
// and a renamed project must update on the next report.
func TestObservedFactsOnlyEverAdvance(t *testing.T) {
	m := &Model{facts: visual.Facts{Project: "known", Environment: "dev"}}
	m.observeTools(core.ToolInfo{DockerVersion: "27.4.1", ComposeVersion: "2.31.0"})
	m.observeTarget(core.Target{})
	if m.facts.Project != "known" || m.facts.Environment != "dev" {
		t.Fatalf("an empty report erased known facts: %+v", m.facts)
	}
	if m.facts.Docker != "27.4.1" || m.facts.Compose != "2.31.0" {
		t.Fatalf("tools = %+v", m.facts)
	}
	// A failed run reports no versions; the last observed ones stay.
	m.observeTools(core.ToolInfo{})
	if m.facts.Docker != "27.4.1" {
		t.Fatalf("a failed report erased the version: %+v", m.facts)
	}
}

// A remote endpoint never claims 로컬. Remote engines are refused before engine
// access, so the header simply omits the suffix rather than mislabeling it.
func TestLocalLabelOnlyForLocalEndpoints(t *testing.T) {
	for _, kind := range []string{"unix", "npipe", "fd"} {
		if got := localEnvironment(kind); got != "로컬" {
			t.Fatalf("%s = %q", kind, got)
		}
	}
	for _, kind := range []string{"", "tcp", "ssh", "https"} {
		if got := localEnvironment(kind); got != "" {
			t.Fatalf("%s = %q, want empty", kind, got)
		}
	}
}

// The header's project name starts from the requested folder, so the first
// frame already names the stack; no Docker call is needed to learn it.
func TestInitialFactsUseTheRequestedFolder(t *testing.T) {
	f := initialFacts(core.TargetRequest{Path: "/work/acme-platform"})
	if f.Project != "acme-platform" {
		t.Fatalf("project = %q", f.Project)
	}
	f = initialFacts(core.TargetRequest{Path: "/work/other", ProjectName: "explicit"})
	if f.Project != "explicit" {
		t.Fatalf("explicit project name lost: %q", f.Project)
	}
}

// A wizard preview that cannot be saved must not offer enter: the footer only
// names keys that work on the current screen.
func TestWizardFooterHidesEnterWhileThePreviewIsInvalid(t *testing.T) {
	m := &Model{phase: phaseWizardPreview, wizardPreviewErr: errors.New("invalid"), theme: *plainTheme()}
	items := m.footerItems()
	for _, item := range items {
		if item.Key == "enter" {
			t.Fatalf("invalid preview offered enter: %+v", items)
		}
	}
	m.wizardPreviewErr = nil
	found := false
	for _, item := range m.footerItems() {
		if item.Key == "enter" && item.Label == "저장" {
			found = true
		}
	}
	if !found {
		t.Fatal("a saveable preview does not offer enter")
	}
}
