package visual

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// RenderDocument renders a neutral document for the TUI at the given width.
// Text wraps to the available width; table cells retain their explicit
// truncation marker and full text in the detail view. The renderer reads only
// the injected theme.
func RenderDocument(th Theme, doc core.Document, width int) []string {
	return RenderDocumentLayout(th, doc, width).Lines
}

// DetailEntry is one value the body had to cut, with the row identity that
// tells the reader where it belongs. The value is always complete.
type DetailEntry struct {
	Label string
	Value string
	Kind  core.ValueKind
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
					out = append(out, DetailEntry{Label: p.Key, Value: p.Value, Kind: p.Kind})
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
			lines = append(lines, "  "+paintValue(th, entry.Kind, line))
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
// to the leading columns the screen can afford.
const compactTableWidth = 60

// compactTableColumns is the default compact fold: service and state, the pair
// every table screen exists to compare.
const compactTableColumns = 2

// TableOptions are presentation choices the caller owns. The compact fold is a
// screen decision: a table wider than the breakpoint still folds when its screen
// is compact (a short terminal), and a screen that must keep three leading
// columns says so.
type TableOptions struct {
	// Compact forces the compact fold regardless of width. Without it the fold
	// happens only below the width breakpoint, which is the status table's rule.
	Compact bool
	// CompactColumns is how many leading columns a compact table keeps. Zero
	// means compactTableColumns.
	CompactColumns int
	// CursorRow is the 1-based row the screen's cursor is on, bolded so the
	// position survives a colorless frame. Zero means no cursor: the status
	// table has no selection and must never bold a row by accident.
	CursorRow int
}

// LayoutTable fits a table into width cells with the default options.
func LayoutTable(th Theme, table core.Table, width int) TableLayout {
	return LayoutTableOptions(th, table, width, TableOptions{})
}

// LayoutTableOptions fits a table into width cells.
//
// Fixed columns keep their exact width. Flexible columns share what is left by
// weight, clamp to Min/Max, and the remainder is redistributed one cell at a
// time so the columns sum to the available width. When the minimums no longer
// fit, columns are dropped by DropPriority: port, then image, then health.
// Service, state and instances are never dropped by width because comparing
// them is the reason the table exists; below the compact breakpoint the table
// deliberately shows only the leading columns the caller names.
func LayoutTableOptions(th Theme, table core.Table, width int, opts TableOptions) TableLayout {
	ell := th.Ellipsis()
	if len(table.Rows) == 0 {
		return TableLayout{Rows: []string{emptyTableText(th, table.EmptyText, width)}}
	}

	type placed struct {
		col  core.Column
		orig int
	}
	var chosen []placed
	if opts.Compact || width < compactTableWidth {
		keep := opts.CompactColumns
		if keep <= 0 {
			keep = compactTableColumns
		}
		for i, col := range table.Columns {
			if i >= keep {
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

	for rowIndex, row := range table.Rows {
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
			if cell.Badge != core.BadgeNone {
				text = badgeText(th, cell.Badge, text)
			}
			// A badge that does not fit falls back through its shorter labels
			// before it is cut: `○ 컨테이너 없음` becomes `○ 미생성`, then
			// the icon alone. Silently leaving `○ 컨…` would name nothing.
			if Width(text) > widths[i] && cell.Badge != core.BadgeNone {
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
				layout.Truncations = append(layout.Truncations, DetailEntry{Label: label, Value: value, Kind: cell.Kind})
			}
			cells[i] = alignCell(p.col.Align, text, widths[i], ell)
			if cell.Badge != core.BadgeNone {
				_, token := badgeGlyph(th, cell.Badge)
				cells[i] = th.Paint(token, cells[i])
			} else if !row.Selectable {
				cells[i] = th.Paint(TokenMuted, cells[i])
			}
		}
		line := strings.Join(cells, " ")
		if opts.CursorRow > 0 && rowIndex == opts.CursorRow-1 {
			// The approved selection marker is the gutter glyph plus bold. Bold
			// is an attribute rather than a color, but a colorless theme still
			// promises no escape sequences at all, so it keeps only the glyph
			// (drawn by the caller) and this row stays attribute-free.
			if th.Color {
				line = boldText(line)
			}
		}
		layout.Rows = append(layout.Rows, Fit(line, width, ell))
	}
	return layout
}

// boldText wraps a row in the bold attribute. It is unconditional, unlike
// Theme.Bold: the selection marker must survive a colorless theme, and bold
// emits no cells.
func boldText(s string) string {
	if s == "" {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
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
		text = msgEmptyTable
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
//
// `◐ 확인 중` is nine cells and the health column is eight, so `starting` needs
// its own short form: without one it fell straight to the bare icon, which names
// nothing. `◐ 확인` keeps the icon and a Korean word for the same state.
func badgeFallbacks(th Theme, kind core.BadgeKind) []string {
	glyph, _ := badgeGlyph(th, kind)
	switch kind {
	case core.BadgeNotCreated:
		return []string{glyph + msgBadgeNotCreated, glyph}
	case core.BadgeRunning:
		return []string{glyph + msgBadgeRunning, glyph}
	case core.BadgeStopped:
		return []string{glyph + msgBadgeStopped, glyph}
	case core.BadgeUnhealthy:
		return []string{glyph + msgBadgeUnhealthy, glyph}
	case core.BadgeStarting:
		return []string{glyph + msgBadgeStarting, glyph}
	case core.BadgeRisk:
		return []string{glyph + msgBadgeRisk, glyph}
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
		indent := keyWidth + 2
		if valueWidth < 12 && width > 2 {
			lines = append(lines, th.Paint(TokenMuted, key))
			indent = 2
			valueWidth = width - indent
			for _, value := range Wrap(p.Value, valueWidth) {
				lines = append(lines, strings.Repeat(" ", indent)+paintValue(th, p.Kind, value))
			}
			continue
		}
		for i, value := range Wrap(p.Value, valueWidth) {
			if i == 0 {
				lines = append(lines, th.Paint(TokenMuted, key)+"  "+paintValue(th, p.Kind, value))
			} else {
				lines = append(lines, strings.Repeat(" ", indent)+paintValue(th, p.Kind, value))
			}
		}
	}
	return lines
}

// renderValueLines paints one line body. Each run is painted by its declared
// kind before wrapping, so the existing ANSI-safe wrap keeps a coloured run
// intact across rows and plain labels stay in the default foreground.
func renderValueLines(th Theme, lines []core.ValueLine, width int) []string {
	var out []string
	for _, line := range lines {
		out = append(out, Wrap(paintValueLine(th, line), width)...)
	}
	return out
}

// paintValueLine paints every declared run of one line. The text is unchanged;
// only a run with a kind receives paint.
func paintValueLine(th Theme, line core.ValueLine) string {
	var b strings.Builder
	for _, run := range line {
		b.WriteString(paintValue(th, run.Kind, run.Text))
	}
	return b.String()
}

func paintValue(th Theme, kind core.ValueKind, value string) string {
	switch kind {
	case core.ValuePath:
		return th.Paint(TokenInfo, value)
	case core.ValueURL:
		return th.Paint(TokenURL, value)
	case core.ValueImage:
		return th.Paint(TokenCode, value)
	case core.ValueCommand:
		return th.Paint(TokenCommand, value)
	case core.ValueTime:
		return th.Paint(TokenMuted, value)
	case core.ValueService:
		return th.Paint(TokenName, value)
	case core.ValueProject:
		return th.Paint(TokenProject, value)
	default:
		return value
	}
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
			cur = ""
		}
		if cur == "" && Width(chip) > width {
			wrapped := Wrap(chip, width)
			lines = append(lines, wrapped[:len(wrapped)-1]...)
			cur = wrapped[len(wrapped)-1]
		} else if cur == "" {
			cur = chip
		} else {
			cur = next
		}
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
		return msgCalloutWarn, TokenWarning
	case core.CalloutDanger:
		return msgCalloutDanger, TokenDanger
	case core.CalloutNote:
		return msgCalloutNote, TokenMuted
	default:
		return msgCalloutInfo, TokenAccent
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

// collapseBlankRuns keeps at most one blank line in a row. A paragraph may
// already carry a leading blank line, and the block gap adds another one; the
// combined result would be a two-line hole instead of a section boundary. An
// empty line never carries content, so the run length is the only decision.
func collapseBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
