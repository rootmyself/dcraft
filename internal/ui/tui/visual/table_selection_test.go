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

// The approved selection marker is the gutter glyph plus bold text. Bold is an
// attribute, not a color, so the position survives a colorless frame.
func TestLayoutTableOptionsBoldsTheCursorRow(t *testing.T) {
	th := Plain()
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
	// Width is unchanged by the attribute: bold emits no cells.
	for i, row := range layout.Rows {
		if got := Width(row); got != 40 {
			t.Fatalf("row %d is %d cells: %q", i, got, row)
		}
	}
}

// A table with no cursor (the status screen) never bolds a row: the highlight
// belongs to screens that own a selection, and zero means none.
func TestLayoutTableOptionsZeroCursorLeavesRowsPlain(t *testing.T) {
	th := Plain()
	layout := LayoutTableOptions(th, selectionRows(), 40, TableOptions{Compact: true, CompactColumns: 2})
	if strings.Contains(strings.Join(layout.Rows, "\n"), "\x1b[1m") {
		t.Fatalf("rows were bolded without a cursor:\n%q", layout.Rows)
	}
}
