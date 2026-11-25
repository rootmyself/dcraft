package visual

import (
	"strings"
	"testing"
)

func sampleNav() Nav {
	return Nav{
		Title: "기능 · 4",
		Sections: []NavSection{
			{Title: "조회", Items: []NavItem{
				{Title: "스택 상태", Summary: "서비스 상태 확인.", Selected: true},
				{Title: "로그 보기", Summary: "로그를 따라봅니다."},
			}},
			{Title: "운영", Items: []NavItem{
				{Title: "스택 시작", Summary: "스택 시작."},
				{Title: "스택 제거", Summary: "스택 제거.", Danger: true},
			}},
		},
	}
}

func TestSidebarOccupiesExactlyTheGivenBox(t *testing.T) {
	th := Plain()
	lines := Sidebar(th, 26, 20, sampleNav())
	if len(lines) != 20 {
		t.Fatalf("rows = %d, want 20", len(lines))
	}
	for i, line := range lines {
		if got := Width(line); got != 26 {
			t.Fatalf("row %d width = %d: %q", i, got, line)
		}
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"기능 · 4", "조회", "스택 상태", "로그 보기", "운영", "스택 시작"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, joined)
		}
	}
}

// Sections render in the order they are given; the renderer never sorts them.
func TestSidebarKeepsSectionAndItemOrder(t *testing.T) {
	joined := strings.Join(Sidebar(Plain(), 30, 24, sampleNav()), "\n")
	if strings.Index(joined, "조회") > strings.Index(joined, "운영") {
		t.Fatalf("sections reordered:\n%s", joined)
	}
	if strings.Index(joined, "스택 상태") > strings.Index(joined, "로그 보기") {
		t.Fatalf("items reordered:\n%s", joined)
	}
}

// The danger badge is both a glyph and the Korean word, so stripping color
// cannot hide the risk. A safe entry never shows the word.
func TestSidebarDangerBadgeCarriesTheKoreanLabel(t *testing.T) {
	th := Plain()
	lines := Sidebar(th, 34, 24, sampleNav())
	joined := strings.Join(lines, "\n")

	dangerLine := ""
	for _, line := range lines {
		if strings.Contains(line, "스택 제거") {
			dangerLine = line
		}
	}
	if dangerLine == "" {
		t.Fatalf("danger entry missing:\n%s", joined)
	}
	if !strings.Contains(dangerLine, th.Glyphs.Danger) || !strings.Contains(dangerLine, "위험") {
		t.Fatalf("danger row lacks glyph+label: %q", dangerLine)
	}
	for _, line := range lines {
		if strings.Contains(line, "스택 시작") && strings.Contains(line, "위험") {
			t.Fatalf("safe entry carries a risk label: %q", line)
		}
	}
}

func TestSidebarMarksTheSelectedItem(t *testing.T) {
	th := Plain()
	lines := Sidebar(th, 30, 24, sampleNav())
	selected := ""
	for _, line := range lines {
		if strings.Contains(line, "스택 상태") {
			selected = line
		}
	}
	if !strings.Contains(selected, th.Glyphs.Sel) {
		t.Fatalf("selected row has no marker: %q", selected)
	}
}

// The focused entry explains itself in a fixed band, not by widening the row.
func TestSidebarShowsTheFocusedSummaryBand(t *testing.T) {
	nav := sampleNav()
	nav.Sections[0].Items[0].Selected = true
	joined := strings.Join(Sidebar(Plain(), 30, 26, nav), "\n")
	if !strings.Contains(joined, "서비스 상태 확인.") {
		t.Fatalf("summary band missing:\n%s", joined)
	}
}

// A short sidebar drops the description band before it drops an entry.
func TestSidebarDropsTheBandBeforeEntries(t *testing.T) {
	th := Plain()
	short := Sidebar(th, 30, 18, sampleNav())
	joined := strings.Join(short, "\n")
	if strings.Contains(joined, "서비스 상태 확인.") {
		t.Fatalf("short sidebar kept the band:\n%s", joined)
	}
	for _, want := range []string{"스택 상태", "스택 제거"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("short sidebar dropped %q:\n%s", want, joined)
		}
	}
}

// A filtered navigation with no matches states so instead of rendering an
// empty box or a stale item. The sentence may wrap; it is never truncated
// mid-word as if it were an identifier.
func TestSidebarStatesAnEmptyFilter(t *testing.T) {
	nav := Nav{Title: "기능 · 0", Empty: "조건에 맞는 기능이 없음."}
	joined := strings.Join(Sidebar(Plain(), 34, 20, nav), "\n")
	if !strings.Contains(joined, "조건에 맞는 기능이 없음.") {
		t.Fatalf("empty state missing:\n%s", joined)
	}
}

// The one-row navigation is the medium breakpoint form: collapsed it states
// the current item and the total; expanded it adds section counts, and both
// forms are exactly one row.
func TestNavRowCollapsedAndExpandedAreOneRow(t *testing.T) {
	nav := sampleNav()
	for _, tc := range []struct {
		name     string
		expanded bool
		want     []string
		absent   []string
	}{
		{name: "collapsed", expanded: false,
			want:   []string{"메뉴", "스택 상태", "4개 항목", "tab 펼치기"},
			absent: []string{"조회 2", "운영 2", "tab 접기"}},
		{name: "expanded", expanded: true,
			want:   []string{"메뉴", "스택 상태", "조회 2", "운영 2", "tab 접기"},
			absent: []string{"tab 펼치기"}},
	} {
		row := NavRow(Plain(), 80, nav, tc.expanded)
		if strings.Contains(row, "\n") {
			t.Fatalf("%s row wrapped: %q", tc.name, row)
		}
		if got := Width(row); got != 80 {
			t.Fatalf("%s width = %d: %q", tc.name, got, row)
		}
		for _, want := range tc.want {
			if !strings.Contains(row, want) {
				t.Fatalf("%s row missing %q: %q", tc.name, want, row)
			}
		}
		for _, absent := range tc.absent {
			if strings.Contains(row, absent) {
				t.Fatalf("%s row contains %q: %q", tc.name, absent, row)
			}
		}
	}
}

// The row must never exceed a narrow terminal, and the tab key is the part
// that survives trimming because it is the only affordance the row exists for.
func TestNavRowFitsItsWidth(t *testing.T) {
	nav := sampleNav()
	for _, width := range []int{60, 64, 72, 99} {
		row := NavRow(Plain(), width, nav, false)
		if got := Width(row); got != width {
			t.Fatalf("width %d: row is %d cells: %q", width, got, row)
		}
		if !strings.Contains(row, "tab 펼치기") {
			t.Fatalf("width %d: expand hint dropped: %q", width, row)
		}
	}
}

// A selected dangerous item repeats its label in the one-row form, since the
// row is the only place the entry is named.
func TestNavRowLabelsADangerousSelection(t *testing.T) {
	nav := sampleNav()
	nav.Sections[0].Items[0].Selected = false
	nav.Sections[1].Items[1].Selected = true
	row := NavRow(Plain(), 80, nav, false)
	if !strings.Contains(row, "스택 제거") || !strings.Contains(row, "위험") {
		t.Fatalf("dangerous selection unlabelled: %q", row)
	}
}

// Filtering changes the item set, never the geometry.
func TestSidebarAndNavRowShowTheActiveFilter(t *testing.T) {
	nav := sampleNav()
	nav.Filter = "로그"
	nav.Sections = []NavSection{{Title: "조회", Items: []NavItem{{Title: "로그 보기", Selected: true}}}}
	nav.Title = "기능 · 1"
	sidebar := strings.Join(Sidebar(Plain(), 30, 24, nav), "\n")
	if !strings.Contains(sidebar, "/로그") {
		t.Fatalf("sidebar does not state the filter:\n%s", sidebar)
	}
	if strings.Contains(sidebar, "스택 제거") {
		t.Fatalf("sidebar kept a filtered-out row:\n%s", sidebar)
	}
	row := NavRow(Plain(), 80, nav, false)
	if !strings.Contains(row, "/로그") {
		t.Fatalf("nav row does not state the filter: %q", row)
	}
}

// A non-UTF-8 locale replaces the filter caret and every section glyph with
// one-cell ASCII; Korean headings keep their characters.
func TestNavASCIISafeGlyphs(t *testing.T) {
	ascii := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: false})
	nav := sampleNav()
	nav.Filter = "로"
	nav.FilterActive = true
	joined := strings.Join(Sidebar(ascii, 30, 26, nav), "\n")
	for _, banned := range []string{"▸", "…", "▲", "─", "│", "▏"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("ASCII sidebar contains %q:\n%s", banned, joined)
		}
	}
	for _, want := range []string{"조회", "운영", "스택 제거"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ASCII sidebar lost Korean text %q:\n%s", want, joined)
		}
	}
}

// A heading is never the last visible row: it would name a section with
// nothing under it. The window is one row shorter instead.
func TestWindowNeverEndsOnAHeading(t *testing.T) {
	nav := Nav{}
	for i := 0; i < 6; i++ {
		nav.Sections = append(nav.Sections, NavSection{
			Title: "섹션" + string(rune('A'+i)),
			Items: []NavItem{{Title: "항목" + string(rune('a'+i))}},
		})
	}
	for height := 4; height <= 20; height++ {
		lines := Sidebar(Plain(), 30, height, nav)
		last := ""
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				last = strings.TrimSpace(line)
			}
		}
		if strings.HasPrefix(last, "섹션") {
			t.Fatalf("height %d ends on a heading: %q", height, last)
		}
	}
}

// A risk badge is never the part that gets cut, at any sidebar width: a pane
// too narrow for the title still keeps the warning.
func TestSidebarNeverCutsTheRiskBadge(t *testing.T) {
	nav := Nav{Title: "기능 · 3", Sections: []NavSection{{Title: "데이터", Items: []NavItem{
		{Title: "아주 긴 위험한 작업 이름", Danger: true},
		{Title: "안전한 작업"},
	}}}}
	for width := 12; width <= 40; width++ {
		joined := strings.Join(Sidebar(Plain(), width, 20, nav), "\n")
		if !strings.Contains(joined, "위험") {
			t.Fatalf("width %d dropped the risk label:\n%s", width, joined)
		}
	}
}

// The single-column menu also protects the badge when the summary column is
// competing for the row.
func TestBodyMenuNeverCutsTheRiskBadge(t *testing.T) {
	nav := Nav{Title: "기능 · 2", Sections: []NavSection{{Title: "데이터", Items: []NavItem{
		{Name: "restore", Title: "데이터 복원", Summary: strings.Repeat("아주 긴 설명 ", 6), Danger: true},
		{Name: "backup", Title: "데이터 백업", Summary: "백업."},
	}}}}
	for width := 20; width <= 90; width++ {
		joined := strings.Join(MenuLines(Plain(), width, nav, 10, true), "\n")
		if !strings.Contains(joined, "위험") {
			t.Fatalf("width %d dropped the risk label:\n%s", width, joined)
		}
		for _, line := range strings.Split(joined, "\n") {
			if Width(line) > width {
				t.Fatalf("width %d: row is %d cells: %q", width, Width(line), line)
			}
		}
	}
}

// The window keeps a heading attached to at least one of its items on both
// ends: no orphan heading at the top, none at the bottom.
func TestWindowHeadingsStayAttached(t *testing.T) {
	nav := Nav{}
	for i := 0; i < 8; i++ {
		var items []NavItem
		for j := 0; j < 3; j++ {
			items = append(items, NavItem{Title: "항목" + string(rune('a'+i)) + string(rune('a'+j))})
		}
		nav.Sections = append(nav.Sections, NavSection{Title: "섹션" + string(rune('A'+i)), Items: items})
	}
	for _, selected := range []string{"항목a0", "항목aa2", "항목dd1", "항목hh2"} {
		for _, section := range nav.Sections {
			for i := range section.Items {
				section.Items[i].Selected = section.Items[i].Title == selected
			}
		}
		for height := 10; height <= 26; height++ {
			rows := windowRows(navRows(nav), height-2)
			if len(rows) == 0 {
				t.Fatalf("%s: empty window", selected)
			}
			if rows[0].kind == navRowItem && len(rows) > 1 {
				// A window that opens on an item must not have hidden its
				// heading when there is room for it.
				for _, row := range rows {
					if row.kind == navRowHeading {
						break
					}
				}
			}
			if rows[len(rows)-1].kind == navRowHeading {
				t.Fatalf("%s height %d ends on a heading: %+v", selected, height, rows)
			}
		}
	}
}
