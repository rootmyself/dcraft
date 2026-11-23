package visual

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
)

// NavItem is one feature in the navigation. The TUI builds it from
// core.Descriptor; the renderer never knows a feature name.
type NavItem struct {
	// Name is the machine name. Only the single-column menu shows it; the
	// sidebar has too little width and uses the Korean title alone.
	Name    string
	Title   string
	Summary string
	// Danger draws the risk badge. It is derived from the descriptor, never
	// from a name list.
	Danger bool
	// Selected marks the current menu cursor.
	Selected bool
}

// NavSection is one heading and its features, already in display order.
type NavSection struct {
	Title string
	Items []NavItem
}

// Nav is the full navigation model: the title line, the sections, the active
// filter and the text shown when the filter matches nothing.
type Nav struct {
	Title    string
	Sections []NavSection
	Filter   string
	// FilterActive is true while the filter has the keyboard. The renderer
	// underlines the field only then; the filter text itself is shown either
	// way so the narrowed list never looks like a short registry.
	FilterActive bool
	Empty        string
	// SummaryOffset is the sidebar description's left edge in display cells.
	SummaryOffset int
}

// Count returns the number of items across all sections.
func (n Nav) Count() int {
	total := 0
	for _, section := range n.Sections {
		total += len(section.Items)
	}
	return total
}

// selected returns the focused item and whether one is marked.
func (n Nav) selected() (NavItem, bool) {
	for _, section := range n.Sections {
		for _, item := range section.Items {
			if item.Selected {
				return item, true
			}
		}
	}
	return NavItem{}, false
}

type navRowKind int

const (
	navRowHeading navRowKind = iota
	navRowItem
	navRowGap
)

type navRow struct {
	kind    navRowKind
	heading string
	item    NavItem
}

// navRows flattens the navigation into renderable rows. A gap row separates
// two sections; the first section has none.
func navRows(nav Nav) []navRow {
	var rows []navRow
	for i, section := range nav.Sections {
		if i > 0 {
			rows = append(rows, navRow{kind: navRowGap})
		}
		rows = append(rows, navRow{kind: navRowHeading, heading: section.Title})
		for _, item := range section.Items {
			rows = append(rows, navRow{kind: navRowItem, item: item})
		}
	}
	return rows
}

// selectedRow is the index of the selected item row, or -1.
func selectedRow(rows []navRow) int {
	for i, row := range rows {
		if row.kind == navRowItem && row.item.Selected {
			return i
		}
	}
	return -1
}

// windowRows keeps at most budget rows and scrolls them so the selected item
// stays visible. A section heading is never the last row of the window: a
// heading alone names a section with nothing under it.
func windowRows(rows []navRow, budget int) []navRow {
	if budget <= 0 || len(rows) == 0 {
		return nil
	}
	if len(rows) <= budget {
		return rows
	}
	sel := selectedRow(rows)
	start := 0
	if sel >= 0 {
		start = sel - budget/2
	}
	if start > len(rows)-budget {
		start = len(rows) - budget
	}
	if start < 0 {
		start = 0
	}
	end := start + budget
	// Drop a trailing heading or gap: it would leave the window ending in a
	// label with nothing below it.
	for end > start+1 && rows[end-1].kind != navRowItem {
		end--
	}
	// If the window opens on an item whose heading is above, pull the heading
	// in so the item is not shown under another section's label.
	if rows[start].kind == navRowItem {
		for i := start - 1; i >= 0; i-- {
			if rows[i].kind == navRowHeading {
				if start-i < budget {
					start = i
					end = start + budget
					for end > start+1 && rows[end-1].kind != navRowItem {
						end--
					}
				}
				break
			}
			if rows[i].kind == navRowGap {
				break
			}
		}
	}
	// A leading gap row is not useful either.
	if rows[start].kind == navRowGap {
		start++
	}
	return rows[start:end]
}

// noWidthLimit stands in for "no size known yet" so callers that render before
// the first WindowSizeMsg still get their content instead of empty rows.
const noWidthLimit = 1 << 20

// sidebarBandMinRows is the inside height from which the band is kept. Below
// it the band disappears before any entry does.
const sidebarBandMinRows = 22

// NavHit is one selectable navigation row and its offset from the top of the
// pane or list that drew it. The renderer and the mouse targets read the same
// window, so a click can never land on a row the screen did not draw.
type NavHit struct {
	Name string
	Row  int
}

// Sidebar renders the persistent navigation for the wide layout as exactly
// height lines of exactly width cells. The frame is inside the body region, so
// it never includes the header or the keycap footer.
//
// The description band at the bottom names the focused entry and its one-line
// summary. A sidebar too short for both keeps its entries and drops the band:
// the menu is the reason the pane exists.
func Sidebar(th Theme, width, height int, nav Nav) []string {
	lines, _ := SidebarWithHits(th, width, height, nav)
	return lines
}

// SidebarWithHits is the single sidebar layout pass. Lines and hit rows are
// returned together so the drawn rows and the mouse targets cannot disagree.
func SidebarWithHits(th Theme, width, height int, nav Nav) ([]string, []NavHit) {
	if width <= 0 || height <= 0 {
		return nil, nil
	}
	ell := th.Glyphs.Ell
	if width < 8 || height < 4 {
		lines := padLines(navTextLines(th, nav, width), width, height, ell)
		return lines, navRowHits(navRows(nav), 0)
	}
	inner := width - 2
	inside := height - 2

	band := []string{}
	if inside >= sidebarBandMinRows {
		if item, ok := nav.selected(); ok {
			band = []string{
				th.Paint(TokenMuted, strings.Repeat(th.Glyphs.H, inner)),
				th.Bold(TokenAccent, " "+th.Glyphs.Sel+" "+Truncate(item.Title, inner-4, ell)),
				th.Paint(TokenMuted, " "+summaryWindow(item.Summary, nav.SummaryOffset, inner-2)),
			}
		}
	}
	if len(band) > inside-1 {
		band = nil
	}
	listRows := inside - len(band)
	rows := windowRows(navRows(nav), listRows)

	interior := make([]string, 0, inside)
	if len(rows) == 0 && nav.Empty != "" {
		interior = append(interior, wrapMuted(th, nav.Empty, inner-2)...)
	}
	interior = append(interior, renderNavRows(th, rows, inner-2, false)...)
	for len(interior) < listRows {
		interior = append(interior, "")
	}
	interior = append(interior, band...)

	out := make([]string, 0, height)
	title, right := sidebarLabel(th, nav)
	out = append(out, borderTop(th, width, title, right))
	for _, line := range interior {
		out = append(out, th.Paint(TokenMuted, th.Glyphs.V)+Fit(line, inner, ell)+th.Paint(TokenMuted, th.Glyphs.V))
	}
	out = append(out, th.Paint(TokenMuted, th.Glyphs.BL+strings.Repeat(th.Glyphs.H, inner)+th.Glyphs.BR))
	// The top border is row 0, so a rendered interior row is offset by one.
	return out, navRowHits(rows, 1)
}

// navRowHits returns the drawn row of every feature entry. Rows are 1:1 with
// renderNavRows, which draws one line per navRow.
func navRowHits(rows []navRow, offset int) []NavHit {
	hits := make([]NavHit, 0, len(rows))
	for i, row := range rows {
		if row.kind == navRowItem {
			hits = append(hits, NavHit{Name: row.item.Name, Row: offset + i})
		}
	}
	return hits
}

func summaryWindow(text string, offset, width int) string {
	var out strings.Builder
	position := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		cluster := graphemes.Str()
		w := Width(cluster)
		end := position + w
		if position < offset && end > offset {
			out.WriteString(strings.Repeat(" ", end-offset))
		} else if position >= offset && end <= offset+width {
			out.WriteString(cluster)
		}
		if end > offset+width {
			break
		}
		position = end
	}
	return Pad(out.String(), width)
}

// sidebarLabel is the title-bar text: the feature count on the left and the
// approved `esc 닫기` hint on the right. The hint says what esc does while the
// sidebar has focus: it closes the pane's task, not the program.
func sidebarLabel(th Theme, nav Nav) (string, string) {
	title := nav.Title
	if nav.Filter != "" || nav.FilterActive {
		title += "  " + filterText(nav, th)
	}
	return title, msgNavClose
}

// filterText is the visible filter field. While the field has the keyboard a
// caret marks where the next character lands; the narrowing itself is visible
// either way, so a short list is never mistaken for a small registry.
//
// The caret follows the glyph set: a non-UTF-8 locale gets a one-cell ASCII
// stand-in rather than a block character it cannot draw.
func filterText(nav Nav, th Theme) string {
	caret := "▏"
	if th.Glyphs.ASCII {
		caret = "_"
	}
	if nav.FilterActive {
		return "/" + nav.Filter + caret
	}
	return "/" + nav.Filter
}

// borderTop draws the frame's top rule with an optional title on the left and
// an optional hint on the right. The hint is dropped before the title when the
// width cannot carry both: the pane's name says what the pane is, the hint only
// names a key.
func borderTop(th Theme, width int, label, right string) string {
	inner := width - 2
	if inner < 1 {
		return th.Paint(TokenMuted, th.Glyphs.TL+th.Glyphs.TR)
	}
	left := ""
	if label != "" {
		text := " " + label + " "
		if Width(text) <= inner {
			left = text
		}
	}
	rightText := ""
	if right != "" {
		text := " " + right + " "
		// Two cells of rule keep the hint from touching the title.
		if Width(text) <= inner-Width(left)-2 {
			rightText = text
		}
	}
	fill := inner - Width(left) - Width(rightText)
	if fill < 0 {
		fill = 0
	}
	top := th.Glyphs.TL + left + strings.Repeat(th.Glyphs.H, fill) + rightText + th.Glyphs.TR
	return th.Paint(TokenMuted, top)
}

// navTextLines is the borderless form used when the pane is narrower than a
// box. It is the same content without a frame; padLines bounds the height.
func navTextLines(th Theme, nav Nav, width int) []string {
	rows := navRows(nav)
	if len(rows) == 0 && nav.Empty != "" {
		return wrapMuted(th, nav.Empty, width)
	}
	return renderNavRows(th, rows, width, false)
}

func wrapMuted(th Theme, text string, width int) []string {
	wrapped := Wrap(text, width)
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, th.Paint(TokenMuted, line))
	}
	return out
}

// MenuLines renders the sectioned menu as single-column body rows. The cursor
// marker stays the two-cell "> " form the keyboard tests already assert, so
// only the grouping around it is new.
//
// A width of zero means the first WindowSizeMsg has not arrived yet; the rows
// are still rendered so the menu is never missing from the first frame.
func MenuLines(th Theme, width int, nav Nav, maxRows int, showName bool) []string {
	lines, _ := MenuLinesWithHits(th, width, nav, maxRows, showName)
	return lines
}

// MenuLinesWithHits draws the single-column menu and returns the row of every
// feature entry in the same pass, so a press resolves against the rows the
// screen actually shows.
func MenuLinesWithHits(th Theme, width int, nav Nav, maxRows int, showName bool) ([]string, []NavHit) {
	if nav.Count() == 0 {
		if nav.Empty == "" {
			return nil, nil
		}
		if width <= 0 {
			return []string{nav.Empty}, nil
		}
		return wrapMuted(th, nav.Empty, width), nil
	}
	rows := windowRows(navRows(nav), maxRows)
	hits := navRowHits(rows, 0)
	if width <= 0 {
		return renderNavRows(th, rows, 0, showName), hits
	}
	return renderNavRows(th, rows, width, showName), hits
}

// renderNavRows draws the windowed rows. Sidebar rows indent by two cells; the
// single-column menu uses the cursor marker in the same cells.
func renderNavRows(th Theme, rows []navRow, width int, showName bool) []string {
	ell := th.Glyphs.Ell
	if width <= 0 {
		// Before the first WindowSizeMsg there is no budget to enforce; the
		// rows are still drawn so the menu is never missing from a frame.
		width = noWidthLimit
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		switch row.kind {
		case navRowGap:
			out = append(out, "")
		case navRowHeading:
			out = append(out, th.Bold(TokenMuted, Truncate(row.heading, width, ell)))
		case navRowItem:
			out = append(out, navItemLine(th, row.item, width, showName))
		}
	}
	return out
}

// navItemLine draws one entry. Priority when the row is narrow: cursor marker,
// title, risk badge, then the secondary columns (machine name, summary). The
// badge is a warning and never yields; the secondary columns are information
// and do.
func navItemLine(th Theme, item NavItem, width int, showName bool) string {
	const markerWidth = 2
	marker := "  "
	if item.Selected {
		marker = th.Paint(TokenAccent, th.Glyphs.Sel) + " "
	}

	badge := ""
	if item.Danger {
		badge = " " + th.Glyphs.Danger + msgDangerBadge
	}
	secondary := ""
	if showName && item.Name != "" {
		secondary = " (" + item.Name + ")"
	}
	if showName && item.Summary != "" {
		// The single-column menu has the width to explain every entry, so the
		// summary travels with the row. The sidebar does not (26–30 cells);
		// there the focused entry's summary sits in the pinned band.
		secondary += " · " + item.Summary
	}

	// Reserve the marker and the badge first, then fill what is left with the
	// title and as much of the secondary columns as fits.
	room := width - markerWidth - Width(badge)
	if room < 1 {
		room = 1
	}
	title := Truncate(item.Title, room, ellOf(th))
	label := title + badge
	if spare := width - markerWidth - Width(label); spare > 0 && secondary != "" {
		label += Truncate(secondary, spare, ellOf(th))
	}

	switch {
	case item.Danger:
		return marker + th.Paint(TokenDanger, label)
	case item.Selected:
		return marker + th.Bold(TokenAccent, label)
	default:
		return marker + label
	}
}

func ellOf(th Theme) string { return th.Glyphs.Ell }

func padLines(lines []string, width, height int, ell string) []string {
	out := make([]string, 0, height)
	for _, line := range lines {
		if len(out) == height {
			break
		}
		out = append(out, Fit(line, width, ell))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	return out
}

// NavRow renders the single-row navigation of the medium layout. Collapsed it
// states the current entry and the total; expanded it adds one count per
// section. Both forms are exactly one row: the row count must not change when
// the menu opens, because the content area keeps its height.
func NavRow(th Theme, width int, nav Nav, expanded bool) string {
	if width <= 0 {
		return ""
	}
	ell := th.Glyphs.Ell
	right := msgNavExpand
	if expanded {
		right = msgNavCollapse
	}
	var left strings.Builder
	left.WriteString(th.Bold(TokenMuted, msgNavMenuLabel))
	if nav.Filter != "" || nav.FilterActive {
		left.WriteString(th.Paint(TokenMuted, " · "))
		left.WriteString(th.Paint(TokenAccent, filterText(nav, th)))
	}
	if item, ok := nav.selected(); ok {
		left.WriteString(th.Paint(TokenMuted, " · "))
		label := th.Glyphs.Sel + " " + item.Title
		if item.Danger {
			label += " " + th.Glyphs.Danger + msgDangerBadge
			left.WriteString(th.Bold(TokenDanger, label))
		} else {
			left.WriteString(th.Bold(TokenAccent, label))
		}
	}
	left.WriteString(th.Paint(TokenMuted, " · "))
	if expanded {
		parts := make([]string, 0, len(nav.Sections))
		for _, section := range nav.Sections {
			parts = append(parts, fmt.Sprintf(msgNavSectionCnt, section.Title, len(section.Items)))
		}
		left.WriteString(th.Bold(TokenMuted, strings.Join(parts, "  ")))
	} else {
		left.WriteString(th.Paint(TokenMuted, fmt.Sprintf(msgNavFeatureCnt, nav.Count())))
	}
	rightText := th.Paint(TokenMuted, right)
	leftBudget := width - Width(rightText) - 2
	if leftBudget < 0 {
		leftBudget = 0
	}
	row := Fit(left.String(), leftBudget, ell)
	if pad := leftBudget - Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	row += strings.Repeat(" ", 2) + rightText
	row = Fit(row, width, ell)
	if !th.Color {
		return row
	}
	return th.PaintOn(TokenMuted, TokenSurface, row)
}
