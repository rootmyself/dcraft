package tui

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// footerPhase maps the model's current interaction phase onto the approved
// keycap row. The five contract phases are used exactly as approved; two
// screens need keys the contract rows do not cover, so they supply their own
// rows (see footerItems): the service toggle list, and the wizard.
func (m *Model) footerPhase() visual.Phase {
	if m.form != nil {
		return visual.PhaseForm
	}
	if m.live != nil {
		return visual.PhaseLive
	}
	switch m.phase {
	case phaseConfirm:
		return visual.PhaseConfirm
	case phaseSelectingServices, phaseWizardLoading, phaseWizardStep, phaseWizardPreview, phaseLoadingInputs:
		return visual.PhaseForm
	case phaseLoading, phasePlanning:
		if m.active != nil && !m.active.Descriptor().ReadOnly {
			return visual.PhaseBusy
		}
	}
	return visual.PhaseMenu
}

// footerItems is the exact keycap row for the current screen. While the help
// panel is open it states the one key that screen has: closing the panel.
// There is exactly one keycap row per screen, so a panel never repeats one.
func (m *Model) footerItems() []visual.Keycap {
	if m.helpOpen {
		return []visual.Keycap{{Key: "esc", Label: msgHelpClose, Priority: 0}}
	}
	return m.screenKeycaps()
}

// screenKeycaps is the keycap row of the screen under any open panel. The
// approved phase rows apply verbatim except where a screen's keys are a strict
// superset that no approved row covers; those rows keep the approved chips and
// add only the keys that screen actually accepts.
func (m *Model) screenKeycaps() []visual.Keycap {
	if m.filtering {
		// While "/" has the keyboard the only two meanings are clear and
		// commit; q, r and the arrows are characters or menu moves here.
		return []visual.Keycap{
			{Key: "esc", Label: msgKeyClear, Priority: 0},
			{Key: "enter", Label: msgKeyCommit, Priority: 0},
		}
	}
	if m.live != nil && m.live.mode == core.LiveAttach {
		// Attach ends with ctrl+], never with ctrl+c (which is forwarded to
		// the child), so the live row would be wrong.
		return []visual.Keycap{
			{Key: "ctrl+]", Label: msgKeyDetach, Priority: 0},
			{Key: "?", Label: msgKeyHelp, Priority: 3},
		}
	}
	if m.live != nil && m.liveScrolls() {
		// A log stream owns the scroll keys when its output does not fit. The
		// approved live row keeps its mandatory chips and gains only the keys
		// this screen actually accepts.
		return []visual.Keycap{
			{Key: "q", Label: msgKeyCloseScreen, Priority: 0},
			{Key: "ctrl+c", Label: msgKeyStop, Priority: 1},
			{Key: "PgUp/PgDn", Label: msgKeyScroll, Priority: 2},
			{Key: "g/G", Label: msgKeyEnds, Priority: 3},
			{Key: "?", Label: msgKeyHelp, Priority: 3},
		}
	}
	if m.live == nil && (m.phase == phaseLoading || m.phase == phasePlanning) &&
		m.active != nil && m.active.Descriptor().ReadOnly {
		// A read-only operation in flight (not a live screen) accepts only
		// quit; the menu keys do not work until the result is ready, so the
		// row states just that. A mutating run returns PhaseBusy instead.
		return []visual.Keycap{
			{Key: "q", Label: msgKeyQuit, Priority: 0},
		}
	}
	if m.phase == phaseSelectingEnvironment {
		// The environment screen ignores "/", "r" and "tab"; advertising the
		// menu row here would name keys that do nothing.
		return []visual.Keycap{
			{Key: "↑↓", Label: msgKeySelect, Priority: 1},
			{Key: "enter", Label: msgKeyRun, Priority: 0},
			{Key: "q", Label: msgKeyQuit, Priority: 0},
			{Key: "?", Label: msgKeyHelp, Priority: 3},
		}
	}
	if m.phase == phaseSelectingServices {
		return []visual.Keycap{
			{Key: "↑↓", Label: msgKeyMove, Priority: 1},
			{Key: "space", Label: msgKeyToggle, Priority: 1},
			{Key: "enter", Label: msgKeyRun, Priority: 0},
			{Key: "esc", Label: msgKeyCancel, Priority: 0},
			{Key: "q", Label: msgKeyQuit, Priority: 0},
			{Key: "?", Label: msgKeyHelp, Priority: 3},
		}
	}
	if m.form != nil {
		// A text field accepts "?" as input, so the help key only exists on
		// choice fields. The footer states only keys that do something.
		items := visual.FooterItems(visual.PhaseForm)
		if m.formFocusIsText() {
			items = withoutHelp(items)
		}
		return items
	}
	if m.isWizardPhase() {
		// The wizard's keys are not the form's: it moves with arrows and goes
		// back with left, and tab does nothing. Advertising the form row here
		// would name a key that has no effect.
		if m.phase == phaseWizardPreview {
			keys := []visual.Keycap{
				{Key: "enter", Label: msgKeySave, Priority: 0},
				{Key: "left", Label: msgKeyPrev, Priority: 2},
				{Key: "esc", Label: msgKeyCancel, Priority: 0},
				{Key: "q", Label: msgKeyQuit, Priority: 0},
				{Key: "?", Label: msgKeyHelp, Priority: 3},
			}
			if m.wizardPreviewErr != nil {
				// Enter does nothing while the preview is invalid, so the row
				// must not offer it.
				keys = []visual.Keycap{
					{Key: "left", Label: msgKeyPrev, Priority: 0},
					{Key: "esc", Label: msgKeyCancel, Priority: 0},
					{Key: "q", Label: msgKeyQuit, Priority: 0},
					{Key: "?", Label: msgKeyHelp, Priority: 3},
				}
			}
			if m.bodyScrolls() {
				// A long preview scrolls instead of being cut; the keys that
				// move it must be on screen.
				keys = append(keys, visual.Keycap{Key: "PgUp/PgDn", Label: msgKeyScroll, Priority: 2})
			}
			return keys
		}
		items := []visual.Keycap{
			{Key: "↑↓", Label: msgKeySelect, Priority: 1},
			{Key: "enter", Label: msgKeyNext, Priority: 0},
			{Key: "left", Label: msgKeyPrev, Priority: 2},
			{Key: "esc", Label: msgKeyCancel, Priority: 0},
			{Key: "q", Label: msgKeyQuit, Priority: 0},
			{Key: "?", Label: msgKeyHelp, Priority: 3},
		}
		if m.wizardTextInput() && m.wizardSpec.Steps[m.wizardStep].List {
			esc := msgListCancel
			if m.wizardEditing {
				esc = msgListCancelEdit
			}
			return []visual.Keycap{{Key: "↑↓", Label: msgListSelect, Priority: 1}, {Key: "enter", Label: msgListEnter, Priority: 0}, {Key: "esc", Label: esc, Priority: 0}}
		}
		if m.wizardTextInput() {
			// A free-text step accepts "?" as a character and has no list to
			// move through; the row names only the keys that act here.
			items = []visual.Keycap{
				{Key: "enter", Label: msgKeyNext, Priority: 0},
				{Key: "left", Label: msgKeyPrev, Priority: 2},
				{Key: "esc", Label: msgKeyCancel, Priority: 0},
				{Key: "q", Label: msgKeyQuit, Priority: 0},
			}
			if m.wizardStepIsOptional() {
				items[0] = visual.Keycap{Key: "enter", Label: msgKeyNextSkip, Priority: 0}
			}
			return items
		}
		if m.wizardStepIsMulti() {
			// Space toggles one box; without the chip the row would only
			// advertise moving the cursor on a multi-select question.
			items = append([]visual.Keycap{
				{Key: "↑↓", Label: msgKeyMove, Priority: 1},
				{Key: "space", Label: msgKeyToggle, Priority: 1},
			}, items[1:]...)
		}
		return items
	}
	return m.resultScrollKeycaps(visual.FooterItems(m.footerPhase()))
}

// withoutHelp drops the help chip from a row for a screen that cannot open the
// overlay. It never mutates the input: the approval footer rows are shared.
func withoutHelp(items []visual.Keycap) []visual.Keycap {
	out := make([]visual.Keycap, 0, len(items))
	for _, item := range items {
		if item.Key == "?" {
			continue
		}
		out = append(out, item)
	}
	return out
}

// bodyWidth is the width the body renderers may use. In the wide layout the
// sidebar owns its cells and the approved one-cell gap; every other layout
// gives the body the whole width. The budget follows the pane that is actually
// drawn, so a hidden menu never costs the body its width.
func (m *Model) bodyWidth() int {
	if m.width <= 0 {
		return 0
	}
	if m.sidebarVisible() {
		return visual.ContentWidth(m.width, visual.ModeWide)
	}
	return visual.ContentWidth(m.width, visual.ModeMedium)
}

// layoutMode is the breakpoint decision for the current terminal size.
func (m *Model) layoutMode() visual.Mode {
	return visual.PickMode(m.width, m.height)
}

// sidebarVisible reports whether the persistent left pane is drawn. A menu of
// one feature is not worth a pane, and the body then keeps its full width.
func (m *Model) sidebarVisible() bool {
	return m.width > 0 && m.layoutMode() == visual.ModeWide && len(m.features) > 1
}

// compact reports whether decoration must be dropped. Before the first
// WindowSizeMsg the size is unknown, so the screen renders its full content
// rather than guessing that it is small.
func (m *Model) compact() bool {
	return m.width > 0 && m.layoutMode() == visual.ModeCompact
}

// bodyLayout is one composed body: the rows in reading order plus the viewport
// that describes the scrollable band (when there is one).
type bodyLayout struct {
	lines    []string
	viewport visual.Viewport
	scrolls  bool
}

// bandGeometry is the one row-accounting decision for a screen with a fixed
// head, a scrolling band and a tail. headerRows is every row drawn above the
// screen (navigation row, title block, kept-output panel), so the band is never
// measured against rows the frame also draws.
type bandGeometry struct {
	// tail is the guidance below the band after the rescue trim.
	tail     []string
	viewport visual.Viewport
	scrolls  bool
	// whole is true when nothing could be pinned: the fixed rows, the band and
	// the tail scroll together so no row is unreachable.
	whole bool
	// table is true for a pinned table band (a fixed header above scrolling
	// rows). Only a table band gets the approved scrollbar gutter; a plain body
	// states its position in the scroll hint instead.
	table bool
}

// renderBody builds every body line: the screen title, the phase content, the
// feature menu and the notice. It never draws the header or the keycap footer;
// those belong to the shell.
//
// A long body scrolls instead of being cut. The table's summary rows and header
// stay fixed, its rows scroll, and the guidance below stays put; any other body
// scrolls as one band. One row is reserved for the scroll hint only when the
// band actually overflows.
func (m *Model) renderBody() []string {
	return m.layoutBody().lines
}

// layoutBody is the single layout pass. renderBody, bodyScrolls,
// currentViewport, the title counter and the key handler all read its result,
// so the drawn rows, the hint and the keys cannot disagree about whether the
// screen scrolls.
func (m *Model) layoutBody() bodyLayout {
	if m.live != nil {
		// The live screen is its own layout: a fixed header and one scrolling
		// output band. It must not borrow the result screen's pinned table.
		return m.liveLayout()
	}
	switch m.phase {
	case phaseReady:
		return m.resultBody()
	case phaseSelectingServices:
		return m.serviceBody()
	case phaseWizardStep:
		return m.wizardBody()
	default:
		return m.wholeBody()
	}
}

// navRowCount is how many body rows the navigation summary occupies. The wide
// layout draws a whole sidebar instead and the summary is one row everywhere
// else, so this is exactly 0 or 1.
func (m *Model) navRowCount() int {
	if m.navLine() == "" {
		return 0
	}
	return 1
}

// wholeBody is a screen whose content is one band: the navigation row, the
// title block, the phase body and the notice scroll together.
func (m *Model) wholeBody() bodyLayout {
	var lines []string
	add := func(s string) {
		s = strings.TrimRight(s, "\n")
		if s == "" {
			return
		}
		lines = append(lines, strings.Split(s, "\n")...)
	}
	if navLine := m.navLine(); navLine != "" {
		add(navLine)
	}
	if title := m.screenTitle(); title != "" {
		lines = append(lines, m.titleRow(title, ""), "")
	}
	add(m.phaseBody())
	if m.notice != "" {
		add(m.noticeText(m.notice))
	}
	return m.fitWhole(lines)
}

// resultBody is the result screen: the navigation row, the title block with the
// position counter, the kept output panel, the table band and the guidance.
func (m *Model) resultBody() bodyLayout {
	lay := m.resultGeometry()
	var lines []string
	if navLine := m.navLine(); navLine != "" {
		lines = append(lines, navLine)
	}
	title := m.screenTitle()
	if m.detailOpen {
		title = msgDetailTitle
	}
	if title != "" {
		counter := ""
		if lay.scrolls {
			counter = lay.viewport.Counter()
		}
		lines = append(lines, m.titleRow(title, counter), "")
	}
	lines = append(lines, lay.panel...)
	if !m.resultDismissed {
		// The status table is one of the two scrolling content lists the
		// contract gives a scrollbar; the detail view is a plain band and
		// states its position in the hint alone.
		lines = append(lines, m.drawBand(lay.screen, lay.band, lay.bar)...)
	}

	// The full list is drawn wherever the navigation is not already a list:
	// the wide layout has the persistent sidebar. The medium layout keeps the
	// one-row summary as a headline *and* the list, because a collapsed row
	// that names only one feature would hide the rest of the menu. Before the
	// first WindowSizeMsg the sidebar is not drawn, so the list is.
	//
	// The list is also hidden while the result scrolls: two competing scroll
	// lists would fight for the same keys. esc puts the result away and brings
	// the menu back, so the feature list is never unreachable.
	if len(m.features) > 1 && !m.sidebarVisible() && !m.menuHidden() && !m.resultDismissed {
		lines = append(lines, m.sectionLabel(msgMenu))
		budget := 0
		if m.height > 0 {
			// Everything the body has left after the lines already written.
			budget = visual.BodyRows(m.height) - len(lines)
		}
		lines = append(lines, strings.Split(strings.TrimRight(m.menuView(budget), "\n"), "\n")...)
	}
	return bodyLayout{
		lines:    trimTrailingBlank(lines),
		viewport: lay.viewport,
		scrolls:  lay.scrolls,
	}
}

// resultLayout is the resolved result body: the screen rows at the width the
// frame draws, the kept-output panel, the band geometry and whether the
// approved scrollbar gutter is painted.
type resultLayout struct {
	screen   screenLines
	panel    []string
	band     bandGeometry
	viewport visual.Viewport
	scrolls  bool
	// bar reports that the one-cell scrollbar gutter owns the last table cell.
	bar bool
}

// resultGeometry is the single result layout decision. It narrows only the
// scrolling table when the band overflows: the approved one-cell scrollbar
// gutter takes that cell, so the thumb never covers a value.
func (m *Model) resultGeometry() resultLayout {
	lay := m.resultGeometryWith(false)
	if lay.scrolls && lay.band.table && !lay.band.whole {
		narrow := m.resultGeometryWith(true)
		if narrow.scrolls && narrow.band.table && !narrow.band.whole {
			narrow.bar = true
			return narrow
		}
	}
	return lay
}

// resultGeometryWith computes the result layout with an explicit scrollbar
// decision.
func (m *Model) resultGeometryWith(bar bool) resultLayout {
	var panel []string
	if m.lastLive != nil {
		panel = m.liveTailLines(m.titleRows() + minResultRows)
	}
	screen := screenLines{}
	if !m.resultDismissed {
		screen = m.resultScreenWith(bar)
		// A screen notice is part of the body, so it is counted against the
		// same budget instead of being appended past it and clipped.
		if m.notice != "" {
			screen.Tail = append(screen.Tail, "", m.noticeText(m.notice))
		}
	}
	headerRows := m.navRowCount() + m.titleRows() + len(panel)
	band := m.fitBand(headerRows, screen)
	return resultLayout{
		screen:   screen,
		panel:    panel,
		band:     band,
		viewport: band.viewport,
		scrolls:  band.scrolls,
	}
}

// serviceBody is the service scope screen: the title block, the pinned heading
// and whole-stack row, the scrolling service rows and the guidance.
func (m *Model) serviceBody() bodyLayout {
	geom := m.serviceGeometry()
	var lines []string
	if navLine := m.navLine(); navLine != "" {
		lines = append(lines, navLine)
	}
	if title := m.screenTitle(); title != "" {
		counter := ""
		if geom.scrolls {
			counter = geom.viewport.Counter()
		}
		lines = append(lines, m.titleRow(title, counter), "")
	}
	lines = append(lines, m.drawBand(m.serviceScreen(), geom, false)...)
	if m.notice != "" {
		lines = append(lines, "", m.noticeText(m.notice))
	}
	return bodyLayout{
		lines:    trimTrailingBlank(lines),
		viewport: geom.viewport,
		scrolls:  geom.scrolls,
	}
}

// serviceGeometry is the one row-accounting decision for the scope screen.
func (m *Model) serviceGeometry() bandGeometry {
	return m.fitBand(m.navRowCount()+m.titleRows(), m.serviceScreen())
}

// wizardBody is a guided question: the head and tail are pinned and the option
// cards scroll as whole cards. wizardBandData is that one geometry.
func (m *Model) wizardBody() bodyLayout {
	var lines []string
	if navLine := m.navLine(); navLine != "" {
		lines = append(lines, navLine)
	}
	if title := m.screenTitle(); title != "" {
		counter := ""
		if m.wizardStepScrolls() {
			counter = m.wizardCounter()
		}
		lines = append(lines, m.titleRow(title, counter), "")
	}
	lines = append(lines, m.wizardStepLines()...)
	if m.notice != "" {
		lines = append(lines, "", m.noticeText(m.notice))
	}
	return bodyLayout{
		lines:    trimTrailingBlank(lines),
		viewport: m.wizardViewport(),
		scrolls:  m.wizardStepScrolls(),
	}
}

// fitWhole scrolls a whole body that does not fit, and returns it unchanged when
// it does. It never cuts rows without a way to reach them.
func (m *Model) fitWhole(lines []string) bodyLayout {
	budget := m.bodyBudget()
	if budget <= 0 || len(lines) <= budget {
		return bodyLayout{lines: lines}
	}
	viewport := fitViewport(visual.Viewport{Top: m.body.Top, Height: budget, Total: len(lines)}, budget)
	start, end := viewport.Window()
	out := append([]string{}, lines[start:end]...)
	if hint := viewport.ScrollHint(m.theme, m.bodyWidth()); hint != "" {
		out = append(out, hint)
	}
	return bodyLayout{lines: out, viewport: viewport, scrolls: true}
}

// minTableRows is the smallest number of table rows worth pinning a header for.
// Below this the guidance below the table gives its rows away first.
const minTableRows = 3

// fitBand computes the band geometry. headerRows already counts every row the
// frame draws above the screen; only the band and the tail are still to place.
//
// When the pinned tail would leave the table too short to read, the blank rows
// at the front of the guidance give their space back. If that is still not
// enough, nothing is pinned: the fixed rows, the table and the guidance scroll
// together, so scroll reaches every row the counter names. No row is dropped.
//
// The returned viewport always describes the rows the frame draws, so the
// counter, the hint and the keys read one computation instead of guessing.
func (m *Model) fitBand(headerRows int, screen screenLines) bandGeometry {
	// A pinned table band has a fixed header above scrolling rows and guidance
	// below them. That shape is what the approved scrollbar gutter belongs to.
	table := len(screen.Fixed) > 0 && len(screen.Tail) > 0
	tail := screen.Tail
	budget := m.bodyBudget()
	if budget <= 0 {
		// Unknown height: draw everything. The next WindowSizeMsg reflows it.
		return bandGeometry{tail: tail, viewport: wholeBandViewport(len(screen.Band)), table: table}
	}
	if used := headerRows + len(screen.Fixed) + len(screen.Band) + len(tail); used <= budget {
		return bandGeometry{tail: tail, viewport: wholeBandViewport(len(screen.Band)), table: table}
	}
	available := budget - headerRows - len(screen.Fixed) - len(tail)
	if available < minTableRows && len(screen.Band) > minTableRows && len(tail) > 0 {
		// A table reduced to a single row is not a table, so the blank rows in
		// front of the guidance yield first. Dropping text would make it
		// unreachable; dropping a separator costs nothing.
		give := minTableRows - available
		trimmed := 0
		for trimmed < give && trimmed < len(tail) && strings.TrimSpace(tail[trimmed]) == "" {
			trimmed++
		}
		if trimmed == give {
			tail = tail[trimmed:]
			available += trimmed
		} else {
			available = 0
		}
	}
	if available <= 0 {
		total := len(screen.Fixed) + len(screen.Band) + len(tail)
		height := budget - headerRows
		viewport := fitViewport(visual.Viewport{Top: m.body.Top, Height: height, Total: total}, height)
		return bandGeometry{tail: tail, viewport: viewport, scrolls: viewport.Overflow(), whole: true, table: table}
	}
	viewport := fitViewport(visual.Viewport{Top: m.body.Top, Height: available, Total: len(screen.Band)}, available)
	return bandGeometry{tail: tail, viewport: viewport, scrolls: viewport.Overflow(), table: table}
}

// wholeBandViewport is the window over every band row: what a band that fits
// looks like to the counter and the keys.
func wholeBandViewport(total int) visual.Viewport {
	return visual.Viewport{Top: 0, Height: total, Total: total}
}

// drawBand draws the fixed rows, the visible band rows with the scroll hint and
// the tail, exactly as fitBand measured them. bar paints the approved scrollbar
// gutter; the status table and the log viewport are the two scrolling content
// lists the contract names.
func (m *Model) drawBand(screen screenLines, g bandGeometry, bar bool) []string {
	rows := screen.Band
	prefix := screen.Fixed
	if g.whole {
		rows = append(append([]string{}, screen.Fixed...), screen.Band...)
		rows = append(rows, g.tail...)
		prefix = nil
	}
	out := make([]string, 0, len(prefix)+g.viewport.Visible()+len(g.tail)+1)
	out = append(out, prefix...)
	start, end := g.viewport.Window()
	if start > len(rows) {
		start = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	drawn := append([]string{}, rows[start:end]...)
	if bar {
		drawn = m.attachScrollbar(drawn, g)
	}
	out = append(out, drawn...)
	if hint := g.viewport.ScrollHint(m.theme, m.bodyWidth()); hint != "" {
		out = append(out, hint)
	}
	if !g.whole {
		out = append(out, g.tail...)
	}
	return out
}

// attachScrollbar paints the approved one-cell gutter when the band overflows.
// The table was laid out one cell narrower when the gutter was reserved, so the
// cell is free and the thumb never covers a value.
func (m *Model) attachScrollbar(rows []string, g bandGeometry) []string {
	if !g.table || g.whole || !g.scrolls || !g.viewport.Overflow() {
		return rows
	}
	gutter := visual.Scrollbar(m.theme, len(rows), g.viewport.Top, g.viewport.Visible(), g.viewport.Total)
	if len(gutter) == 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, line := range rows {
		out[i] = visual.Fit(line, m.bodyWidth()-1, m.theme.Glyphs.Ell) + gutter[i]
	}
	return out
}

// bodyBudget is how many body rows the frame has left after the header, the
// footer and the pinned lines. A size of zero means no WindowSizeMsg has
// arrived yet: the body is unbounded then, and the frame keeps everything.
func (m *Model) bodyBudget() int {
	if m.height <= 0 {
		return 0
	}
	rows := visual.BodyRows(m.height)
	if rows < 1 {
		rows = 1
	}
	return rows
}

// resultScrolls reports whether the result body needs to scroll.
func (m *Model) resultScrolls() bool {
	return m.resultGeometry().scrolls
}

// bodyScrolls reports whether the current screen scrolls. It drives the footer,
// the key handler and the scroll hint, so it is the layout pass's own answer.
func (m *Model) bodyScrolls() bool {
	return m.layoutBody().scrolls
}

// currentViewport is the scroll window of the current screen, taken from the
// same layout pass that draws it.
func (m *Model) currentViewport() visual.Viewport {
	return m.layoutBody().viewport
}

// titleRows is the number of rows the title block occupies.
func (m *Model) titleRows() int {
	title := m.screenTitle()
	if m.detailOpen {
		title = msgDetailTitle
	}
	if title == "" {
		return 0
	}
	return 2 // title + blank separator
}

// titleRow is the screen title with the scroll counter right-aligned on the same
// row, exactly as the approved status screen shows `6-16/16`. The counter is
// empty when nothing scrolls, so a short screen keeps its title alone.
func (m *Model) titleRow(title, counter string) string {
	styled := m.title(title)
	if counter == "" {
		return styled
	}
	width := m.bodyWidth()
	if width <= 0 {
		return styled + "  " + m.hintText(counter)
	}
	gap := width - visual.Width(title) - visual.Width(counter)
	if gap < 1 {
		gap = 1
	}
	return styled + strings.Repeat(" ", gap) + m.hintText(counter)
}

// trimTrailingBlank drops trailing empty rows so the frame pads the rest.
func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// menuHidden reports whether the result sheet occupies the whole body. A long
// result hides the menu until it is dismissed, because both scroll and both
// would fight for the same keys.
func (m *Model) menuHidden() bool {
	if m.detailOpen {
		return true
	}
	if m.resultDismissed {
		return false
	}
	return m.resultScrolls()
}

// screenTitle is the current feature's title. The stack identity lives in the
// header, so the body does not repeat "dcraft".
func (m *Model) screenTitle() string {
	shown := m.active
	if (m.phase == phaseLoadingServices || m.phase == phaseSelectingServices) && m.serviceFeature != nil {
		shown = m.serviceFeature
	}
	if m.confirmOpen() {
		// The panel states the action; the background keeps the stack
		// identity without repeating it.
		shown = nil
	}
	if m.live != nil && m.active != nil {
		shown = m.active
	}
	if shown == nil {
		return ""
	}
	return shown.Descriptor().Title
}

// phaseBody renders the content of the current phase. Screen content is
// unchanged by T2; only the surrounding frame is new.
func (m *Model) phaseBody() string {
	switch m.phase {
	case phaseNoPrimary:
		return msgNoPrimary
	case phasePlanning:
		return m.workingBody(msgPlanning)
	case phaseConfirm:
		// The confirmation is a composed panel, not a body screen: the body
		// under it is the dimmed background.
		return ""
	case phaseLoadingEnvironments:
		return msgEnvChecking
	case phaseSelectingEnvironment:
		return m.environmentView()
	case phaseLoadingServices:
		return msgServiceLoading
	case phaseSelectingServices:
		return m.serviceView()
	case phaseLoadingInputs:
		return msgLoadingInputs
	case phaseForm:
		return m.formView()
	case phaseWizardLoading, phaseWizardStep, phaseWizardPreview:
		return m.wizardView()
	case phaseLoading:
		// A live screen is laid out as a body (fixed header plus a scrolling
		// band) by layoutBody; phaseBody only handles non-live work.
		return m.workingBody(msgWorking)
	default:
		// phaseReady has no single-string body: its document is rendered as
		// lines by renderBody so the table can scroll under a fixed header.
		return ""
	}
}

// environmentView lists named environments; nothing is preselected.
func (m *Model) environmentView() string {
	var b strings.Builder
	b.WriteString(m.title(msgEnvSelectTitle))
	b.WriteString("\n")
	b.WriteString(msgEnvSelectIntro)
	b.WriteString("\n")
	for i, env := range m.envs {
		marker := "  "
		if i == m.envSelected {
			marker = "> "
		}
		b.WriteString(marker + env.Name + " — " + environmentSummary(env) + "\n")
	}
	b.WriteString(m.hintText(msgEnvIsolationNote))
	b.WriteString("\n")
	return b.String()
}

// minResultRows is the floor the kept-output panel must leave for the result it
// follows: enough for the title block, the outcome line and the scroll hint. A
// panel that fills the frame would hide the event the reader came to read.
const minResultRows = 6

// liveTailLines is the kept live output shown on the management screen. The
// content is raw process output and is passed through unchanged; only the frame
// around it is the TUI's. It is drawn as a titled panel so the retained output
// reads as one block with the rest of the screen instead of a bare list.
//
// reserve is how many body rows the screen below the panel still needs. The
// panel keeps what is left, states the lines it had to leave out, and never
// grows past that budget.
func (m *Model) liveTailLines(reserve int) []string {
	if len(m.lastLive) == 0 {
		return nil
	}
	lines := m.lastLive
	title := msgLastOutput

	budget := m.bodyBudget()
	if budget > 0 {
		decorated := !m.compact()
		chrome := 1 // the blank row after the panel
		if decorated {
			chrome += 2 // top and bottom border rows
		}
		limit := budget - reserve - chrome
		if limit < 1 {
			limit = 1
		}
		if len(lines) > limit {
			omitted := len(lines) - limit
			lines = lines[omitted:]
			title = fmt.Sprintf(msgLastOutputOmitted, omitted)
		}
	}

	panel := visual.Panel(m.theme, title, lines, m.bodyWidth(), !m.compact())
	return append(panel, "")
}

// stageGlyph is the themed status symbol for one stage. The Korean label is
// always drawn next to it, so color and icon are never the only channel.
func stageGlyph(th visual.Theme, status core.StageStatus) string {
	switch status {
	case core.StageSucceeded:
		return th.Glyphs.OK
	case core.StageFailed:
		return th.Glyphs.Danger
	case core.StageCanceled:
		return th.Glyphs.Stopped
	case core.StageStarted:
		return th.Glyphs.Spin[0]
	default:
		return th.Glyphs.Dot
	}
}

// Shell-level text styles. Every renderer gets them from the injected theme;
// none reads the environment or a package-level style.

func (m *Model) title(s string) string {
	return m.theme.Bold(visual.TokenAccent, s)
}

func (m *Model) sectionLabel(s string) string {
	return m.theme.Bold(visual.TokenMuted, s)
}

func (m *Model) hintText(s string) string {
	return m.theme.Paint(visual.TokenMuted, s)
}

func (m *Model) noticeText(s string) string {
	return m.theme.Bold(visual.TokenWarning, s)
}
