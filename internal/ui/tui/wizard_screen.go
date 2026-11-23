package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// wizardStepScreen is one question split into the rows that never move (head),
// the selectable option cards, and the rows after them (tail). The view, the
// cursor follow and the scroll position all read this one structure, so the
// card the keys move to is always the card that is drawn.
type wizardStepScreen struct {
	head   []string
	cards  []string
	ranges [][2]int
	tail   []string
}

func (s wizardStepScreen) lines() []string {
	out := make([]string, 0, len(s.head)+len(s.cards)+len(s.tail))
	out = append(out, s.head...)
	out = append(out, s.cards...)
	out = append(out, s.tail...)
	return out
}

// wizardStepScreen builds the screen of one question: the flow title, the
// position in the flow, the step title, the prompt, the answer control and any
// inline guidance about that answer. Nothing here reads the environment; all
// text and options come from the feature's spec.
func (m *Model) wizardStepScreen(step core.WizardStep) wizardStepScreen {
	var screen wizardStepScreen
	width := m.bodyWidth()
	addStyled := func(style func(string) string, text string) {
		for _, line := range m.wrapTo(text, width) {
			screen.head = append(screen.head, style(line))
		}
	}
	if title := strings.TrimSpace(m.wizardSpec.Title); title != "" {
		addStyled(m.title, title)
		screen.head = append(screen.head, "")
	}
	ordinal, total := m.wizardProgress()
	progress := fmt.Sprintf("%s %d/%d", msgWizardProgress, ordinal, total)
	screen.head = append(screen.head, m.hintText(progress))
	addStyled(m.title, step.Title)
	screen.head = append(screen.head, "")
	if prompt := strings.TrimSpace(step.Prompt); prompt != "" {
		addStyled(func(s string) string { return s }, prompt)
		screen.head = append(screen.head, "")
	}

	if step.List {
		for i, item := range m.wizardItems {
			marker := "  "
			if i == m.wizardChoice {
				marker = "> "
			}
			if i == m.wizardChoice && m.wizardEditing {
				item += msgListEditing
			}
			start := len(screen.cards)
			screen.cards = append(screen.cards, wrapWithPrefix(item, marker+"* ", "    ", width, m.theme.Glyphs.Ell)...)
			screen.ranges = append(screen.ranges, [2]int{start, len(screen.cards)})
		}
		screen.ranges = append(screen.ranges, [2]int{len(screen.cards), len(screen.cards)})
		marker := "  "
		if m.wizardChoice == len(m.wizardItems) || m.wizardEditing {
			marker = "> "
		}
		input := m.wizardInput
		available := width - visual.Width(marker) - visual.Width(cursorGlyph)
		if width > 0 && visual.Width(input) > available {
			ell := m.theme.Glyphs.Ell
			available -= visual.Width(ell)
			if available < 0 {
				available = 0
			}
			inputWidth := visual.Width(input)
			input = ell + ansi.Cut(input, inputWidth-available, inputWidth)
		}
		screen.tail = append(screen.tail, marker+input+cursorGlyph)
		for _, line := range m.wrapTo(fmt.Sprintf(msgListHint, len(m.wizardItems)), width) {
			screen.tail = append(screen.tail, m.hintText(line))
		}
		if m.wizardNotice != "" {
			screen.tail = append(screen.tail, m.noticeText(m.wizardNotice))
		}
		if step.CustomHint != "" {
			screen.tail = append(screen.tail, m.wrapTo(step.CustomHint, width)...)
		}
		// A single wrapped item must fit in the band; otherwise no part of
		// that item can be selected or drawn by the whole-card viewport.
		if budget := m.bodyBudget(); budget > 0 {
			maxRows := budget - m.titleRows() - len(screen.head) - len(screen.tail) - 1
			if maxRows < 1 {
				maxRows = 1
			}
			var cards []string
			for i, r := range screen.ranges[:len(screen.ranges)-1] {
				start := len(cards)
				end := r[1]
				if end-r[0] > maxRows {
					end = r[0] + maxRows
				}
				cards = append(cards, screen.cards[r[0]:end]...)
				if end < r[1] {
					last := len(cards) - 1
					cards[last] = visual.Fit(cards[last]+m.theme.Glyphs.Ell, width, m.theme.Glyphs.Ell)
				}
				screen.ranges[i] = [2]int{start, len(cards)}
			}
			screen.cards = cards
			screen.ranges[len(screen.ranges)-1] = [2]int{len(cards), len(cards)}
		}
		return screen
	}
	if len(step.Options) > 0 {
		if step.Multi {
			screen.cards, screen.ranges = m.wizardCheckboxes(step)
			return screen
		}
		screen.cards, screen.ranges = m.wizardCards(step)
		return screen
	}
	if !step.AllowCustom {
		// A question with neither options nor typing is a feature bug; the
		// screen states it instead of showing an empty list.
		screen.tail = append(screen.tail, m.noticeText(msgWizardNeedChoice))
		return screen
	}

	// The typed answer: the cursor is drawn after the value so an empty field
	// still shows where typing lands.
	input := m.wizardInput
	screen.cards = []string{msgWizardInputLabel + input + cursorGlyph}
	screen.ranges = append(screen.ranges, [2]int{0, 1})
	// Guidance about the answer sits directly under the field it belongs to.
	if m.wizardNotice != "" {
		for _, line := range m.wrapTo(m.wizardNotice, width) {
			screen.tail = append(screen.tail, m.noticeText(line))
		}
	}
	hint := step.CustomHint
	if step.Optional {
		if hint != "" {
			hint += " · "
		}
		hint += msgWizardOptionalHint
	}
	if hint != "" {
		for _, line := range m.wrapTo(hint, width) {
			screen.tail = append(screen.tail, m.hintText(line))
		}
	}
	return screen
}

// cursorGlyph is the visible end-of-input caret. It is ASCII in every locale
// so a non-UTF-8 terminal still shows where typing lands.
const cursorGlyph = "_"

// wizardCards draws each choice as its own selectable card. The cursor is the
// `> ` marker inside the card, so the current choice is readable with no color
// at all. All cards share one width — a list of differently sized plates does
// not read as one choice list — and the width never exceeds the body. Compact
// mode drops the border, exactly like the rest of the frame.
func (m *Model) wizardCards(step core.WizardStep) ([]string, [][2]int) {
	width := m.bodyWidth()
	cardWidth := 0
	for _, option := range step.Options {
		// 2 cells of marker, 2 of padding and 2 of border.
		need := 2 + visual.Width(option.Label) + 4
		if need > cardWidth {
			cardWidth = need
		}
	}
	if width > 0 && cardWidth > width {
		cardWidth = width
	}
	var lines []string
	var ranges [][2]int
	for i, option := range step.Options {
		start := len(lines)
		lines = append(lines, m.wizardCard(option, i == m.wizardChoice, cardWidth)...)
		ranges = append(ranges, [2]int{start, len(lines)})
	}
	return lines, ranges
}

// wizardCard is one option card at the shared card width. Decorated mode hugs
// the label inside a bordered plate; compact mode keeps the marker row only. A
// label wider than the card wraps inside it instead of being cut, so a narrow
// terminal still shows the whole option.
func (m *Model) wizardCard(option core.WizardOption, selected bool, width int) []string {
	marker := "  "
	if selected {
		marker = "> "
	}
	if width <= 0 {
		return []string{marker + option.Label}
	}
	if m.compact() || width < 8 {
		return wrapWithPrefix(option.Label, marker, "  ", width, m.theme.Glyphs.Ell)
	}

	inner := width - 4
	text := wrapWithPrefix(option.Label, marker, "  ", inner, m.theme.Glyphs.Ell)
	token := visual.TokenMuted
	if selected {
		token = visual.TokenAccent
	}
	border := func(s string) string { return m.theme.Paint(token, s) }
	plain := func(s string) string {
		return border(m.theme.Glyphs.V) + " " + visual.Fit(s, inner, m.theme.Glyphs.Ell) + " " + border(m.theme.Glyphs.V)
	}
	rule := strings.Repeat(m.theme.Glyphs.H, width-2)

	out := make([]string, 0, len(text)+2)
	out = append(out, border(m.theme.Glyphs.TL+rule+m.theme.Glyphs.TR))
	for _, part := range text {
		label := part
		if selected {
			label = m.theme.Bold(token, part)
		}
		out = append(out, plain(label))
	}
	out = append(out, border(m.theme.Glyphs.BL+rule+m.theme.Glyphs.BR))
	return out
}

// wizardCheckboxes is the multi-select control: an explicit [ ] / [x] box per
// value with the same cursor marker the rest of the TUI uses. Space toggles
// the highlighted box; the state is never implied by color.
func (m *Model) wizardCheckboxes(step core.WizardStep) ([]string, [][2]int) {
	var lines []string
	var ranges [][2]int
	width := m.bodyWidth()
	for i, option := range step.Options {
		marker := "  "
		if i == m.wizardChoice {
			marker = "> "
		}
		box := "[ ]"
		if m.wizardChecked[option.Value] {
			box = "[x]"
		}
		start := len(lines)
		lines = append(lines, wrapWithPrefix(option.Label, marker+box+" ", "    ", width, m.theme.Glyphs.Ell)...)
		ranges = append(ranges, [2]int{start, len(lines)})
	}
	return lines, ranges
}

// wrapWithPrefix wraps text under a first-line prefix and keeps continuation
// rows aligned with it. The cell budget is the body width, so nothing is cut.
func wrapWithPrefix(text, first, rest string, width int, ell string) []string {
	if width <= 0 {
		return []string{first + text}
	}
	prefixWidth := visual.Width(first)
	if prefixWidth >= width {
		return []string{visual.Fit(first, width, ell)}
	}
	parts := visual.Wrap(text, width-prefixWidth)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		lead := first
		if i > 0 {
			lead = rest
			if visual.Width(lead) > prefixWidth {
				lead = strings.Repeat(" ", prefixWidth)
			}
			lead += strings.Repeat(" ", prefixWidth-visual.Width(lead))
		}
		out = append(out, lead+part)
	}
	return out
}

// wrapTo wraps prose at the body width. Before the first WindowSizeMsg the
// width is unknown, so the text is returned unwrapped rather than guessed at.
func (m *Model) wrapTo(text string, width int) []string {
	if width <= 0 {
		return strings.Split(text, "\n")
	}
	return visual.Wrap(text, width)
}

// wizardStepScrolls reports whether the option cards have cards outside the
// window.
func (m *Model) wizardStepScrolls() bool {
	return m.wizardBandData().Scrolls
}

// wizardViewport is the scroll window of the current question, expressed in
// cards. It is the same geometry the frame draws.
func (m *Model) wizardViewport() visual.Viewport {
	band := m.wizardBandData()
	if band.Total == 0 {
		return visual.Viewport{}
	}
	return visual.Viewport{Top: band.Top, Height: band.Visible, Total: band.Total}
}

// wizardCounter is the approved `first-last/total` position over the visible
// cards.
func (m *Model) wizardCounter() string {
	return m.wizardViewport().Counter()
}

// wizardHits maps every drawn card and, for a list step, the new-item input row
// back to the question index it stands for. base is the body line of the first
// drawn question row.
func (m *Model) wizardHits(base int) []bodyHit {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return nil
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	band := m.wizardBandData()
	if band.Total == 0 {
		return nil
	}
	hits := make([]bodyHit, 0, len(band.Spans)+1)
	cardBase := base + len(band.Fixed)
	for _, span := range band.Spans {
		if span.Rows <= 0 {
			continue
		}
		hits = append(hits, bodyHit{
			row: cardBase + span.Start, h: span.Rows,
			kind: hitWizardCard, index: span.Index,
		})
	}
	if step.List {
		hits = append(hits, bodyHit{
			row:  cardBase + len(band.Cards),
			kind: hitWizardInput,
		})
	}
	return hits
}

// wizardStepLines draws the current question: the pinned rows, the whole cards
// the window contains, the rows below them and the scroll hint when cards are
// outside the window.
func (m *Model) wizardStepLines() []string {
	band := m.wizardBandData()
	if band.Total == 0 {
		// No cards: the whole question is one body and scrolls as one band.
		screen := m.wizardStepScreen(m.wizardSpec.Steps[m.wizardStep])
		out := append([]string{}, screen.head...)
		out = append(out, screen.cards...)
		return append(out, screen.tail...)
	}
	out := append([]string{}, band.Fixed...)
	out = append(out, band.Cards...)
	out = append(out, band.Tail...)
	if hint := m.wizardViewport().ScrollHint(m.theme, m.bodyWidth()); hint != "" {
		out = append(out, hint)
	}
	return out
}

// wizardBandData is the single geometry pass for a question. The renderer, the
// cursor follow and the counter all read it, so the drawn cards and the
// counter cannot disagree.
func (m *Model) wizardBandData() wizardStepBandData {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return wizardStepBandData{}
	}
	screen := m.wizardStepScreen(m.wizardSpec.Steps[m.wizardStep])
	band := wizardStepBandData{Fixed: screen.head, Tail: screen.tail, Total: len(screen.ranges)}
	if band.Total == 0 {
		return band
	}
	rows := make([]int, band.Total)
	for i, r := range screen.ranges {
		rows[i] = r[1] - r[0]
	}
	available := 0
	unknownHeight := m.bodyBudget() <= 0
	if !unknownHeight {
		available = m.bodyBudget() - m.titleRows() - len(screen.head) - len(screen.tail)
	}
	if unknownHeight {
		// No WindowSizeMsg yet: draw every card. The next resize reflows the
		// same rows; guessing a small window would hide options needlessly.
		band.Top = 0
		band.Visible = band.Total
		for i, r := range screen.ranges {
			start := len(band.Cards)
			band.Cards = append(band.Cards, screen.cards[r[0]:r[1]]...)
			band.Spans = append(band.Spans, wizardCardSpan{Index: i, Start: start, Rows: len(band.Cards) - start})
		}
		return band
	}
	band.Top = m.cardTop(band.Total, rows, available)
	band.Visible = visibleCards(rows, band.Top, available)
	if band.Top+band.Visible < band.Total && available > 0 {
		// The hint gets its own row instead of covering a card.
		band.Visible = visibleCards(rows, band.Top, available-1)
	}
	band.Scrolls = band.Top > 0 || band.Top+band.Visible < band.Total
	for i := band.Top; i < band.Top+band.Visible; i++ {
		r := screen.ranges[i]
		start := len(band.Cards)
		band.Cards = append(band.Cards, screen.cards[r[0]:r[1]]...)
		band.Spans = append(band.Spans, wizardCardSpan{Index: i, Start: start, Rows: len(band.Cards) - start})
	}
	return band
}

// wizardCardSpan is one drawn card: its question index, the row it starts on
// inside Cards, and how many rows it covers. The mouse targets read the same
// spans the renderer drew.
type wizardCardSpan struct {
	Index int
	Start int
	Rows  int
}

// wizardStepBandData is one computed question layout: the pinned rows, the
// visible whole cards, the rows below, and the card window position.
type wizardStepBandData struct {
	Fixed   []string
	Cards   []string
	Tail    []string
	Spans   []wizardCardSpan
	Top     int
	Visible int
	Total   int
	Scrolls bool
}

// cardTop clamps the requested first visible card to a position where the
// window still shows whole cards.
func (m *Model) cardTop(total int, rows []int, available int) int {
	top := m.body.Top
	if top < 0 {
		top = 0
	}
	maxTop := total - visibleCards(rows, 0, available)
	if maxTop < 0 {
		maxTop = 0
	}
	if top > maxTop {
		top = maxTop
	}
	return top
}

// visibleCards is how many whole cards fit in height rows from card top.
func visibleCards(rows []int, top, height int) int {
	if height <= 0 {
		return 0
	}
	used, count := 0, 0
	for i := top; i < len(rows); i++ {
		if used+rows[i] > height {
			break
		}
		used += rows[i]
		count++
	}
	return count
}

// followWizardCursor scrolls the minimum distance that keeps the highlighted
// card visible, using the same geometry the frame draws.
func (m *Model) followWizardCursor() {
	rows := m.wizardCardRows()
	if len(rows) == 0 {
		return
	}
	viewport := m.wizardViewport()
	viewport.Follow(m.wizardChoice)
	m.body.Top = viewport.Top
}

// wizardCardRows returns the row height of every card on the current question.
func (m *Model) wizardCardRows() []int {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return nil
	}
	screen := m.wizardStepScreen(m.wizardSpec.Steps[m.wizardStep])
	rows := make([]int, len(screen.ranges))
	for i, r := range screen.ranges {
		rows[i] = r[1] - r[0]
	}
	return rows
}

// wizardAnswerLines lists the exact values the apply will send, in step order.
// A choice shows its label and, when the label is not the value, the machine
// value the feature receives; an explicitly empty optional answer says so.
func (m *Model) wizardAnswerLines(width int) []string {
	var out []string
	for _, step := range m.wizardSpec.Steps {
		if !wizardStepVisible(m.wizardAnswers, step) {
			continue
		}
		value, answered := m.wizardAnswers[step.Name]
		if !answered {
			continue
		}
		label := step.Title
		if strings.TrimSpace(label) == "" {
			label = step.Name
		}
		if step.List && strings.TrimSpace(value) != "" {
			out = append(out, m.wrapTo(label+":", width)...)
			for _, item := range strings.Split(value, "\n") {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, wrapWithPrefix(item, "* ", "  ", width, m.theme.Glyphs.Ell)...)
				}
			}
			continue
		}
		text := label + ": " + wizardAnswerDisplay(step, value)
		for _, line := range m.wrapTo(text, width) {
			out = append(out, line)
		}
	}
	return out
}

// wizardAnswerDisplay renders one collected answer for review. Machine values
// stay visible: the review must state what will be applied, not a friendlier
// paraphrase that could hide a wrong value.
func wizardAnswerDisplay(step core.WizardStep, value string) string {
	if strings.TrimSpace(value) == "" {
		return msgWizardAnswerEmpty
	}
	if len(step.Options) == 0 {
		return value
	}
	byValue := make(map[string]string, len(step.Options))
	for _, option := range step.Options {
		byValue[option.Value] = option.Label
	}
	var parts []string
	for _, part := range strings.Split(value, "\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label, ok := byValue[part]
		if !ok {
			parts = append(parts, part)
			continue
		}
		// A label that already names its machine value (`실행 시점
		// (runtime)`) is not repeated.
		if strings.Contains(label, part) {
			parts = append(parts, label)
			continue
		}
		parts = append(parts, label+" ("+part+")")
	}
	if len(parts) == 0 {
		return msgWizardAnswerEmpty
	}
	return strings.Join(parts, ", ")
}

// wizardPreviewBody is the preview panel's content: every answer that will be
// applied, the approvals the apply still needs, and the feature's own effect
// preview. The values and the approvals come first because they are what the
// reader must check; the effect prose below may be longer than the screen and
// scrolls.
func (m *Model) wizardPreviewBody(width int) []string {
	inner := width
	if inner > 4 {
		inner -= 4
	}
	var body []string
	body = append(body, m.wizardAnswerLines(inner)...)
	if kinds := m.previewApprovals(); len(kinds) > 0 {
		if len(body) > 0 {
			body = append(body, "")
		}
		first := kinds[0]
		line := fmt.Sprintf("%s: %s (%s)", msgWizardRequiredApprovals, first.KoreanLabel(), first)
		body = append(body, m.wrapTo(line, inner)...)
		// The remaining kinds follow under the same heading; repeating it
		// once per kind would read as a second list.
		for _, kind := range kinds[1:] {
			body = append(body, fmt.Sprintf("  %s (%s)", kind.KoreanLabel(), kind))
		}
	}
	if text := strings.TrimSpace(m.wizardPreview); text != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		body = append(body, m.wrapTo(text, inner)...)
	}
	return body
}

// wizardPreviewPanel frames the preview in the same titled panel the rest of
// the TUI uses. Compact mode keeps the content and drops the border. Before the
// first WindowSizeMsg the content is returned under its heading unwrapped, so a
// model built before its size arrives still shows the whole preview.
//
// A preview that failed is not an "applied values" panel: nothing would be
// applied, so the screen states the failure and the answers are not dressed up
// as a pending change.
func (m *Model) wizardPreviewPanel() []string {
	if m.wizardPreviewErr != nil {
		return m.wrapTo(m.wizardPreview, m.bodyWidth())
	}
	width := m.bodyWidth()
	if width <= 0 {
		out := []string{m.title(msgWizardAppliedTitle)}
		return append(out, m.wizardPreviewBody(0)...)
	}
	return visual.Panel(m.theme, msgWizardAppliedTitle, m.wizardPreviewBody(width), width, !m.compact())
}
