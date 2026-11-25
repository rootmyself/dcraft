package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// statusTableHeader returns the frame row that carries the status table's
// column names. The summary badge row also contains "인스턴스", so a test that
// searched the whole frame would prove nothing about the table fold.
func statusTableHeader(t *testing.T, m *Model) string {
	t.Helper()
	for _, line := range viewLines(t, m) {
		if strings.Contains(line, "서비스") && strings.Contains(line, "상태") && !strings.Contains(line, "✓") {
			return line
		}
	}
	t.Fatalf("no table header in the frame:\n%s", m.View())
	return ""
}

// Compact is `<60 columns` *or* `<18 rows`. A short but wide terminal must fold
// the status table to its leading columns, exactly as a narrow one does: the
// full six-column table used to survive a height-driven compact screen.
func TestCompactByHeightFoldsTheStatusTable(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 16)
	if m.layoutMode() != visual.ModeCompact {
		t.Fatalf("120x16 mode = %s, want compact", m.layoutMode())
	}
	header := statusTableHeader(t, m)
	// The compact fold keeps the service and state columns and drops the rest.
	for _, gone := range []string{"인스턴스", "health", "이미지", "포트"} {
		if strings.Contains(header, gone) {
			t.Fatalf("height-compact table kept the %q column: %q", gone, header)
		}
	}
	if !strings.Contains(header, "서비스") || !strings.Contains(header, "상태") {
		t.Fatalf("height-compact table lost the leading columns: %q", header)
	}
	// Every row still fits the frame.
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 120 {
			t.Fatalf("row %d is %d cells: %q", i, visual.Width(line), line)
		}
	}
}

// A narrow-but-tall terminal keeps the compact fold too, so the two reasons for
// compact cannot disagree about the table.
func TestCompactByWidthAlsoFoldsTheStatusTable(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 40, 30)
	header := statusTableHeader(t, m)
	for _, gone := range []string{"인스턴스", "health", "이미지", "포트"} {
		if strings.Contains(header, gone) {
			t.Fatalf("width-compact table kept the %q column: %q", gone, header)
		}
	}
}

// A decorated screen keeps every column: the fold is compact-only.
func TestDecoratedScreenKeepsTheWholeTable(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 30)
	header := statusTableHeader(t, m)
	for _, want := range []string{"서비스", "상태", "인스턴스", "health", "이미지", "포트"} {
		if !strings.Contains(header, want) {
			t.Fatalf("decorated table lost the %q column: %q", want, header)
		}
	}
}

// Compact by height must not remove the navigation: the current position has to
// stay visible, and the arrow keys must not move an invisible cursor.
func TestCompactByHeightKeepsTheNavigationVisible(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 16)
	if m.layoutMode() != visual.ModeCompact {
		t.Fatalf("120x16 mode = %s, want compact", m.layoutMode())
	}
	view := m.View()
	// The one-row summary names the current selection.
	if !strings.Contains(view, "▸ 스택 상태") {
		t.Fatalf("the navigation summary is missing the current selection:\n%s", view)
	}
	if !strings.Contains(view, "메뉴") {
		t.Fatalf("the navigation summary is missing:\n%s", view)
	}
	if m.sidebarVisible() {
		t.Fatal("a compact screen grew a sidebar")
	}
}

// The arrow keys still move a visible position at compact-by-height, and the
// summary follows the move.
func TestCompactByHeightNavigationMovesAVisibleCursor(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 16)
	before := selectedTitle(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	after := selectedTitle(t, m)
	if before == after {
		t.Fatalf("↓ did not move the selection (%q)", before)
	}
	view := m.View()
	if !strings.Contains(view, "▸ "+after) {
		t.Fatalf("the frame does not show the new selection %q:\n%s", after, view)
	}
	// And the position is really the model's, not a stale row.
	if got := m.selectedFeature().Descriptor().Title; got != after {
		t.Fatalf("summary shows %q, model selection is %q", after, got)
	}
}

// selectedTitle is the Korean title the one-row summary names.
func selectedTitle(t *testing.T, m *Model) string {
	t.Helper()
	nav := m.nav()
	for _, entry := range nav.entries {
		if entry.index == m.selected {
			return entry.menu.Entry.Label
		}
	}
	t.Fatalf("no selected item in the navigation:\n%s", m.View())
	return ""
}
