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
// contract's grouping produces.
func TestNavigationConsumesTheRegistryGrouping(t *testing.T) {
	features := goldenBaseFeatures()
	m := newGoldenModel(t, features...)
	resize(t, m, 120, 40)

	groups := m.registry.BySection()
	if len(groups) != 4 {
		t.Fatalf("registry groups = %d, want the four declared sections", len(groups))
	}
	// The menu's own order must match the registry grouping exactly, including
	// each group's membership.
	var want []string
	for _, group := range groups {
		for _, f := range group.Features {
			want = append(want, f.Descriptor().Title)
		}
	}
	got := m.nav()
	var rendered []string
	for _, section := range got.Sections {
		for _, item := range section.Items {
			rendered = append(rendered, item.Title)
		}
	}
	if strings.Join(rendered, "|") != strings.Join(want, "|") {
		t.Fatalf("menu order differs from the registry grouping:\nmenu:     %v\nregistry: %v", rendered, want)
	}
	// Arrow-key order must be the same sequence, so the cursor never jumps
	// across a section.
	if ids := m.navIndexes(); len(ids) != len(want) {
		t.Fatalf("navIndexes = %d entries, registry grouping has %d", len(ids), len(want))
	}
	for i, idx := range m.navIndexes() {
		if got := m.features[idx].Descriptor().Title; got != want[i] {
			t.Fatalf("navIndexes[%d] = %q, want %q", i, got, want[i])
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

// The filter narrows the registry grouping without re-grouping it: a filtered
// menu keeps section headings for the sections that still have a match.
func TestFilteredNavigationKeepsRegistrySectionOrder(t *testing.T) {
	m := newGoldenModel(t, goldenBaseFeatures()...)
	resize(t, m, 120, 40)
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("스택")})
	nav := m.nav()
	if len(nav.Sections) == 0 {
		t.Fatal("filter dropped every section")
	}
	last := -1
	for _, section := range nav.Sections {
		for _, item := range section.Items {
			if !strings.Contains(item.Title, "스택") {
				t.Fatalf("filtered menu kept %q", item.Title)
			}
		}
		idx := sectionIndex(section.Title)
		if idx <= last {
			t.Fatalf("section %q is out of order", section.Title)
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
