package tui

import (
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
			{Key: "esc", Label: "해제", Priority: 0},
			{Key: "enter", Label: "확정", Priority: 0},
		}
	}
	if m.live != nil && m.live.mode == core.LiveAttach {
		// Attach ends with ctrl+], never with ctrl+c (which is forwarded to
		// the child), so the live row would be wrong.
		return []visual.Keycap{
			{Key: "ctrl+]", Label: "접속 종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
		}
	}
	if m.live == nil && (m.phase == phaseLoading || m.phase == phasePlanning) &&
		m.active != nil && m.active.Descriptor().ReadOnly {
		// A read-only operation in flight (not a live screen) accepts only
		// quit; the menu keys do not work until the result is ready, so the
		// row states just that. A mutating run returns PhaseBusy instead.
		return []visual.Keycap{
			{Key: "q", Label: "종료", Priority: 0},
		}
	}
	if m.phase == phaseSelectingEnvironment {
		// The environment screen ignores "/", "r" and "tab"; advertising the
		// menu row here would name keys that do nothing.
		return []visual.Keycap{
			{Key: "↑↓", Label: "선택", Priority: 1},
			{Key: "enter", Label: "실행", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
		}
	}
	if m.phase == phaseSelectingServices {
		return []visual.Keycap{
			{Key: "↑↓", Label: "이동", Priority: 1},
			{Key: "space", Label: "선택/해제", Priority: 1},
			{Key: "enter", Label: "실행", Priority: 0},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
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
			if m.wizardPreviewErr != nil {
				// Enter does nothing while the preview is invalid, so the row
				// must not offer it.
				return []visual.Keycap{
					{Key: "left", Label: "이전", Priority: 0},
					{Key: "esc", Label: "취소", Priority: 0},
					{Key: "q", Label: "종료", Priority: 0},
					{Key: "?", Label: "도움말", Priority: 3},
				}
			}
			return []visual.Keycap{
				{Key: "enter", Label: "저장", Priority: 0},
				{Key: "left", Label: "이전", Priority: 2},
				{Key: "esc", Label: "취소", Priority: 0},
				{Key: "q", Label: "종료", Priority: 0},
				{Key: "?", Label: "도움말", Priority: 3},
			}
		}
		items := []visual.Keycap{
			{Key: "↑↓", Label: "선택", Priority: 1},
			{Key: "enter", Label: "다음", Priority: 0},
			{Key: "left", Label: "이전", Priority: 2},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
		}
		if m.wizardTextInput() {
			// A free-text step accepts "?" as a character.
			items = withoutHelp(items)
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

// layoutBody is the single layout pass. renderBody, bodyScrolls and the counter
// all read the same inputs, so the drawn rows and the counter cannot disagree
// about whether the screen scrolls.
func (m *Model) layoutBody() bodyLayout {
	title := m.screenTitle()
	if m.detailOpen {
		title = msgDetailTitle
	}
	counter := ""
	if m.bodyScrolls() {
		counter = m.scrollCounter()
	}

	var lines []string
	add := func(s string) {
		s = strings.TrimRight(s, "\n")
		if s == "" {
			return
		}
		lines = append(lines, strings.Split(s, "\n")...)
	}

	// The medium layout states the navigation in exactly one row directly
	// under the header. It is one row collapsed and expanded, so opening the
	// menu never costs the content a line.
	if navLine := m.navLine(); navLine != "" {
		add(navLine)
	}

	if title != "" {
		// The separator is an explicit row: titleRows counts it, so dropping
		// it would let the frame draw one row more than the budget.
		lines = append(lines, m.titleRow(title, counter), "")
	}

	if m.phase != phaseReady {
		add(m.phaseBody())
		if m.notice != "" {
			add("")
			add(m.noticeText(m.notice))
		}
		return m.fitWhole(lines)
	}

	if !m.resultDismissed {
		screen := m.resultScreen()
		lines = append(lines, m.liveTailLines()...)
		lines = append(lines, screen.Fixed...)
		out := m.fitBand(lines, screen)
		lines = append(lines, out...)
	} else if tail := m.liveTailLines(); len(tail) > 0 {
		lines = append(lines, tail...)
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
	if len(m.features) > 1 && m.phase == phaseReady && !m.sidebarVisible() && !m.menuHidden() {
		add("")
		add(m.sectionLabel(msgMenu))
		budget := 0
		if m.height > 0 {
			// Everything the body has left after the lines already written.
			budget = visual.BodyRows(m.height) - len(lines)
		}
		add(m.menuView(budget))
	}
	if m.notice != "" {
		add("")
		add(m.noticeText(m.notice))
	}
	return bodyLayout{lines: trimTrailingBlank(lines)}
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

// fitBand draws the visible band rows, the hint and the tail. placed already
// contains the title block and the fixed rows; only the band and the tail are
// still to place. When the fixed rows and the tail alone do not fit, nothing can
// be pinned: the whole result becomes one band so no row is unreachable.
func (m *Model) fitBand(placed []string, screen screenLines) []string {
	budget := m.bodyBudget()
	if budget <= 0 {
		// Unknown height: draw everything. The next WindowSizeMsg reflows it.
		m.storeScroll(visual.Viewport{})
		out := append([]string{}, screen.Band...)
		return append(out, screen.Tail...)
	}
	if total := len(placed) + len(screen.Band) + len(screen.Tail); total <= budget {
		m.storeScroll(visual.Viewport{})
		out := append([]string{}, screen.Band...)
		return append(out, screen.Tail...)
	}
	available := budget - len(placed) - len(screen.Tail)
	if available < minTableRows && len(screen.Band) > minTableRows && len(screen.Tail) > 0 {
		// A table reduced to a single row is not a table. The guidance below
		// yields rows first, and is reachable by scrolling the whole body.
		short := minTableRows - available
		if short > len(screen.Tail) {
			short = len(screen.Tail)
		}
		screen.Tail = screen.Tail[short:]
		available += short
	}
	if available <= 0 {
		all := screen.lines()
		viewport := fitViewport(visual.Viewport{Top: m.body.Top, Height: budget, Total: len(all)}, budget)
		start, end := viewport.Window()
		out := append([]string{}, all[start:end]...)
		if hint := viewport.ScrollHint(m.theme, m.bodyWidth()); hint != "" {
			out = append(out, hint)
		}
		m.storeScroll(viewport)
		return out
	}
	viewport := fitViewport(visual.Viewport{Top: m.body.Top, Height: available, Total: len(screen.Band)}, available)
	m.storeScroll(viewport)
	start, end := viewport.Window()
	out := make([]string, 0, end-start+1)
	for i := start; i < end && i < len(screen.Band); i++ {
		out = append(out, screen.Band[i])
	}
	if hint := viewport.ScrollHint(m.theme, m.bodyWidth()); hint != "" {
		out = append(out, hint)
	}
	return append(out, screen.Tail...)
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

// resultScrolls reports whether the result body needs to scroll. It compares
// against the rows the result actually gets: the title block, the tail and the
// hint row all reduce the band, so comparing the raw body against the whole
// budget under-reports and would leave rows unreachable.
func (m *Model) resultScrolls() bool {
	budget := m.bodyBudget()
	if budget <= 0 {
		return false
	}
	screen := m.resultScreen()
	// Rows the result itself must place: title block, summary, table header
	// and the guidance below. The hint row comes out of the same budget.
	used := m.titleRows() + len(screen.Fixed) + len(screen.Band) + len(screen.Tail)
	return used > budget
}

// bodyScrolls reports whether the current screen scrolls. It drives the footer
// and the key handler, so a screen that fits never advertises scrolling.
func (m *Model) bodyScrolls() bool {
	budget := m.bodyBudget()
	if budget <= 0 {
		return false
	}
	if m.phase == phaseReady {
		return m.resultScrolls()
	}
	total := len(strings.Split(strings.TrimRight(m.phaseBody(), "\n"), "\n"))
	return total > budget
}

// currentViewport is the scroll window for the current screen, derived from the
// model instead of from the last drawn frame. The keys and the counter both use
// it, so a key press works even before the first frame is drawn and can never
// disagree with the counter.
//
// A result screen pins its summary rows and the table header; other screens have
// no band of their own and report the whole body.
func (m *Model) currentViewport() visual.Viewport {
	budget := m.bodyBudget()
	if budget <= 0 {
		return visual.Viewport{}
	}
	if m.phase != phaseReady {
		lines := strings.Split(strings.TrimRight(m.phaseBody(), "\n"), "\n")
		return fitViewport(visual.Viewport{Top: m.body.Top, Height: budget, Total: len(lines)}, budget)
	}
	screen := m.resultScreen()
	available := budget - m.titleRows() - len(screen.Fixed) - len(screen.Tail)
	if available <= 0 {
		return visual.Viewport{}
	}
	return fitViewport(visual.Viewport{Top: m.body.Top, Height: available, Total: len(screen.Band)}, available)
}

// scrollCounter is the `first-last/total` position of the table rows, computed
// from the same viewport the frame draws — including the row the hint costs, so
// the counter can never describe a row that the hint pushed off screen.
func (m *Model) scrollCounter() string {
	if m.phase != phaseReady || !m.resultScrolls() {
		return ""
	}
	return m.currentViewport().Counter()
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
		if m.live != nil {
			return m.liveView()
		}
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

// liveTailLines is the kept live output shown above a result. It is raw process
// output and is passed through unchanged.
func (m *Model) liveTailLines() []string {
	if len(m.lastLive) == 0 {
		return nil
	}
	out := []string{m.title(msgLastOutput)}
	out = append(out, m.lastLive...)
	out = append(out, "")
	return out
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

// wrapProse wraps a paragraph to the body width. Prose may wrap; paths and
// identifiers may not. The CLI paths pass width 0, which means "no wrapping".
func (m *Model) wrapProse(text string) string {
	width := m.bodyWidth()
	if width <= 0 || strings.TrimSpace(text) == "" {
		return text
	}
	return strings.Join(visual.Wrap(text, width), "\n")
}
