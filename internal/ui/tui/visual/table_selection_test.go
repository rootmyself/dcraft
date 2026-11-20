package visual

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func selectionRows() core.Table {
	return core.Table{
		Columns: []core.Column{
			{Header: "선택", Fixed: 4},
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
		},
		Rows: []core.Row{
			{Selectable: true, Cells: []core.Cell{{Text: "▸[ ]"}, {Text: "api"}}},
			{Selectable: true, Cells: []core.Cell{{Text: " [ ]"}, {Text: "web"}}},
		},
	}
}

// coloredTheme is a theme that may emit attributes, so the bold marker is
// observable.
func coloredTheme() Theme {
	th := DarkPalette()
	return Theme{Palette: th, Color: true, Glyphs: UTF8Glyphs()}
}

// The approved selection marker is the gutter glyph plus bold text, so the
// position survives even when the row's own colors are hard to see.
func TestLayoutTableOptionsBoldsTheCursorRow(t *testing.T) {
	th := coloredTheme()
	layout := LayoutTableOptions(th, selectionRows(), 40, TableOptions{
		Compact: true, CompactColumns: 2, CursorRow: 1,
	})
	if len(layout.Rows) != 2 {
		t.Fatalf("rows = %d", len(layout.Rows))
	}
	if !strings.Contains(layout.Rows[0], "\x1b[1m") {
		t.Fatalf("the cursor row is not bold: %q", layout.Rows[0])
	}
	if strings.Contains(layout.Rows[1], "\x1b[1m") {
		t.Fatalf("an unselected row is bold: %q", layout.Rows[1])
	}
	// Bold emits no cells: the width contract is unchanged.
	for i, row := range layout.Rows {
		if got := Width(row); got != 40 {
			t.Fatalf("row %d is %d cells: %q", i, got, row)
		}
	}
}

// A colorless theme promises no escape sequences at all. The cursor glyph (drawn
// by the screen itself) is then the whole selection channel, and a test that
// greps the plain frame must not trip over a stray SGR.
func TestLayoutTableOptionsPlainThemeEmitsNoEscapes(t *testing.T) {
	th := Plain()
	layout := LayoutTableOptions(th, selectionRows(), 40, TableOptions{
		Compact: true, CompactColumns: 2, CursorRow: 1,
	})
	for i, row := range layout.Rows {
		if strings.Contains(row, "\x1b") {
			t.Fatalf("row %d carries an escape sequence under a plain theme: %q", i, row)
		}
	}
}

// A table with no cursor (the status screen) never bolds a row: the highlight
// belongs to screens that own a selection, and zero means none.
func TestLayoutTableOptionsZeroCursorLeavesRowsPlain(t *testing.T) {
	th := coloredTheme()
	layout := LayoutTableOptions(th, selectionRows(), 40, TableOptions{Compact: true, CompactColumns: 2})
	if strings.Contains(strings.Join(layout.Rows, "\n"), "\x1b[1m") {
		t.Fatalf("rows were bolded without a cursor:\n%q", layout.Rows)
	}
}
