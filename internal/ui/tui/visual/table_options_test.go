package visual

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// sevenColumnTable is the service-selection shape: the approved service columns
// with a leading selection column.
func sevenColumnTable() core.Table {
	return core.Table{
		Columns: []core.Column{
			{Header: "선택", Fixed: 4},
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "상태", Fixed: 11},
			{Header: "인스턴스", Fixed: 8, Align: core.AlignRight},
			{Header: "health", Fixed: 8, DropPriority: 3},
			{Header: "이미지", Weight: 45, Min: 16, Max: 34, DropPriority: 2},
			{Header: "포트", Fixed: 13, DropPriority: 1},
		},
		Rows: []core.Row{{
			Selectable: true,
			Cells: []core.Cell{
				{Text: "▸[x]"}, {Text: "api"}, {Text: "실행 중"}, {Text: "1"},
				{Text: "정상"}, {Text: "nginx:1.27"}, {Text: "8080→80"},
			},
		}},
	}
}

// The compact fold keeps the leading columns the caller names. The service
// selection screen needs 선택·서비스·상태, not the status table's two.
func TestLayoutTableOptionsKeepsRequestedCompactColumns(t *testing.T) {
	th := Plain()
	layout := LayoutTableOptions(th, sevenColumnTable(), 48, TableOptions{CompactColumns: 3})
	for _, want := range []string{"선택", "서비스", "상태"} {
		if !strings.Contains(layout.Header, want) {
			t.Fatalf("compact header %q missing %q", layout.Header, want)
		}
	}
	for _, gone := range []string{"인스턴스", "health", "이미지", "포트"} {
		if strings.Contains(layout.Header, gone) {
			t.Fatalf("compact header %q still has %q", layout.Header, gone)
		}
	}
	for i, row := range append([]string{layout.Header}, layout.Rows...) {
		if got := Width(row); got != 48 {
			t.Fatalf("row %d is %d cells, want 48: %q", i, got, row)
		}
	}
}

// Without an explicit count the default stays the status table's two columns,
// so the existing screen keeps its approved shape.
func TestLayoutTableOptionsDefaultsToTwoCompactColumns(t *testing.T) {
	th := Plain()
	layout := LayoutTableOptions(th, sevenColumnTable(), 48, TableOptions{})
	if !strings.Contains(layout.Header, "선택") || !strings.Contains(layout.Header, "서비스") {
		t.Fatalf("default compact dropped a leading column: %q", layout.Header)
	}
	if strings.Contains(layout.Header, "상태") {
		t.Fatalf("default compact kept three columns: %q", layout.Header)
	}
	// The zero-value options entry point must behave exactly like LayoutTable.
	if got, want := layout.Header, LayoutTable(th, sevenColumnTable(), 48).Header; got != want {
		t.Fatalf("LayoutTable diverged from zero options:\n%q\n%q", want, got)
	}
}
