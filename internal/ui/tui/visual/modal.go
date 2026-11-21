package visual

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ModalRow is one labelled field of a modal. A scalar value is a single row and
// is shortened with the explicit marker when it does not fit; a prose value
// wraps, because losing the end of a sentence is worse than a taller panel.
type ModalRow struct {
	Label string
	Value string
	Wrap  bool
}

// ModalSpec is the content of a modal panel: the fixed fields, an optional
// block of pre-formatted lines (a plan), an optional section with one entry per
// item (the independent approvals), and the risk statement that is pinned
// outside the scrolling band.
type ModalSpec struct {
	// Title is drawn in the top border, Badge in the top-right corner. The
	// badge is a non-color channel: `위험` stays readable when color is gone.
	Title string
	Badge string

	Rows []ModalRow
	// Extra is shown verbatim after the fields, one entry per line; an entry
	// wider than the panel is wrapped with its indentation preserved.
	Extra []string
	// Section is a heading shown before Items, one entry per line.
	Section string
	Items   []string

	// RiskLabel prefixes the pinned risk band; Risk is the sentence list. The
	// band never scrolls away: the scroll band keeps only the cut remainder.
	RiskLabel string
	Risk      []string

	// ScrollTop is the first visible scroll-band row.
	ScrollTop int
}

// ModalWidth is the approved panel width: clamp(width-8, 40, 76), and the whole
// terminal when it is narrower than the minimum panel.
func ModalWidth(width int) int {
	if width <= 0 {
		return 0
	}
	if width < 40 {
		return width
	}
	return clamp(width-8, 40, 76)
}

// modalSpan is one styled span of a panel row.
type modalSpan struct {
	Text  string
	Token Token
	Bold  bool
}

// modalLine is one row of the panel as styled spans.
type modalLine []modalSpan

// ComposeModal draws spec as a centered panel over the body of an already
// composed frame. Header and footer keep their exact rows and contrast; every
// body row outside the panel is dimmed to muted. The result has the same size
// as the frame.
//
// With no known size the header, the panel and the footer are returned in order
// so a model built before the first WindowSizeMsg still renders the whole
// confirmation.
func ComposeModal(th Theme, width, height int, frame []string, spec ModalSpec) []string {
	if width <= 0 || height <= 0 || len(frame) < 3 {
		return sizelessModal(th, frame, spec)
	}
	panel := renderModalPanel(th, width, height, spec)
	if len(panel) == 0 {
		return frame
	}
	mw := ModalWidth(width)
	bottom := height - 1
	top := 1 + (height-2-len(panel))/2
	if top < 1 {
		top = 1
	}
	if top+len(panel) > bottom {
		top = bottom - len(panel)
	}
	if top < 1 {
		top = 1
	}
	left := (width - mw) / 2
	if left < 0 {
		left = 0
	}

	out := make([]string, len(frame))
	copy(out, frame)
	for i := 1; i < len(frame)-1; i++ {
		out[i] = dimLine(th, frame[i], width)
	}
	for i, row := range panel {
		y := top + i
		if y <= 0 || y >= len(out)-1 {
			continue
		}
		plain := Fit(ansi.Strip(frame[y]), width, th.Glyphs.Ell)
		gutterLeft := gutterCells(plain, 0, left)
		gutterRight := gutterCells(plain, left+mw, width-left-mw)
		out[y] = th.Paint(TokenMuted, gutterLeft) + row + th.Paint(TokenMuted, gutterRight)
	}
	return out
}

// gutterCells is exactly w cells of the already-stripped row, starting at cell
// `left`. The panel covers the cells in between, so a wide grapheme that
// straddles either boundary belongs to neither gutter: it is dropped and the
// gutter is padded. ansi.Cut alone is not enough because it keeps a straddling
// grapheme whole, which would make the row one cell wider than the terminal.
func gutterCells(plain string, left, w int) string {
	if w <= 0 {
		return ""
	}
	_, rest := splitCells(plain, left)
	part, _ := splitCells(rest, w)
	return Pad(part, w)
}

// dimLine lowers one body row to muted. The content is kept exactly: the dim
// changes contrast, never what the screen says.
func dimLine(th Theme, line string, width int) string {
	return th.Paint(TokenMuted, Fit(ansi.Strip(line), width, th.Glyphs.Ell))
}

// sizelessModal renders the content between the header and the footer with no
// geometry: there is no width to center in or to truncate against yet.
func sizelessModal(th Theme, frame []string, spec ModalSpec) []string {
	content := renderModalContent(th, spec, noSize)
	out := make([]string, 0, len(frame)+len(content)+3)
	if len(frame) > 0 {
		out = append(out, frame[0])
	}
	if spec.Title != "" {
		// The panel has no borders yet, so the title is stated on its own
		// row: the screen still says what it is asking.
		title := " " + spec.Title
		if spec.Badge != "" {
			title += " · " + spec.Badge
		}
		out = append(out, th.Bold(TokenAccent, title))
	}
	for _, line := range content {
		out = append(out, renderModalLine(th, line, noSize))
	}
	if len(frame) > 1 {
		out = append(out, frame[len(frame)-1])
	}
	return out
}

// noSize stands in for "no width known yet".
const noSize = 1 << 20

// modalLayout is the geometry of one panel: the rows that scroll, the rows
// that are pinned, and how many of the scrolling rows fit.
type modalLayout struct {
	width   int
	content int
	// scroll is the full scrolling band, risk continuation included.
	scroll []modalLine
	// pinned is the risk band that is always drawn.
	pinned []string
	cut    bool
	// visible is how many scroll rows fit; the position row is already
	// deducted when it is needed.
	visible int
	pos     bool
}

// maxTop is the largest scroll position that still fills the band.
func (l modalLayout) maxTop() int {
	if max := len(l.scroll) - l.visible; max > 0 {
		return max
	}
	return 0
}

// layoutModal is the single geometry pass. The renderer, the clamp and the
// scroll keys all read it, so the drawn rows and the position row can never
// disagree.
func layoutModal(th Theme, width, height int, spec ModalSpec) modalLayout {
	mw := ModalWidth(width)
	inner := mw - 2
	content := inner - 4
	if content < 1 {
		content = 1
	}
	bodyRows := height - 2
	// The approved pinned band: clamp(innerHeight/8, 1, 3) rows. innerHeight
	// is what the panel's inside can be, so a tall terminal pins more of the
	// risk and a short one still pins at least one row.
	innerAvail := bodyRows - 2
	if innerAvail < 1 {
		innerAvail = 1
	}
	riskRows := 0
	if len(spec.Risk) > 0 {
		riskRows = clamp(innerAvail/8, 1, 3)
	}

	riskAll := wrapRiskLines(spec.RiskLabel, spec.Risk, content)
	pinned := riskAll
	cut := false
	if len(pinned) > riskRows {
		pinned = pinned[:riskRows]
		cut = true
	}

	scroll := buildScrollLines(spec, content)
	if cut {
		// The pinned band guarantees visibility, it never deletes: the cut
		// remainder lives in its own scrollable section.
		scroll = append(scroll, modalLine{}, modalLine{{Text: riskContinuation, Token: TokenMuted, Bold: true}})
		for _, line := range riskAll[len(pinned):] {
			scroll = append(scroll, modalLine{{Text: line}})
		}
	}

	// Borders take 2 rows; the danger rule above the risk takes one row when
	// there is a risk. The position row and the pinned band share the rest.
	//
	// When the body is too short for all of that, decoration yields in a fixed
	// order: first the danger rule, then the scrolling band, then further
	// pinned risk rows. The risk statement is never the part that is dropped
	// before the frame's own rows are.
	rule := 0
	if len(pinned) > 0 {
		rule = 1
	}
	if budget := bodyRows - 2 - rule - len(pinned); budget < 1 && rule == 1 {
		rule = 0
	}
	if limit := bodyRows - 2 - rule; len(pinned) > limit {
		if limit < 0 {
			limit = 0
		}
		pinned = pinned[:limit]
		cut = true
	}
	available := bodyRows - 2 - rule - len(pinned)
	if available < 0 {
		available = 0
	}
	pos := false
	if len(scroll) > available && available >= 2 {
		pos = true
		available--
	}
	visible := len(scroll)
	if visible > available {
		visible = available
	}
	return modalLayout{
		width: mw, content: content, scroll: scroll, pinned: pinned,
		cut: cut, visible: visible, pos: pos,
	}
}

// ModalMetrics reports the scrolling band and the largest legal scroll
// position. Keys clamp through it so a key press and the drawn frame agree.
func ModalMetrics(th Theme, width, height int, spec ModalSpec) (visible, maxTop int) {
	if width <= 0 || height <= 0 {
		return len(spec.Rows) + len(spec.Extra) + len(spec.Items), 0
	}
	l := layoutModal(th, width, height, spec)
	return l.visible, l.maxTop()
}

// renderModalPanel builds the panel as wide as the approved formula says and as
// tall as the body allows.
func renderModalPanel(th Theme, width, height int, spec ModalSpec) []string {
	l := layoutModal(th, width, height, spec)
	top := spec.ScrollTop
	if top < 0 {
		top = 0
	}
	if max := l.maxTop(); top > max {
		top = max
	}

	panel := make([]string, 0, l.visible+5)
	panel = append(panel, modalTopBorder(th, l.width, spec.Title, spec.Badge))
	for i := 0; i < l.visible; i++ {
		panel = append(panel, modalContentRow(th, l.width, l.content, l.scroll[top+i]))
	}
	if l.pos {
		panel = append(panel, modalContentRow(th, l.width, l.content, modalPositionLine(top+1, len(l.scroll))))
	}
	if len(l.pinned) > 0 {
		panel = append(panel, modalRule(th, l.width, TokenDanger))
		for i, line := range l.pinned {
			text := line
			if l.cut && i == len(l.pinned)-1 {
				text += th.Glyphs.Ell
			}
			// The label is the non-color channel; the sentence carries the
			// danger color and weight. Both are always drawn.
			line := modalLine{{Text: text, Token: TokenDanger, Bold: true}}
			panel = append(panel, modalContentRow(th, l.width, l.content, line))
		}
	}
	panel = append(panel, modalBottomBorder(th, l.width))
	return panel
}

// riskContinuation is the approved heading for the part of the risk that the
// pinned band had to shorten.
const riskContinuation = "위험 · 이어서"

// wrapRiskLines renders the risk statement with its label on the first line.
// The sentence continues on the next line without repeating the label.
func wrapRiskLines(label string, risk []string, width int) []string {
	text := strings.TrimSpace(strings.Join(risk, " "))
	if text == "" {
		return nil
	}
	if label == "" {
		return Wrap(text, width)
	}
	prefix := label + " "
	first := Wrap(prefix+text, width)
	if len(first) <= 1 || Width(prefix) >= width-1 {
		return first
	}
	rest := Wrap(text, width-Width(prefix))
	indent := strings.Repeat(" ", Width(prefix))
	out := []string{prefix + rest[0]}
	for _, line := range rest[1:] {
		out = append(out, indent+line)
	}
	return out
}

// buildScrollLines is the scrolling band: the fixed fields, the plan, and the
// independent approvals. Nothing here is dropped, regardless of width.
func buildScrollLines(spec ModalSpec, content int) []modalLine {
	labelW := modalLabelWidth(spec.Rows, content)
	var out []modalLine
	for _, row := range spec.Rows {
		out = append(out, fieldLines(row, content, labelW)...)
	}
	if len(spec.Extra) > 0 {
		out = append(out, modalLine{})
		for _, line := range spec.Extra {
			for _, wrapped := range wrapIndented(line, content) {
				out = append(out, modalLine{{Text: wrapped}})
			}
		}
	}
	if len(spec.Items) > 0 {
		if spec.Section != "" {
			out = append(out, modalLine{}, modalLine{{Text: spec.Section, Token: TokenMuted, Bold: true}})
		} else if len(out) > 0 {
			out = append(out, modalLine{})
		}
		for _, item := range spec.Items {
			for _, wrapped := range Wrap(item, content) {
				out = append(out, modalLine{{Text: wrapped}})
			}
		}
	}
	return collapseBlank(out)
}

// collapseBlank keeps one separator row between blocks. Two callers may each
// contribute a leading blank (a plan after a field, a timeline after a plan);
// two empty rows in a row waste a row of the scrolling band.
func collapseBlank(lines []modalLine) []modalLine {
	out := make([]modalLine, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 && len(out) > 0 && len(out[len(out)-1]) == 0 {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

// fieldLines draws one label and its value. A wrapped value continues in the
// value column without repeating the label.
func fieldLines(row ModalRow, content, labelW int) []modalLine {
	label := Pad(Truncate(row.Label, labelW, "…"), labelW)
	valueWidth := content - labelW - 1
	if valueWidth < 1 {
		valueWidth = 1
	}
	var values []string
	if row.Wrap {
		values = Wrap(row.Value, valueWidth)
	} else {
		values = []string{Truncate(row.Value, valueWidth, "…")}
	}
	if len(values) == 0 {
		values = []string{""}
	}
	out := make([]modalLine, 0, len(values))
	for i, value := range values {
		if i == 0 {
			out = append(out, modalLine{
				{Text: label, Token: TokenMuted},
				{Text: " "},
				{Text: value},
			})
			continue
		}
		out = append(out, modalLine{
			{Text: strings.Repeat(" ", labelW+1)},
			{Text: value},
		})
	}
	return out
}

// wrapIndented wraps a pre-formatted line and keeps its indentation on the
// continuation rows, so a plan's bullets stay readable.
func wrapIndented(line string, width int) []string {
	if Width(line) <= width {
		return []string{line}
	}
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]
	indentWidth := Width(indent)
	if indentWidth >= width || indentWidth == 0 {
		return Wrap(strings.TrimSpace(line), width)
	}
	wrapped := Wrap(trimmed, width-indentWidth)
	out := make([]string, len(wrapped))
	for i, part := range wrapped {
		if part == "" {
			out[i] = ""
			continue
		}
		out[i] = indent + part
	}
	return out
}

// modalPositionLine is the approved `position · keys` row: `12/24 · PgUp/PgDn`.
// The position counts rows, so it always describes what is on screen.
func modalPositionLine(first, total int) modalLine {
	return modalLine{{Text: strconv.Itoa(first) + "/" + strconv.Itoa(total) + " · PgUp/PgDn", Token: TokenMuted}}
}

// modalLabelWidth is the label column: the longest label, never more than half
// the panel's content width. A label column that eats the width makes every
// value unreadable.
func modalLabelWidth(rows []ModalRow, content int) int {
	longest := 0
	for _, row := range rows {
		if w := Width(row.Label); w > longest {
			longest = w
		}
	}
	limit := content / 2
	if limit < 1 {
		limit = 1
	}
	if longest > limit {
		longest = limit
	}
	if longest < 1 {
		longest = 1
	}
	return longest
}

// modalTopBorder is `╭─ title ──── badge ─╮`. The title shrinks before the
// badge does: the badge is the non-color warning channel.
func modalTopBorder(th Theme, width int, title, badge string) string {
	inner := width - 2
	right := ""
	if badge != "" {
		right = " " + badge + " "
	}
	titled := ""
	if title != "" {
		titled = " " + title + " "
	}
	if Width(titled)+Width(right)+2 > inner {
		titled = ""
	}
	if Width(right)+2 > inner {
		right = ""
	}
	// TL, the leading horizontal cell and TR are the fixed cells; the title
	// and badge sit between them.
	fill := inner - 1 - Width(titled) - Width(right)
	if fill < 0 {
		fill = 0
	}
	row := plateSpan(th, TokenMuted, false, th.Glyphs.TL+th.Glyphs.H)
	row += plateSpan(th, TokenAccent, true, titled)
	row += plateSpan(th, TokenMuted, false, strings.Repeat(th.Glyphs.H, fill))
	row += plateSpan(th, TokenDanger, true, right)
	row += plateSpan(th, TokenMuted, false, th.Glyphs.TR)
	return Fit(row, width, th.Glyphs.Ell)
}

func modalBottomBorder(th Theme, width int) string {
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	row := plateSpan(th, TokenMuted, false, th.Glyphs.BL+strings.Repeat(th.Glyphs.H, inner)+th.Glyphs.BR)
	return Fit(row, width, th.Glyphs.Ell)
}

// modalRule is the danger divider between the scroll band and the pinned risk.
func modalRule(th Theme, width int, tok Token) string {
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	row := plateSpan(th, tok, false, th.Glyphs.V+strings.Repeat(th.Glyphs.H, inner)+th.Glyphs.V)
	return Fit(row, width, th.Glyphs.Ell)
}

// modalContentRow is `│  content  │` on the surface plate.
func modalContentRow(th Theme, width, content int, line modalLine) string {
	row := plateSpan(th, TokenMuted, false, th.Glyphs.V+"  ")
	row += renderModalLine(th, line, content)
	row += plateSpan(th, TokenMuted, false, "  "+th.Glyphs.V)
	return Fit(row, width, th.Glyphs.Ell)
}

// renderModalLine fits the styled spans into exactly width cells.
func renderModalLine(th Theme, line modalLine, width int) string {
	var b strings.Builder
	used := 0
	for _, span := range line {
		if used >= width {
			break
		}
		text := span.Text
		if Width(text) > width-used {
			text = Truncate(text, width-used, th.Glyphs.Ell)
		}
		used += Width(text)
		b.WriteString(plateSpan(th, span.Token, span.Bold, text))
	}
	if used < width {
		b.WriteString(plateSpan(th, TokenMuted, false, strings.Repeat(" ", width-used)))
	}
	return b.String()
}

// plateSpan paints one span of a panel row: a token foreground on the surface
// plate. The plate is what makes the panel read as raised when color is gone,
// so every panel cell — padding included — is painted.
func plateSpan(th Theme, tok Token, bold bool, s string) string {
	if !th.Color || s == "" {
		return s
	}
	hex, ok := th.Palette.Color(tok)
	if !ok {
		return s
	}
	return sgr(hex, th.Palette.Surface, bold) + s + "\x1b[0m"
}

// renderModalContent is the plain span list of the scroll band, used when no
// size is known yet.
func renderModalContent(th Theme, spec ModalSpec, width int) []modalLine {
	lines := buildScrollLines(spec, width)
	if len(spec.Risk) == 0 {
		return lines
	}
	lines = append(lines, modalLine{}, modalLine{{Text: spec.RiskLabel, Token: TokenDanger, Bold: true}})
	for _, line := range wrapRiskLines("", spec.Risk, width) {
		lines = append(lines, modalLine{{Text: line, Token: TokenDanger, Bold: true}})
	}
	return lines
}
