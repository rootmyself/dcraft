package visual

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// RenderDocument renders a neutral document for the TUI at the given width.
// Every returned line fits the width; a value that cannot fit is cut with the
// theme's explicit marker and its full text stays available as a detail entry
// (see DocumentDetails). The renderer reads only the injected theme: no color,
// glyph or width decision comes from package state.
func RenderDocument(th Theme, doc core.Document, width int) []string {
	return RenderDocumentLayout(th, doc, width).Lines
}

// DetailEntry is one value the body had to cut, with the row identity that
// tells the reader where it belongs. The value is always complete.
type DetailEntry struct {
	Label string
	Value string
}

// DocumentDetails returns the full value of everything the body had to cut. It
// is the approved "truncate with an ellipsis + detail view" pair: nothing is
// lost, only folded. It returns no entries when nothing was truncated.
func DocumentDetails(doc core.Document, width int) []DetailEntry {
	var out []DetailEntry
	for _, block := range doc.Blocks {
		switch block.Kind {
		case core.BlockKeyValues:
			keyWidth := pairKeyWidth(block.Pairs, width)
			for _, p := range block.Pairs {
				if Width(p.Value) > max(width-keyWidth-2, 0) {
					out = append(out, DetailEntry{Label: p.Key, Value: p.Value})
				}
			}
		case core.BlockTable:
			if block.Table == nil {
				continue
			}
			out = append(out, LayoutTable(Plain(), *block.Table, width).Truncations...)
		}
	}
	return out
}

// RenderDetails formats detail entries at the given width. A long value is
// broken across lines with a hanging indent: the detail screen exists to show
// the whole value, so it must not truncate again. Every line fits the width.
func RenderDetails(th Theme, entries []DetailEntry, width int) []string {
	var lines []string
	for _, entry := range entries {
		lines = append(lines, th.Bold(TokenMuted, Truncate(entry.Label, width, th.Ellipsis())))
		for _, line := range Wrap(entry.Value, max(width-2, 1)) {
			lines = append(lines, "  "+line)
		}
	}
	return trimTrailingBlank(lines)
}

// TableLayout is one rendered table: a fixed header row, the drawn data rows,
// and the record of what was cut so the detail view can restore it.
type TableLayout struct {
	Header string
	Rows   []string

	// Kept are the surviving columns in declared order.
	Kept []core.Column
	// Widths is the rendered width of each surviving column.
	Widths []int
	// Truncated is true when any cell was cut with the ellipsis.
	Truncated bool
	// Truncations lists every cut value for the detail view.
	Truncations []DetailEntry
}

// Lines is the header followed by the rows. An empty table returns only its
// explained empty state, never a header that implies rows exist.
func (l TableLayout) Lines() []string {
	if l.Header == "" {
		return trimTrailingBlank(l.Rows)
	}
	return trimTrailingBlank(append([]string{l.Header}, l.Rows...))
}

// Empty reports whether the table had no rows.
func (l TableLayout) Empty() bool { return l.Header == "" }

// compactTableWidth is the approved breakpoint below which the table collapses
// to the two columns the screen exists to compare: service and state.
const compactTableWidth = 60

// LayoutTable fits a table into width cells.
//
// Fixed columns keep their exact width. Flexible columns share what is left by
// weight, clamp to Min/Max, and the remainder is redistributed one cell at a
// time so the columns sum to the available width. When the minimums no longer
// fit, columns are dropped by DropPriority: port, then image, then health.
// Service, state and instances are never dropped by width because comparing
// them is the reason the table exists; below the compact breakpoint the table
// deliberately shows only service and state.
func LayoutTable(th Theme, table core.Table, width int) TableLayout {
	ell := th.Ellipsis()
	if len(table.Rows) == 0 {
		return TableLayout{Rows: []string{emptyTableText(th, table.EmptyText, width)}}
	}

	type placed struct {
		col  core.Column
		orig int
	}
	var chosen []placed
	if width < compactTableWidth {
		for i, col := range table.Columns {
			if i >= 2 {
				break
			}
			chosen = append(chosen, placed{col: col, orig: i})
		}
	} else {
		for _, i := range fitColumns(table.Columns, width) {
			chosen = append(chosen, placed{col: table.Columns[i], orig: i})
		}
	}
	if len(chosen) == 0 {
		return TableLayout{Rows: nameOnlyRows(th, table, width)}
	}

	cols := make([]core.Column, len(chosen))
	for i, p := range chosen {
		cols[i] = p.col
	}
	widths := columnWidths(cols, width)

	layout := TableLayout{Widths: widths, Kept: cols}
	header := make([]string, len(chosen))
	for i, p := range chosen {
		header[i] = alignCell(p.col.Align, p.col.Header, widths[i], ell)
	}
	layout.Header = Fit(strings.Join(header, " "), width, ell)

	for _, row := range table.Rows {
		cells := make([]string, len(chosen))
		lead := ""
		if len(row.Cells) > 0 {
			lead = row.Cells[0].Text
		}
		for i, p := range chosen {
			cell := core.Cell{}
			if p.orig < len(row.Cells) {
				cell = row.Cells[p.orig]
			}
			text := cell.Text
			if cell.Badge != BadgeNone {
				text = badgeText(th, cell.Badge, text)
			}
			// A badge that does not fit falls back through its shorter labels
			// before it is cut: `○ 컨테이너 없음` becomes `○ 미생성`, then
			// the icon alone. Silently leaving `○ 컨…` would name nothing.
			if Width(text) > widths[i] && cell.Badge != BadgeNone {
				for _, candidate := range badgeFallbacks(th, cell.Badge) {
					if Width(candidate) <= widths[i] {
						text = candidate
						break
					}
				}
			}
			if Width(text) > widths[i] {
				layout.Truncated = true
				value := cell.Detail
				if value == "" {
					value = cell.Text
				}
				label := p.col.Header
				if p.orig != 0 && lead != "" {
					label = lead + " · " + p.col.Header
				}
				layout.Truncations = append(layout.Truncations, DetailEntry{Label: label, Value: value})
			}
			cells[i] = alignCell(p.col.Align, text, widths[i], ell)
		}
		line := strings.Join(cells, " ")
		if !row.Selectable {
			line = th.Paint(TokenMuted, line)
		}
		layout.Rows = append(layout.Rows, Fit(line, width, ell))
	}
	return layout
}

// fitColumns drops columns that cannot meet their minimum, in the approved
// order, and returns the surviving original indices in declared order.
func fitColumns(columns []core.Column, width int) []int {
	keep := make([]bool, len(columns))
	for i := range keep {
		keep[i] = true
	}
	for {
		idx := indices(keep)
		if len(idx) == 0 {
			return nil
		}
		if columnsFit(columns, idx, width) {
			return idx
		}
		worst := -1
		for _, i := range idx {
			if columns[i].DropPriority <= 0 {
				continue
			}
			if worst < 0 || columns[i].DropPriority < columns[worst].DropPriority ||
				(columns[i].DropPriority == columns[worst].DropPriority && i > worst) {
				worst = i
			}
		}
		if worst < 0 {
			return nil
		}
		keep[worst] = false
	}
}

// columnsFit reports whether the surviving columns can be laid out at their
// minimums inside width.
func columnsFit(columns []core.Column, idx []int, width int) bool {
	need := len(idx) - 1
	for _, i := range idx {
		col := columns[i]
		if col.Weight > 0 || col.Fixed == 0 {
			need += max(col.Min, 1)
			continue
		}
		need += col.Fixed
	}
	return need <= width
}

func indices(keep []bool) []int {
	var out []int
	for i, ok := range keep {
		if ok {
			out = append(out, i)
		}
	}
	return out
}

// columnWidths distributes width across columns: fixed widths are exact,
// flexible columns split the remainder by weight, clamp to min/max, and the
// leftover is handed out one cell at a time so the sum is the available width.
func columnWidths(columns []core.Column, width int) []int {
	gaps := len(columns) - 1
	if gaps < 0 {
		gaps = 0
	}
	available := width - gaps
	if available < len(columns) {
		available = len(columns)
	}
	widths := make([]int, len(columns))
	used := 0
	weightTotal := 0
	for i, col := range columns {
		if col.Weight > 0 {
			weightTotal += col.Weight
			continue
		}
		widths[i] = col.Fixed
		if widths[i] == 0 {
			widths[i] = max(col.Min, 1)
		}
		used += widths[i]
	}
	if weightTotal == 0 {
		return widths
	}
	remaining := available - used
	if remaining < 0 {
		remaining = 0
	}
	flex := make([]int, 0, len(columns))
	for i, col := range columns {
		if col.Weight == 0 {
			continue
		}
		w := clampWidth(remaining*col.Weight/weightTotal, col)
		widths[i] = w
		used += w
		flex = append(flex, i)
	}
	// Redistribute one cell at a time so the row fills the width exactly.
	for used < available {
		moved := false
		for _, i := range flex {
			if used >= available {
				break
			}
			if columns[i].Max > 0 && widths[i] >= columns[i].Max {
				continue
			}
			widths[i]++
			used++
			moved = true
		}
		if !moved {
			break
		}
	}
	for used > available {
		moved := false
		for _, i := range flex {
			if used <= available {
				break
			}
			if widths[i] <= max(columns[i].Min, 1) {
				continue
			}
			widths[i]--
			used--
			moved = true
		}
		if !moved {
			break
		}
	}
	return widths
}

func clampWidth(w int, col core.Column) int {
	if w < col.Min {
		w = col.Min
	}
	if col.Min == 0 && w < 1 {
		w = 1
	}
	if col.Max > 0 && w > col.Max {
		w = col.Max
	}
	return w
}

// alignCell fits a cell into its column width and places it according to the
// column alignment. Fit pads on the right; a right-aligned column moves that
// padding to the left.
func alignCell(align core.Align, text string, width int, ell string) string {
	if width <= 0 {
		return ""
	}
	text = Truncate(text, width, ell)
	if align != core.AlignRight {
		return Pad(text, width)
	}
	return PadLeft(strings.TrimLeft(text, " "), width)
}

func emptyTableText(th Theme, text string, width int) string {
	if strings.TrimSpace(text) == "" {
		text = "표시할 항목이 없습니다."
	}
	return th.Paint(TokenMuted, Truncate(text, width, th.Ellipsis()))
}

func nameOnlyRows(th Theme, table core.Table, width int) []string {
	var out []string
	for _, row := range table.Rows {
		name := ""
		if len(row.Cells) > 0 {
			name = row.Cells[0].Text
		}
		out = append(out, Fit(th.Paint(TokenMuted, Truncate(name, width, th.Ellipsis())), width, th.Ellipsis()))
	}
	return out
}

// BadgeNone marks a cell without a semantic state.
const BadgeNone core.BadgeKind = ""

// badgeGlyph pairs every semantic kind with the approved icon. The Korean text
// stays next to it, so the state never depends on color alone.
func badgeGlyph(th Theme, kind core.BadgeKind) (string, Token) {
	switch kind {
	case core.BadgeRunning:
		return th.Glyphs.OK, TokenSuccess
	case core.BadgeStopped:
		return th.Glyphs.Stopped, TokenMuted
	case core.BadgeUnhealthy:
		return th.Glyphs.Danger, TokenDanger
	case core.BadgeStarting:
		return th.Glyphs.Warn, TokenWarning
	case core.BadgeNotCreated:
		return th.Glyphs.None, TokenMuted
	case core.BadgeRisk:
		return th.Glyphs.Danger, TokenDanger
	default:
		return "", TokenMuted
	}
}

// badgeFallbacks is the approved shortening cascade for a state that cannot
// fit its column: the full label (which the caller already has), then a short
// Korean label, then the icon alone. The icon alone is still a non-color channel
// because every row also carries the column position, but it is the last resort.
func badgeFallbacks(th Theme, kind core.BadgeKind) []string {
	glyph, _ := badgeGlyph(th, kind)
	switch kind {
	case core.BadgeNotCreated:
		return []string{glyph + " 미생성", glyph}
	case core.BadgeRunning:
		return []string{glyph + " 실행", glyph}
	case core.BadgeStopped:
		return []string{glyph + " 중지", glyph}
	case core.BadgeUnhealthy:
		return []string{glyph + " 비정상", glyph}
	case core.BadgeStarting:
		return []string{glyph + " 확인 중", glyph}
	case core.BadgeRisk:
		return []string{glyph + " 위험", glyph}
	default:
		return nil
	}
}

// badgeText is `icon label`. A text that already starts with one of the theme
// glyphs is left alone so the icon is never doubled.
func badgeText(th Theme, kind core.BadgeKind, text string) string {
	glyph, _ := badgeGlyph(th, kind)
	if glyph == "" || strings.TrimSpace(text) == "" {
		return text
	}
	if strings.HasPrefix(text, glyph) {
		return text
	}
	return glyph + " " + text
}

func renderPairs(th Theme, pairs []core.Pair, width int) []string {
	if len(pairs) == 0 {
		return nil
	}
	keyWidth := pairKeyWidth(pairs, width)
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		key := Pad(Truncate(p.Key, keyWidth, th.Ellipsis()), keyWidth)
		valueWidth := max(width-keyWidth-2, 1)
		value := Fit(p.Value, valueWidth, th.Ellipsis())
		lines = append(lines, th.Paint(TokenMuted, key)+"  "+value)
	}
	return lines
}

// pairKeyWidth keeps the label column to at most a third of the row (and never
// below six cells): a label column that eats the width makes every value
// unreadable.
func pairKeyWidth(pairs []core.Pair, width int) int {
	longest := 0
	for _, p := range pairs {
		if w := Width(p.Key); w > longest {
			longest = w
		}
	}
	limit := max(width/3, 6)
	if longest > limit {
		longest = limit
	}
	if longest < 1 {
		longest = 1
	}
	return longest
}

func renderBadges(th Theme, badges []core.Badge, width int) []string {
	if len(badges) == 0 {
		return nil
	}
	var lines []string
	cur := ""
	for _, b := range badges {
		glyph, token := badgeGlyph(th, b.Kind)
		chip := b.Text
		if glyph != "" && !strings.HasPrefix(chip, glyph) {
			chip = glyph + " " + chip
		}
		chip = th.Paint(token, chip)
		next := chip
		if cur != "" {
			next = cur + "   " + chip
		}
		if Width(next) > width && cur != "" {
			lines = append(lines, cur)
			cur = chip
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// calloutLabel is the severity word every callout carries. Color is a third
// channel, never the only one.
func calloutLabel(kind core.CalloutKind) (string, Token) {
	switch kind {
	case core.CalloutWarn:
		return "주의", TokenWarning
	case core.CalloutDanger:
		return "위험", TokenDanger
	case core.CalloutNote:
		return "참고", TokenMuted
	default:
		return "안내", TokenAccent
	}
}

// renderCallout draws the severity word and the body underneath it. The label
// is on its own row so a multi-line guidance block keeps a readable left edge;
// wrapping the first body line next to the label would indent every line by the
// label's width and leave no room for the text.
func renderCallout(th Theme, kind core.CalloutKind, text string, width int) []string {
	label, token := calloutLabel(kind)
	out := []string{th.Bold(token, label)}
	for _, line := range Wrap(text, max(width-2, 1)) {
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+line)
	}
	return out
}

func divider(th Theme, width int) string {
	if width <= 0 {
		return ""
	}
	return th.Paint(TokenMuted, strings.Repeat(th.Glyphs.H, width))
}

func isDividerLine(line string, th Theme) bool {
	plain := strings.TrimSpace(line)
	if plain == "" {
		return false
	}
	return plain == strings.Repeat(th.Glyphs.H, Width(plain))
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
