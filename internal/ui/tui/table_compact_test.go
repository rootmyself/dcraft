package tui

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The compact fold is a screen decision: LayoutTableOptions must honour the
// caller's Compact flag even on a wide table, and must not fold without it.
func TestTableCompactOptionIsHonouredAtAnyWidth(t *testing.T) {
	table := statusTable()
	wide := visual.LayoutTableOptions(visual.Plain(), table, 160, visual.TableOptions{Compact: true})
	if wide.Header == "" {
		t.Fatal("compact table has no header")
	}
	for _, gone := range []string{"인스턴스", "health", "이미지", "포트"} {
		if strings.Contains(wide.Header, gone) {
			t.Fatalf("compact fold at width 160 kept %q: %q", gone, wide.Header)
		}
	}
	decorated := visual.LayoutTableOptions(visual.Plain(), table, 160, visual.TableOptions{})
	for _, want := range []string{"서비스", "상태", "인스턴스", "health", "이미지", "포트"} {
		if !strings.Contains(decorated.Header, want) {
			t.Fatalf("uncompacted table at width 160 lost %q: %q", want, decorated.Header)
		}
	}
}

// The document layout must pass the caller's compact choice to the table fold.
// This is the regression A2 named: the flag reached the renderer but never the
// table, so a height-driven compact screen kept all six columns.
func TestDocumentLayoutPassesCompactToTheTable(t *testing.T) {
	tbl := statusTable()
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: &tbl}}}
	layout := visual.RenderDocumentLayoutOptions(visual.Plain(), doc, 160, visual.RenderOptions{Compact: true})
	header := layout.Fixed()
	if len(header) == 0 {
		t.Fatal("no fixed rows")
	}
	if strings.Contains(header[len(header)-1], "인스턴스") {
		t.Fatalf("compact layout kept a secondary column: %q", header[len(header)-1])
	}
	if !strings.Contains(header[len(header)-1], "서비스") || !strings.Contains(header[len(header)-1], "상태") {
		t.Fatalf("compact layout lost the leading columns: %q", header[len(header)-1])
	}
}

// statusTable is the real status table shape used by the fold tests.
func statusTable() core.Table {
	table := core.Table{Columns: []core.Column{
		{Header: "서비스", Weight: 30, Min: 12, Max: 24},
		{Header: "상태", Fixed: 11},
		{Header: "인스턴스", Fixed: 8, Align: core.AlignRight},
		{Header: "health", Fixed: 8, DropPriority: 3},
		{Header: "이미지", Weight: 45, Min: 16, Max: 34, DropPriority: 2},
		{Header: "포트", Fixed: 13, DropPriority: 1},
	}}
	table.Rows = append(table.Rows, core.Row{Selectable: true, Cells: []core.Cell{
		{Text: "api"}, {Text: "실행 중", Badge: core.BadgeRunning}, {Text: "1"},
		{Text: "정상"}, {Text: "nginx:1.27"}, {Text: "8080→80"},
	}})
	return table
}
