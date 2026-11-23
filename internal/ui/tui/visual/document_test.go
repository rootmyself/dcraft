package visual

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func warningTable(rows int) *core.Table {
	t := &core.Table{
		EmptyText: "서비스가 없음. Compose 파일에 services 항목이 있는지 확인.",
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "상태", Fixed: 11},
			{Header: "인스턴스", Fixed: 8, Align: core.AlignRight},
			{Header: "health", Fixed: 8, DropPriority: 3},
			{Header: "이미지", Weight: 45, Min: 16, Max: 34, DropPriority: 2},
			{Header: "포트", Fixed: 13, DropPriority: 1},
		},
	}
	for i := 0; i < rows; i++ {
		name := "api"
		if i == 1 {
			name = "a-very-long-service-name-that-cannot-fit"
		}
		t.Rows = append(t.Rows, core.Row{
			Selectable: true,
			Cells: []core.Cell{
				{Text: name, Detail: name},
				{Text: "실행 중", Badge: core.BadgeRunning},
				{Text: "2"},
				{Text: "비정상", Badge: core.BadgeUnhealthy},
				{Text: "registry.example.com/team/service:2026-11-20", Detail: "registry.example.com/team/service:2026-11-20-full-digest"},
				{Text: "58080→80"},
			},
		})
	}
	return t
}

func allBlocksDocument() core.Document {
	return core.Document{
		Title: "스택 상태",
		Blocks: []core.Block{
			{Kind: core.BlockHeading, Text: "요약"},
			{Kind: core.BlockParagraph, Text: "현재 폴더 스택의 서비스 상태와 연결 정보 확인. 이 문장은 폭에 맞춰 줄바꿈되어야 ."},
			{Kind: core.BlockKeyValues, Pairs: []core.Pair{
				{Key: "마지막 조회", Value: "13:22:46"},
				{Key: "Compose 파일", Value: "/Users/npnl/Documents/work/an-extremely-long-project-folder-name/compose.yaml"},
			}},
			{Kind: core.BlockBadges, Badges: []core.Badge{
				{Kind: core.BadgeRunning, Text: "실행 중 13"},
				{Kind: core.BadgeUnhealthy, Text: "비정상 1"},
			}},
			{Kind: core.BlockTable, Table: warningTable(3)},
			{Kind: core.BlockDivider},
			{Kind: core.BlockCallout, Callout: core.CalloutInfo, Text: "다음 행동\n1. 로그 확인.\n2. 컨테이너가 없는 서비스는 스택 시작으로 생성됨."},
		},
	}
}

// Every block renders at every approved width and no line ever exceeds it.
func TestDocumentFitsEveryWidth(t *testing.T) {
	th := Plain()
	for _, width := range []int{40, 80, 120} {
		lines := RenderDocument(th, allBlocksDocument(), width)
		if len(lines) == 0 {
			t.Fatalf("width %d rendered nothing", width)
		}
		for i, line := range lines {
			if got := Width(line); got > width {
				t.Fatalf("width %d: row %d is %d cells: %q", width, i, got, line)
			}
			if strings.Contains(line, "\n") {
				t.Fatalf("width %d: row %d contains a newline", width, i)
			}
		}
		joined := strings.Join(lines, "\n")
		// Core labels survive every width; secondary columns are allowed to
		// drop below the compact breakpoint.
		for _, want := range []string{"요약", "서비스", "상태", "다음 행동", "로그 확인", "실행 중"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("width %d: missing %q:\n%s", width, want, joined)
			}
		}
	}
}

// The document title is a heading, and it is omitted when empty so the shell
// can own the screen title instead.
func TestDocumentTitleRendering(t *testing.T) {
	th := Plain()
	lines := RenderDocument(th, core.Document{Title: "스택 상태", Blocks: []core.Block{{Kind: core.BlockParagraph, Text: "본문"}}}, 80)
	if !strings.Contains(lines[0], "스택 상태") {
		t.Fatalf("title not first: %q", lines)
	}
	none := RenderDocument(th, core.Document{Blocks: []core.Block{{Kind: core.BlockParagraph, Text: "본문"}}}, 80)
	if strings.Contains(strings.Join(none, "\n"), "스택 상태") {
		t.Fatalf("unexpected title: %q", none)
	}
}

// A value wider than its column is cut with the explicit marker, and the full
// value stays readable as a detail entry.
func TestTableTruncatesWithEllipsisAndKeepsDetail(t *testing.T) {
	th := Plain()
	table := warningTable(2)
	layout := LayoutTable(th, *table, 80)
	if len(layout.Rows) != 2 {
		t.Fatalf("rows = %d", len(layout.Rows))
	}
	if !strings.Contains(layout.Header, "서비스") || !strings.Contains(layout.Header, "포트") {
		t.Fatalf("header = %q", layout.Header)
	}
	// The long image cannot fit an 80-column budget and must carry the marker.
	if !strings.Contains(strings.Join(layout.Rows, "\n"), th.Glyphs.Ell) {
		t.Fatalf("no explicit truncation marker:\n%s", strings.Join(layout.Rows, "\n"))
	}
	for i, row := range layout.Rows {
		if Width(row) > 80 {
			t.Fatalf("row %d is %d cells: %q", i, Width(row), row)
		}
	}

	details := DocumentDetails(core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: table}}}, 80)
	rendered := strings.Join(RenderDetails(th, details, 80), "\n")
	if !strings.Contains(rendered, "registry.example.com/team/service:2026-11-20-full-digest") {
		t.Fatalf("detail lost the full image value:\n%s", rendered)
	}
	// The detail names the row it belongs to.
	if !strings.Contains(rendered, "api") {
		t.Fatalf("detail does not identify its row:\n%s", rendered)
	}
	for _, line := range RenderDetails(th, details, 80) {
		if Width(line) > 80 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}

// The detail listing shows the full key-value text that the body had to cut.
// A path longer than the detail pane is continued over lines instead of being
// truncated again, so the reader can still read every character of it.
func TestDocumentDetailsShowFullKeyValue(t *testing.T) {
	th := Plain()
	entries := DocumentDetails(allBlocksDocument(), 40)
	lines := RenderDetails(th, entries, 40)
	var joined strings.Builder
	for _, line := range lines {
		joined.WriteString(strings.TrimSpace(line))
	}
	want := "/Users/npnl/Documents/work/an-extremely-long-project-folder-name/compose.yaml"
	if !strings.Contains(joined.String(), want) {
		t.Fatalf("detail lost the full path:\n%s", strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if Width(line) > 40 {
			t.Fatalf("detail line too wide: %q", line)
		}
	}
}

// Columns disappear in the approved order: 포트, then 이미지, then health.
// 서비스·상태·인스턴스 are never dropped while the table still fits.
func TestTableDropsColumnsInPriorityOrder(t *testing.T) {
	th := Plain()
	// A table whose minimums make each drop observable above the compact
	// breakpoint, so the priority order itself is what is being proved.
	table := core.Table{
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 20, Max: 30},
			{Header: "상태", Fixed: 12},
			{Header: "인스턴스", Fixed: 12, Align: core.AlignRight},
			{Header: "health", Fixed: 12, DropPriority: 3},
			{Header: "이미지", Weight: 45, Min: 24, Max: 34, DropPriority: 2},
			{Header: "포트", Fixed: 14, DropPriority: 1},
		},
		Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
			{Text: "api"}, {Text: "실행 중"}, {Text: "1"},
			{Text: "정상"}, {Text: "nginx:1.27"}, {Text: "8080→80"},
		}}},
	}
	cases := []struct {
		name  string
		width int
		want  []string
		gone  []string
	}{
		{"wide keeps every column", 140,
			[]string{"서비스", "상태", "인스턴스", "health", "이미지", "포트"}, nil},
		{"port goes first", 90,
			[]string{"서비스", "상태", "인스턴스", "health", "이미지"}, []string{"포트"}},
		{"image goes second", 80,
			[]string{"서비스", "상태", "인스턴스", "health"}, []string{"이미지", "포트"}},
		{"compact collapses to two columns", 48,
			[]string{"서비스", "상태"}, []string{"인스턴스", "health", "이미지", "포트"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := LayoutTable(th, table, tc.width)
			for _, want := range tc.want {
				if !strings.Contains(layout.Header, want) {
					t.Fatalf("width %d: header %q missing %q", tc.width, layout.Header, want)
				}
			}
			for _, gone := range tc.gone {
				if strings.Contains(layout.Header, gone) {
					t.Fatalf("width %d: header %q still has %q", tc.width, layout.Header, gone)
				}
			}
			if got := Width(layout.Header); got != tc.width {
				t.Fatalf("width %d: header is %d cells: %q", tc.width, got, layout.Header)
			}
			for i, row := range layout.Rows {
				if got := Width(row); got != tc.width {
					t.Fatalf("width %d: row %d is %d cells: %q", tc.width, i, got, row)
				}
			}
		})
	}
}

// The real status table keeps all six columns at 80 and drops only the port at
// 72, exactly as the approved width budget says.
func TestStatusTableColumnWidths(t *testing.T) {
	th := Plain()
	table := warningTable(1)
	all := LayoutTable(th, *table, 80)
	if got := len(all.Kept); got != 6 {
		t.Fatalf("80 columns = %d, want 6: %q", got, all.Header)
	}
	narrow := LayoutTable(th, *table, 72)
	if got := len(narrow.Kept); got != 5 {
		t.Fatalf("72 columns = %d, want 5: %q", got, narrow.Header)
	}
	if strings.Contains(narrow.Header, "포트") || !strings.Contains(narrow.Header, "이미지") {
		t.Fatalf("72 dropped the wrong column: %q", narrow.Header)
	}
}

// cellAt slices one rendered cell out of a row by display cells.
func cellAt(t *testing.T, row string, start, width int) string {
	t.Helper()
	_, rest := splitCells(row, start)
	cell, _ := splitCells(rest, width)
	return cell
}

// Alignment and badge glyphs: the instance count is right aligned and every
// state carries an icon plus the Korean label, so color is never the only
// channel.
func TestTableAlignsCountsAndPairsIconsWithLabels(t *testing.T) {
	th := Plain()
	layout := LayoutTable(th, *warningTable(1), 120)
	if layout.Header == "" {
		t.Fatal("no header")
	}
	row := layout.Rows[0]
	if !strings.Contains(row, "✓ 실행 중") {
		t.Fatalf("running badge missing its icon and label: %q", row)
	}
	if !strings.Contains(row, "▲ 비정상") {
		t.Fatalf("unhealthy badge missing its icon and label: %q", row)
	}
	start := 0
	instanceWidth := 0
	for i, col := range layout.Kept {
		if col.Header == "인스턴스" {
			instanceWidth = layout.Widths[i]
			break
		}
		start += layout.Widths[i] + 1
	}
	if instanceWidth == 0 {
		t.Fatal("instance column missing")
	}
	cell := cellAt(t, row, start, instanceWidth)
	if strings.TrimSpace(cell) != "2" || !strings.HasPrefix(cell, "      ") {
		t.Fatalf("instance count is not right aligned: %q", cell)
	}
}

// An empty table is an explained state, not a blank panel, and it does not draw
// a header row that suggests data exists.
func TestEmptyTableExplainsInsteadOfBlank(t *testing.T) {
	th := Plain()
	table := warningTable(0)
	layout := LayoutTable(th, *table, 80)
	if layout.Header != "" {
		t.Fatalf("empty table drew a header: %q", layout.Header)
	}
	text := strings.Join(layout.Lines(), "\n")
	if !strings.Contains(text, "서비스가 없음") {
		t.Fatalf("empty table not explained: %q", text)
	}
	if !layout.Empty() {
		t.Fatal("empty table did not report itself empty")
	}
	// A table with no EmptyText still must not render as a silent blank.
	bare := LayoutTable(th, core.Table{Columns: table.Columns}, 80)
	if strings.TrimSpace(strings.Join(bare.Lines(), "\n")) == "" {
		t.Fatal("empty table with no text rendered nothing")
	}
}

// Paragraphs wrap, headings and dividers stay one row, and every decorative
// glyph follows the injected theme.
func TestBlocksUseTheInjectedTheme(t *testing.T) {
	ascii := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: false})
	doc := core.Document{Blocks: []core.Block{
		{Kind: core.BlockParagraph, Text: strings.Repeat("아주 긴 한국어 문장 ", 8)},
		{Kind: core.BlockDivider},
	}}
	lines := RenderDocument(ascii, doc, 40)
	for i, line := range lines {
		if Width(line) > 40 {
			t.Fatalf("row %d too wide: %q", i, line)
		}
		if strings.Contains(line, "─") || strings.Contains(line, "…") {
			t.Fatalf("ASCII theme leaked a UTF-8 glyph: %q", line)
		}
	}
	// The divider is one full-width rule.
	if got := Width(lines[len(lines)-1]); got != 40 {
		t.Fatalf("divider is %d cells: %q", got, lines[len(lines)-1])
	}
}

// Callouts state their severity as a word, not only as color.
func TestCalloutStatesSeverity(t *testing.T) {
	th := Plain()
	for _, tc := range []struct {
		kind core.CalloutKind
		want string
	}{
		{core.CalloutInfo, "안내"},
		{core.CalloutNote, "참고"},
		{core.CalloutWarn, "주의"},
		{core.CalloutDanger, "위험"},
	} {
		lines := RenderDocument(th, core.Document{Blocks: []core.Block{
			{Kind: core.BlockCallout, Callout: tc.kind, Text: "본문"},
		}}, 40)
		if !strings.Contains(strings.Join(lines, "\n"), tc.want) {
			t.Fatalf("%s callout does not name its severity:\n%s", tc.kind, strings.Join(lines, "\n"))
		}
	}
}

// Unknown block kinds are skipped instead of crashing or printing Go values;
// the model is additive, so an old renderer must survive a new block.
func TestUnknownBlockIsSkipped(t *testing.T) {
	th := Plain()
	lines := RenderDocument(th, core.Document{Blocks: []core.Block{
		{Kind: core.BlockKind("future"), Text: "미래 블록"},
		{Kind: core.BlockParagraph, Text: "본문"},
	}}, 40)
	if strings.Contains(strings.Join(lines, "\n"), "미래 블록") {
		t.Fatalf("unknown block rendered:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "본문") {
		t.Fatal("known block after an unknown one was lost")
	}
}

// A row that is not selectable renders muted but keeps its text; muted styling
// may differ from a plain row without changing the visible layout.
func TestUnselectableRowKeepsItsText(t *testing.T) {
	th := Plain()
	table := warningTable(1)
	table.Rows[0].Selectable = false
	layout := LayoutTable(th, *table, 120)
	if !strings.Contains(layout.Rows[0], "api") {
		t.Fatalf("unselectable row lost its name: %q", layout.Rows[0])
	}
}

// A table narrower than its essential columns still states the service names
// instead of rendering an unusable sliver.
func TestNarrowTableKeepsNames(t *testing.T) {
	th := Plain()
	layout := LayoutTable(th, *warningTable(2), 24)
	joined := strings.Join(layout.Lines(), "\n")
	if !strings.Contains(joined, "api") {
		t.Fatalf("narrow table lost the service names:\n%s", joined)
	}
	for _, line := range layout.Lines() {
		if Width(line) > 24 {
			t.Fatalf("narrow row too wide: %q", line)
		}
	}
}
