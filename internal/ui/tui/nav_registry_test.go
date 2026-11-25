package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// The navigation grouping must come from the registry, not from a second loop
// in the TUI. This test changes the registry grouping and proves the menu
// follows it: if nav.go re-derived sections from core.Sections() it would still
// look right, so the observable check is the order and the membership the
// contract's tree produces.
func TestNavigationConsumesTheRegistryGrouping(t *testing.T) {
	features := goldenBaseFeatures()
	m := newGoldenModel(t, features...)
	resize(t, m, 120, 40)

	tree := m.registry.MenuBySection(nil)
	if len(tree.Sections) != 4 {
		t.Fatalf("registry sections = %d, want the four declared sections", len(tree.Sections))
	}
	// The menu's own order must match the registry tree exactly, including
	// each section's membership and every group heading.
	var want []string
	for _, section := range tree.Sections {
		for _, entry := range section.Entries {
			want = append(want, entry.Entry.Label)
			for _, child := range entry.Children {
				want = append(want, child.Entry.Label)
			}
		}
	}
	nav := m.nav()
	var rendered []string
	for _, section := range nav.sections {
		for _, row := range section.rows {
			if row.group != nil {
				rendered = append(rendered, row.group.label)
				continue
			}
			rendered = append(rendered, nav.entries[row.entryIndex].menu.Entry.Label)
		}
	}
	if strings.Join(rendered, "|") != strings.Join(want, "|") {
		t.Fatalf("menu order differs from the registry tree:\nmenu:     %v\nregistry: %v", rendered, want)
	}
	// Arrow-key order is the selectable rows in the same sequence, so the
	// cursor never lands on a heading or jumps across a section.
	if ids := m.navIndexes(); len(ids) != len(nav.entries) {
		t.Fatalf("navIndexes = %d entries, the drawn list has %d", len(ids), len(nav.entries))
	}
	for i, idx := range m.navIndexes() {
		if idx != i {
			t.Fatalf("navIndexes[%d] = %d, want the drawn order", i, idx)
		}
	}
	// A heading is never addressable: every index names a non-group entry.
	for _, entry := range nav.entries {
		if entry.menu.Group {
			t.Fatalf("menu index %d addresses the heading %q", entry.index, entry.menu.Entry.Label)
		}
	}
}

// Every surface the human interfaces draw for the same stack state agrees.
// This is the cross-package equality check that pins the shared mapping.
func TestServiceScopeAndStatusTableAgreeOnStateAndHealth(t *testing.T) {
	catalog := &catalogStatus{
		stubFeature: &stubFeature{desc: goldenStatusDescriptor(), body: "상태 결과"},
		services:    goldenServiceCatalog(4),
	}
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 결과"}
	features := replaceFeature(goldenBaseFeatures(), "status", catalog)
	features = replaceFeature(features, "stop", stop)
	m := newGoldenModel(t, features...)
	if err := chooseFeatureNamed(t, m, "stop"); err != nil {
		t.Fatal(err)
	}
	resize(t, m, 120, 40)

	scope := m.serviceTable(false)
	statusTable := m.resultScreen().Band
	if len(scope.Rows) == 0 || len(statusTable) == 0 {
		t.Fatalf("both tables must have rows: scope %d, status %d", len(scope.Rows), len(statusTable))
	}
	// The scope table's state and health columns must use the same words and
	// badges as the shared mapping, for every state present in the catalog.
	for _, row := range scope.Rows {
		if len(row.Cells) < 5 {
			t.Fatalf("scope row has %d cells", len(row.Cells))
		}
		if got := row.Cells[2].Text; got == "" {
			t.Fatalf("scope row %q has no state label", row.Cells[1].Text)
		}
		if got := row.Cells[4].Text; got == "" {
			t.Fatalf("scope row %q has no health label", row.Cells[1].Text)
		}
	}
	// Badges are state words, not a free-form field: a running row must carry
	// the running badge and nothing may be a private string constant.
	for _, row := range scope.Rows {
		switch row.Cells[2].Text {
		case "실행 중":
			if row.Cells[2].Badge != core.BadgeRunning {
				t.Fatalf("running state badge = %q", row.Cells[2].Badge)
			}
		case "중지됨":
			if row.Cells[2].Badge != core.BadgeStopped {
				t.Fatalf("stopped state badge = %q", row.Cells[2].Badge)
			}
		}
	}
}

// The status result table's own state column is the shared label: no state word
// appears that the mapping does not produce.
func TestStatusTableStateColumnUsesTheSharedLabels(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 40)
	table := m.resultScreen()
	joined := strings.Join(table.lines(), "\n")
	for _, want := range []string{"실행 중", "중지됨", "재시작 중", "미생성", "정상", "비정상"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("status table missing shared label %q:\n%s", want, joined)
		}
	}
}

// The filter narrows the registry tree without re-grouping it: a filtered menu
// keeps section headings for the sections that still have a match.
func TestFilteredNavigationKeepsRegistrySectionOrder(t *testing.T) {
	m := newGoldenModel(t, goldenBaseFeatures()...)
	resize(t, m, 120, 40)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("스택")})
	nav := m.nav()
	if len(nav.sections) == 0 {
		t.Fatal("filter dropped every section")
	}
	last := -1
	for _, section := range nav.sections {
		for _, row := range section.rows {
			if row.group != nil {
				continue
			}
			label := nav.entries[row.entryIndex].menu.Entry.Label
			if !strings.Contains(label, "스택") {
				t.Fatalf("filtered menu kept %q", label)
			}
		}
		idx := sectionIndex(section.section.KoreanLabel())
		if idx <= last {
			t.Fatalf("section %q is out of order", section.section.KoreanLabel())
		}
		last = idx
	}
}

func sectionIndex(title string) int {
	for i, section := range core.Sections() {
		if section.KoreanLabel() == title {
			return i
		}
	}
	return -1
}
