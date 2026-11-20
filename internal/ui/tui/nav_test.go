package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// navModel builds a menu with one feature per section so the navigation is
// exercised without the real registry.
func navModel(t *testing.T) (*Model, *stubFeature) {
	t.Helper()
	primary := &stubFeature{desc: sectionDescriptor("status", "스택 상태", "상태를 확인합니다.", core.SectionInspect, true, false), body: "상태 본문"}
	logs := &stubFeature{desc: sectionDescriptor("logs", "로그 보기", "실시간 로그를 따라봅니다.", core.SectionInspect, false, false), body: "로그 본문"}
	up := &stubFeature{desc: sectionDescriptor("up", "스택 시작", "현재 폴더 스택을 시작합니다.", core.SectionOperate, false, false), body: "시작 본문"}
	restore := &stubFeature{desc: sectionDescriptor("restore", "데이터 복원", "백업에서 데이터를 되돌립니다.", core.SectionData, false, true), body: "복원 본문"}
	secrets := &stubFeature{desc: sectionDescriptor("secrets", "비밀값 연결", "비밀값 참조를 관리합니다.", core.SectionConfig, false, false), body: "비밀값 본문"}
	m := newTestModel(t, primary, logs, up, restore, secrets)
	m.theme = *plainTheme()
	initModel(t, m)
	return m, primary
}

func sectionDescriptor(name, title, summary string, section core.Section, primary, destructive bool) core.Descriptor {
	return core.Descriptor{
		Name: name, Title: title, Summary: summary, Usage: "dcraft " + name,
		Section: section, ReadOnly: !destructive, Primary: primary, Destructive: destructive,
	}
}

func pressKey(t *testing.T, m *Model, key tea.KeyMsg) tea.Cmd {
	t.Helper()
	nm, cmd := m.Update(key)
	*m = *(nm.(*Model))
	return cmd
}

func typeKeys(t *testing.T, m *Model, text string) {
	t.Helper()
	for _, r := range text {
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// At 100 columns the sidebar is persistent; one column below it is not. The
// boundary is width-only, exactly as the approved breakpoint says.
func TestSidebarAppearsAtOneHundredColumns(t *testing.T) {
	m, _ := navModel(t)

	resize(t, m, 99, 30)
	view := m.View()
	if strings.Contains(view, "╭ 기능") {
		t.Fatalf("99 columns drew the sidebar:\n%s", view)
	}
	if !strings.Contains(view, "tab 펼치기") {
		t.Fatalf("99 columns has no one-row navigation:\n%s", view)
	}

	resize(t, m, 100, 30)
	view = m.View()
	if !strings.Contains(view, "기능 · 5") {
		t.Fatalf("100 columns has no sidebar:\n%s", view)
	}
	if strings.Contains(view, "tab 펼치기") {
		t.Fatalf("sidebar layout kept the collapsed nav row:\n%s", view)
	}
}

// The sidebar groups features under the Korean section headings in the fixed
// order and shows a one-line summary for the focused entry.
func TestSidebarGroupsFeaturesWithOneLineSummaries(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	view := m.View()
	for _, want := range []string{"조회", "운영", "데이터", "설정", "스택 상태", "로그 보기", "스택 시작", "데이터 복원", "비밀값 연결"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, view)
		}
	}
	order := []string{"조회", "운영", "데이터", "설정"}
	last := -1
	for _, heading := range order {
		idx := strings.Index(view, heading)
		if idx <= last {
			t.Fatalf("section %q is out of order:\n%s", heading, view)
		}
		last = idx
	}
	if !strings.Contains(view, "상태를 확인합니다.") {
		t.Fatalf("focused summary missing:\n%s", view)
	}
}

// Tab expands the collapsed navigation in place: the frame keeps its row
// count, so the content does not lose a line.
func TestTabExpandsNavigationWithoutChangingRowCount(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 80, 24)
	before := len(viewLines(t, m))
	collapsed := m.View()
	if !strings.Contains(collapsed, "5개 기능") || !strings.Contains(collapsed, "tab 펼치기") {
		t.Fatalf("collapsed nav row missing:\n%s", collapsed)
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	expanded := m.View()
	if after := len(viewLines(t, m)); after != before {
		t.Fatalf("tab changed the row count: %d -> %d", before, after)
	}
	for _, want := range []string{"조회 2", "운영 1", "데이터 1", "설정 1", "tab 접기"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded nav missing %q:\n%s", want, expanded)
		}
	}
	if strings.Contains(expanded, "tab 펼치기") {
		t.Fatalf("expanded nav still offers expand:\n%s", expanded)
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if view := m.View(); !strings.Contains(view, "tab 펼치기") {
		t.Fatalf("shift+tab did not collapse:\n%s", view)
	}
}

// The filter narrows by title and by summary, works in both layouts, and esc
// restores the full menu.
func TestFilterNarrowsByTitleAndSummaryAndEscClears(t *testing.T) {
	for _, width := range []int{120, 80} {
		m, _ := navModel(t)
		resize(t, m, width, 30)

		pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		typeKeys(t, m, "복원")
		view := m.View()
		if !strings.Contains(view, "데이터 복원") {
			t.Fatalf("%d cols: title filter dropped the match:\n%s", width, view)
		}
		for _, absent := range []string{"로그 보기", "스택 시작", "비밀값 연결"} {
			if strings.Contains(view, absent) {
				t.Fatalf("%d cols: title filter kept %q:\n%s", width, absent, view)
			}
		}

		pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		view = m.View()
		for _, want := range []string{"로그 보기", "스택 시작", "비밀값 연결"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%d cols: esc did not clear the filter (%q missing):\n%s", width, want, view)
			}
		}

		// The summary is the second match channel: "연결" only appears in the
		// secrets summary, not in any title.
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		typeKeys(t, m, "참조")
		view = m.View()
		if !strings.Contains(view, "비밀값 연결") {
			t.Fatalf("%d cols: summary filter dropped the match:\n%s", width, view)
		}
		if strings.Contains(view, "스택 시작") {
			t.Fatalf("%d cols: summary filter kept a non-match:\n%s", width, view)
		}
	}
}

// Enter leaves the filter active but returns the keyboard to navigation.
func TestFilterKeepsTheNarrowedMenuAfterEnter(t *testing.T) {
	m, primary := navModel(t)
	resize(t, m, 80, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "로그")
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "로그 보기") || strings.Contains(m.View(), "스택 시작") {
		t.Fatalf("enter cleared the filter:\n%s", m.View())
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, nil))
	if m.active == primary {
		t.Fatal("navigation after enter still runs the primary")
	}
}

// The risk badge comes from the descriptor alone. A destructive stub with an
// arbitrary name gets the badge; a feature named like a real dangerous one
// without the field does not. This is what makes a name list impossible.
func TestRiskBadgeFollowsTheDestructiveFieldOnly(t *testing.T) {
	primary := &stubFeature{desc: sectionDescriptor("status", "스택 상태", "상태", core.SectionInspect, true, false), body: "상태"}
	arbitrary := &stubFeature{desc: sectionDescriptor("danger-thing", "임의 위험 작업", "데이터를 지웁니다.", core.SectionData, false, true), body: "위험 본문"}
	namesake := &stubFeature{desc: sectionDescriptor("restore", "데이터 복원", "복원합니다.", core.SectionData, false, false), body: "복원"}
	m := newTestModel(t, primary, arbitrary, namesake)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 30)

	view := m.View()
	dangerRows := 0
	for _, line := range viewLines(t, m) {
		if strings.Contains(line, "위험") {
			dangerRows++
		}
	}
	if dangerRows != 1 {
		t.Fatalf("risk rows = %d, want 1 (only the Destructive stub):\n%s", dangerRows, view)
	}
	for _, line := range viewLines(t, m) {
		if strings.Contains(line, "임의 위험 작업") && !strings.Contains(line, "위험") {
			t.Fatalf("destructive stub lacks the badge: %q", line)
		}
		if strings.Contains(line, "데이터 복원") && strings.Contains(line, "위험") {
			t.Fatalf("non-destructive namesake carries a badge: %q", line)
		}
	}
}

// Leaving for a result screen and coming back keeps the cursor where it was.
func TestSelectionSurvivesAResultRoundTrip(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "복원")
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))
	// A destructive stub is still a mutating feature, so the ordinary
	// confirmation stands between the menu and the run.
	if m.phase == phaseConfirm {
		cmd = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		feed(t, m, execute(t, cmd))
	}
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready", m.phase)
	}
	if got := m.active.Descriptor().Title; got != "데이터 복원" {
		t.Fatalf("active = %q, want 데이터 복원", got)
	}
	if got := m.features[m.selected].Descriptor().Title; got != "데이터 복원" {
		t.Fatalf("selection = %q, want 데이터 복원", got)
	}
	if !strings.Contains(m.View(), "복원 본문") {
		t.Fatalf("result not shown:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "▸ 데이터 복원") && !strings.Contains(m.View(), "> 데이터 복원") {
		t.Fatalf("selection is not marked after the round trip:\n%s", m.View())
	}
}

// The menu renders in section order even though the registry order differs.
// Only the menu region is inspected: the screen title above it legitimately
// repeats the active feature's title.
func TestMenuRowsFollowSectionOrderInTheBodyLayout(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 80, 30)
	lines := viewLines(t, m)
	menuStart := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == msgMenu {
			menuStart = i + 1
			break
		}
	}
	if menuStart < 0 {
		t.Fatalf("no menu section in the body:\n%s", strings.Join(lines, "\n"))
	}
	menu := strings.Join(lines[menuStart:], "\n")
	order := []string{"조회", "스택 상태", "로그 보기", "운영", "스택 시작", "데이터", "데이터 복원", "설정", "비밀값 연결"}
	last := -1
	for _, want := range order {
		idx := strings.Index(menu, want)
		if idx < 0 {
			t.Fatalf("menu missing %q:\n%s", want, menu)
		}
		if idx <= last {
			t.Fatalf("%q is out of section order:\n%s", want, menu)
		}
		last = idx
	}
}

// A sidebar that cannot show every row keeps the cursor visible: the window
// follows the selection instead of truncating the last reachable entries.
func TestSidebarScrollsToKeepTheSelectionVisible(t *testing.T) {
	var features []core.Feature
	titles := []string{
		"스택 상태", "로그 보기", "서비스 접속",
		"스택 시작", "스택 중지", "스택 재시작", "스택 제거", "갱신",
		"데이터 백업", "데이터 복원", "이전 버전 복원",
		"최초 구성", "실행환경 별칭", "비밀값 연결", "호스트 스크립트",
		"Compose 설치", "Git 호스트 스크립트 생성", "S3 호스트 스크립트 생성",
	}
	sections := []core.Section{
		core.SectionInspect, core.SectionInspect, core.SectionInspect,
		core.SectionOperate, core.SectionOperate, core.SectionOperate, core.SectionOperate, core.SectionOperate,
		core.SectionData, core.SectionData, core.SectionData,
		core.SectionConfig, core.SectionConfig, core.SectionConfig, core.SectionConfig,
		core.SectionConfig, core.SectionConfig, core.SectionConfig,
	}
	for i, title := range titles {
		d := sectionDescriptor("feature-"+titles[i][:0]+string(rune('a'+i)), title, title+" 요약", sections[i], i == 0, false)
		features = append(features, &stubFeature{desc: d, body: title + " 본문"})
	}
	m := newTestModel(t, features...)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 18)
	for i := 0; i < len(features)-1; i++ {
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	view := m.View()
	// The row is visible; its title is truncated to the pane width with the
	// explicit marker, which is the width contract, so the prefix plus the
	// cursor marker is the observable fact.
	selected := ""
	for _, line := range viewLines(t, m) {
		if strings.Contains(line, "▸ S3") {
			selected = line
		}
	}
	if selected == "" {
		t.Fatalf("selected row scrolled out of the sidebar:\n%s", view)
	}
	if got := len(viewLines(t, m)); got != 18 {
		t.Fatalf("rows = %d, want 18", got)
	}
}

// The filtered view never widens a row: every line still fits the terminal.
func TestFilteredFrameFitsTheTerminal(t *testing.T) {
	m, _ := navModel(t)
	for _, size := range [][2]int{{40, 18}, {80, 24}, {120, 36}} {
		resize(t, m, size[0], size[1])
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		typeKeys(t, m, "스택")
		for i, line := range viewLines(t, m) {
			if got := visual.Width(line); got > size[0] {
				t.Fatalf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, line)
			}
		}
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	}
}

// While the filter has the keyboard the footer names only its two keys; q and
// r are characters there, not commands.
func TestFilterFooterNamesOnlyFilterKeys(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	view := m.View()
	if !strings.Contains(view, "[esc] 해제") || !strings.Contains(view, "[enter] 확정") {
		t.Fatalf("filter footer missing its keys:\n%s", view)
	}
	if strings.Contains(view, "[q] 종료") {
		t.Fatalf("filter footer names a key that types a character:\n%s", view)
	}
}

// Typing q while filtering narrows the menu instead of quitting.
func TestFilterKeysDoNotTriggerMenuCommands(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if isQuit(t, cmd) {
		t.Fatal("q quit while filtering")
	}
	if m.filter != "q" {
		t.Fatalf("filter = %q, want q", m.filter)
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filter != "" {
		t.Fatalf("backspace left %q", m.filter)
	}
}

// A destructive entry in the one-row navigation keeps the Korean risk label,
// because the row is the only place that entry is drawn.
func TestMediumNavRowShowsTheRiskLabel(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 80, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "복원")
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	row := viewLines(t, m)[1]
	for _, want := range []string{"데이터 복원", "위험"} {
		if !strings.Contains(row, want) {
			t.Fatalf("nav row missing %q: %q", want, row)
		}
	}
}

// The single-column menu has the width to explain every entry, so the summary
// travels with the row instead of waiting behind the cursor.
func TestBodyMenuShowsTheOneLineSummary(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 80, 40)
	view := m.View()
	// A non-selected row's summary is visible too.
	for _, want := range []string{"실시간 로그를 따라봅니다.", "현재 폴더 스택을 시작합니다.", "비밀값 참조를 관리합니다."} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu missing the summary %q:\n%s", want, view)
		}
	}
}

// The sidebar is persistent: it stays while a result, a confirmation or a
// failure is on screen, so the current position never disappears.
func TestSidebarPersistsAcrossScreens(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	if !strings.Contains(m.View(), "╭ 기능") {
		t.Fatalf("no sidebar on the menu")
	}
	cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))
	if !strings.Contains(m.View(), "╭ 기능") {
		t.Fatalf("sidebar disappeared after a run:\n%s", m.View())
	}
	nm, _ := m.Update(resultMsg{res: core.Result{
		SchemaVersion: core.SchemaVersion, Action: "status", Outcome: core.OutcomeFailure,
		Diagnostics: []core.Diagnostic{{Category: core.CategoryInternal, Severity: core.SeverityError, Message: "실패했습니다.", NextActions: []string{"다시 실행하세요."}}},
	}})
	m = nm.(*Model)
	if !strings.Contains(m.View(), "╭ 기능") {
		t.Fatalf("sidebar disappeared on failure:\n%s", m.View())
	}
}

// While typing, the filter field grows a caret so the user can see the text
// going in; after enter the same text stays visible without it.
func TestFilterFieldShowsACaretOnlyWhileEditing(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "로")
	if !strings.Contains(m.View(), "/로▏") {
		t.Fatalf("editing filter has no caret:\n%s", m.View())
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	if strings.Contains(view, "▏") {
		t.Fatalf("committed filter kept the caret:\n%s", view)
	}
	if !strings.Contains(view, "/로") {
		t.Fatalf("committed filter text disappeared:\n%s", view)
	}
}

// The TUI must never hold a feature-name list or branch on a feature name:
// the descriptor carries that knowledge. Bubble Tea's key names ("up",
// "down") are the same strings as two feature names, so those two are checked
// only in the shapes a name table takes; the other sixteen are forbidden as
// quoted literals anywhere.
func TestTUISourceHasNoFeatureNameTablesOrNameBranches(t *testing.T) {
	ambiguous := []string{"up", "down"}
	unambiguous := []string{
		"status", "logs", "exec", "stop", "restart", "update",
		"backup", "restore", "rollback", "init", "environments", "secrets",
		"hooks", "install-compose", "generate-git-hook", "generate-s3-hook",
	}
	all := append(append([]string{}, ambiguous...), unambiguous...)
	for _, dir := range []string{".", "visual"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(body), "\n") {
				for _, name := range unambiguous {
					if strings.Contains(line, `"`+name+`"`) {
						t.Fatalf("%s:%d contains the feature name literal %q; derive it from the descriptor", path, i+1, name)
					}
				}
				found := 0
				for _, name := range all {
					if strings.Contains(line, `"`+name+`"`) {
						found++
					}
				}
				if found >= 2 {
					t.Fatalf("%s:%d holds %d feature names on one line: %s", path, i+1, found, line)
				}
				if strings.Contains(line, ".Name ==") || strings.Contains(line, ".Name !=") {
					for _, name := range all {
						if strings.Contains(line, `"`+name+`"`) {
							t.Fatalf("%s:%d branches on the literal feature name %q: %s", path, i+1, name, line)
						}
					}
				}
			}
		}
	}
}

// The danger set is descriptor data, not a name list: a feature with an
// arbitrary name and Destructive=true gets the badge, and a feature named like
// a real dangerous one without the field does not.
// The real registry renders exactly three risk rows, and each is one of the
// three irreversible actions. This is the end-to-end statement of the badge
// contract through the TUI, not the registry.
func TestRealRegistryRendersExactlyThreeRiskRows(t *testing.T) {
	reg := core.NewRegistry()
	for _, d := range []struct {
		name, title, summary string
		section              core.Section
		destructive          bool
		primary              bool
	}{
		{name: "status", title: "스택 상태", summary: "상태", section: core.SectionInspect, primary: true},
		{name: "logs", title: "로그 보기", summary: "로그", section: core.SectionInspect},
		{name: "up", title: "스택 시작", summary: "시작", section: core.SectionOperate},
		{name: "down", title: "스택 제거", summary: "제거", section: core.SectionOperate, destructive: true},
		{name: "backup", title: "데이터 백업", summary: "백업", section: core.SectionData},
		{name: "restore", title: "데이터 복원", summary: "복원", section: core.SectionData, destructive: true},
		{name: "rollback", title: "이전 버전 복원", summary: "되돌리기", section: core.SectionData, destructive: true},
		{name: "secrets", title: "비밀값 연결", summary: "비밀값", section: core.SectionConfig},
	} {
		d := d
		desc := core.Descriptor{
			Name: d.name, Title: d.title, Summary: d.summary, Usage: "dcraft " + d.name,
			Section: d.section, ReadOnly: !d.destructive && d.name != "up", Primary: d.primary,
			Destructive: d.destructive,
		}
		if err := reg.Register(&stubFeature{desc: desc}); err != nil {
			t.Fatalf("Register %s: %v", d.name, err)
		}
	}
	m := NewModel(nil, Options{Registry: reg})
	m.theme = *plainTheme()
	resize(t, m, 120, 30)
	rows := 0
	for _, line := range viewLines(t, m) {
		if strings.Contains(line, "위험") {
			rows++
		}
	}
	if rows != 3 {
		t.Fatalf("risk rows = %d, want 3:\n%s", rows, m.View())
	}
}

// Tab expansion is a same-row edit at every medium width, including the
// narrowest one, and the visible body rows below the nav row do not change.
func TestTabExpansionKeepsContentRowsAtEveryMediumWidth(t *testing.T) {
	for _, width := range []int{60, 72, 80, 99} {
		m, _ := navModel(t)
		resize(t, m, width, 24)
		before := viewLines(t, m)
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
		after := viewLines(t, m)
		if len(before) != len(after) {
			t.Fatalf("%d cols: row count %d -> %d", width, len(before), len(after))
		}
		if before[1] == after[1] {
			t.Fatalf("%d cols: nav row did not change on tab: %q", width, before[1])
		}
		// The rows under the nav row are the content; expansion must not
		// consume one of them.
		if strings.Join(before[2:], "\n") != strings.Join(after[2:], "\n") {
			t.Fatalf("%d cols: expansion changed the content rows", width)
		}
	}
}

// The 100-column switch changes the navigation form, not the frame structure:
// the header keeps the same facts, the footer row is identical, and the
// content pane shrinks by exactly the sidebar width plus the one-cell gap.
func TestBreakpointSwitchKeepsFrameGeometry(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 99, 30)
	narrow := viewLines(t, m)
	resize(t, m, 100, 30)
	wide := viewLines(t, m)

	if len(narrow) != len(wide) {
		t.Fatalf("rows %d -> %d", len(narrow), len(wide))
	}
	if strings.Join(strings.Fields(narrow[0]), " ") != strings.Join(strings.Fields(wide[0]), " ") {
		t.Fatalf("header facts changed across the boundary:\n%q\n%q", narrow[0], wide[0])
	}
	if strings.TrimRight(narrow[len(narrow)-1], " ") != strings.TrimRight(wide[len(wide)-1], " ") {
		t.Fatalf("footer changed across the boundary:\n%q\n%q", narrow[len(narrow)-1], wide[len(wide)-1])
	}
	wantContent := 100 - visual.SidebarWidth(100) - 1
	if got := m.bodyWidth(); got != wantContent {
		t.Fatalf("body width = %d, want %d", got, wantContent)
	}
	for i, line := range wide {
		if got := visual.Width(line); got != 100 {
			t.Fatalf("row %d width = %d: %q", i, got, line)
		}
	}
}

// The filter works in the compact layout too: the menu is in the body there,
// so a narrowed list must actually narrow and esc must restore it.
func TestFilterWorksInCompactLayout(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 48, 20)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "시작")
	view := m.View()
	if !strings.Contains(view, "스택 시작") {
		t.Fatalf("compact filter dropped the match:\n%s", view)
	}
	for _, absent := range []string{"로그 보기", "데이터 복원", "비밀값 연결"} {
		if strings.Contains(view, absent) {
			t.Fatalf("compact filter kept %q:\n%s", absent, view)
		}
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	view = m.View()
	for _, want := range []string{"로그 보기", "데이터 복원", "비밀값 연결"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact esc did not restore %q:\n%s", want, view)
		}
	}
}

// A one-feature registry has no menu to navigate, so it gets neither a sidebar
// nor a nav row; the body keeps the full width instead of reserving a pane for
// nothing.
func TestSingleFeatureKeepsTheFullWidth(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	m := newTestModel(t, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 30)
	view := m.View()
	if strings.Contains(view, "╭ 기능") {
		t.Fatalf("single-feature registry drew a sidebar:\n%s", view)
	}
	if strings.Contains(view, "tab 펼치기") {
		t.Fatalf("single-feature registry drew a nav row:\n%s", view)
	}
	if got := m.bodyWidth(); got != 120 {
		t.Fatalf("body width = %d, want 120", got)
	}
}

// A read-only operation in flight does not offer the menu keys: they do not
// work until the result is ready. The row states only what is true.
func TestReadOnlyLoadingFooterNamesOnlyWhatWorks(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	cmd := m.start(m.primary)
	if cmd == nil {
		t.Fatal("start returned no command")
	}
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want loading", m.phase)
	}
	view := m.View()
	if strings.Contains(view, "[enter] 실행") || strings.Contains(view, "[r] 새로고침") {
		t.Fatalf("loading footer promises menu keys:\n%s", view)
	}
	if !strings.Contains(view, "[q] 종료") {
		t.Fatalf("loading footer lost quit:\n%s", view)
	}
}

// A terminal resize mid-session reflows the navigation without losing the
// cursor: crossing the breakpoint in both directions keeps the same feature.
func TestResizeAcrossTheBreakpointKeepsTheCursor(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 30)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	typeKeys(t, m, "복원")
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	for _, size := range [][2]int{{80, 24}, {48, 20}, {120, 30}, {99, 30}, {100, 30}} {
		resize(t, m, size[0], size[1])
		if got := m.selectedFeature().Descriptor().Title; got != "데이터 복원" {
			t.Fatalf("%dx%d: cursor moved to %q", size[0], size[1], got)
		}
	}
}

// A hidden content column must not delete prose: a wizard prompt and its
// preview wrap at the content width instead of being truncated by the frame.
func TestWizardProseWrapsInTheWideLayout(t *testing.T) {
	long := strings.Repeat("아주 긴 안내 문장입니다. ", 8)
	wizard := &stubWizard{stubFeature: &stubFeature{desc: core.Descriptor{
		Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
		Section: core.SectionConfig, Wizard: true,
	}}, steps: core.WizardSpec{Title: "설정", Steps: []core.WizardStep{{
		Name: "ref", Title: "원본 참조 입력", Prompt: long, AllowCustom: true,
	}}}, preview: long}
	m := newWizardModel(t, wizard)
	m.theme = *plainTheme()
	resize(t, m, 120, 36)
	openWizard(t, m)
	view := m.View()
	if !strings.Contains(view, strings.TrimSpace(long)) && strings.Count(view, "아주 긴 안내 문장입니다.") < 4 {
		t.Fatalf("prompt did not wrap; it was cut:\n%s", view)
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 120 {
			t.Fatalf("row %d too wide: %q", i, line)
		}
	}
}
