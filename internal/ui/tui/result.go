package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// screenLines is the full body for the current screen plus the boundaries of the
// scrollable region. The summary rows above a table and its header stay fixed,
// the rows scroll, and the guidance below stays put. A screen without a table
// has nothing to pin, so it all scrolls as one band.
type screenLines struct {
	Fixed []string
	Band  []string
	Tail  []string
}

// lines flattens the screen in reading order.
func (s screenLines) lines() []string {
	out := make([]string, 0, len(s.Fixed)+len(s.Band)+len(s.Tail))
	out = append(out, s.Fixed...)
	out = append(out, s.Band...)
	out = append(out, s.Tail...)
	return out
}

// detailEntries lists everything the current result body had to truncate.
func (m *Model) detailEntries() []visual.DetailEntry {
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		return nil
	}
	doc := m.resultDocument()
	doc.Title = ""
	return visual.DocumentDetails(doc, m.bodyWidth())
}

// hasDetail reports whether the full-value view would show anything.
func (m *Model) hasDetail() bool { return len(m.detailEntries()) > 0 }

// detailLines is the full-value body when the detail view is open.
func (m *Model) detailLines() []string {
	return visual.RenderDetails(m.theme, m.detailEntries(), m.bodyWidth())
}

// resultScreen is the result body split into fixed rows (the summary and the
// table header), the scrollable rows, and the guidance below.
func (m *Model) resultScreen() screenLines {
	if m.result == nil {
		return screenLines{}
	}
	if m.detailOpen {
		return screenLines{Band: m.detailLines()}
	}
	if m.result.Outcome != core.OutcomeSuccess {
		// The outcome, target, operation ID and diagnostics are the shared
		// Korean text the CLI prints. The stage rows are the TUI's own
		// timeline, so a failed stage stays next to the stages that never
		// ran instead of disappearing with the failure summary.
		lines := strings.Split(view.FailureText(*m.result), "\n")
		if stages := m.timelineView(m.result.Stages, m.planStages); len(stages) > 0 {
			lines = append(lines, "", m.sectionLabel(msgStageTimeline))
			lines = append(lines, stages...)
		}
		return screenLines{Band: lines}
	}

	doc := m.resultDocument()
	doc.Title = ""
	layout := visual.RenderDocumentLayoutOptions(m.theme, doc, m.bodyWidth(), visual.RenderOptions{
		Compact: m.compact(),
	})
	out := screenLines{
		Fixed: layout.Fixed(),
		Band:  layout.Band(),
		Tail:  layout.Tail(),
	}
	if warnings := view.Warnings(m.result.Diagnostics); len(warnings) > 0 {
		out.Tail = append(out.Tail, "")
		out.Tail = append(out.Tail, strings.Split(view.Diagnostics(warnings), "\n")...)
	}
	return out
}

// storeScroll records the position actually drawn so the next frame continues
// from there instead of a stale request.
func (m *Model) storeScroll(viewport visual.Viewport) {
	if m.detailOpen {
		m.detailScroll = viewport
		return
	}
	m.body = viewport
}

// fitViewport reserves one row for the scroll hint when the band overflows, so
// the hint gets its own row instead of covering the last row.
func fitViewport(v visual.Viewport, available int) visual.Viewport {
	if available < 0 {
		available = 0
	}
	v.Height = available
	if v.Total > available && available > 0 {
		v.Height = available - 1
	}
	return v.Clamp()
}

// scrollKey handles the viewport keys. Arrows are deliberately not handled here:
// on the menu they move the selection, and taking them over would break muscle
// memory. PgUp/PgDn, Home/End and g/G have no other meaning on these screens.
func (m *Model) scrollKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.detailOpen {
		return true, m.detailKey(msg)
	}
	// The scope table follows its selection: PgUp/PgDn move the cursor there,
	// and a second scroll position would let the two disagree.
	if m.phase == phaseSelectingServices {
		return false, nil
	}
	// A free-text wizard answer accepts "g" as a character. Scrolling must not
	// steal a letter from the field the user is typing in.
	if m.wizardTextInput() {
		return false, nil
	}
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "g", "G":
		if !m.bodyScrolls() {
			return false, nil
		}
	default:
		return false, nil
	}
	v := m.currentViewport()
	switch msg.String() {
	case "pgup":
		v.PgUp()
	case "pgdown":
		v.PgDn()
	case "home", "g":
		v.Home()
	case "end", "G":
		v.End()
	}
	m.body.Top = v.Top
	return true, nil
}

// detailKey moves the full-value view; it never leaves it inconsistent and
// never runs anything.
func (m *Model) detailKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "d":
		m.closeDetail()
		return nil
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		m.applyScroll(func(v *visual.Viewport) { v.Move(-1) })
	case "down", "j":
		m.applyScroll(func(v *visual.Viewport) { v.Move(1) })
	case "pgup":
		m.applyScroll((*visual.Viewport).PgUp)
	case "pgdown":
		m.applyScroll((*visual.Viewport).PgDn)
	case "home", "g":
		m.applyScroll((*visual.Viewport).Home)
	case "end", "G":
		m.applyScroll((*visual.Viewport).End)
	}
	return nil
}

// applyScroll runs one viewport move against the derived window and keeps only
// the resulting Top: the size is recomputed from the model on every frame.
func (m *Model) applyScroll(move func(*visual.Viewport)) {
	v := m.currentViewport()
	move(&v)
	m.body.Top = v.Top
}

// openDetail switches to the full-value view; the table position is kept so
// closing it returns the reader where they were.
func (m *Model) openDetail() {
	m.detailOpen = true
	m.body = visual.Viewport{Top: 0}
}

// closeDetail returns to the result body.
func (m *Model) closeDetail() {
	m.detailOpen = false
	m.body = visual.Viewport{Top: m.detailScroll.Top}
	m.detailScroll = visual.Viewport{}
}

// resetScrollState clears both positions and closes the detail view, so a new
// result or a feature change never inherits the previous screen's state.
func (m *Model) resetScrollState() {
	m.body = visual.Viewport{}
	m.detailOpen = false
	m.detailScroll = visual.Viewport{}
}

// detailViewKeycaps is the row shown while the full-value view is open. It
// replaces the menu row because none of the menu keys act on that screen.
func (m *Model) detailViewKeycaps() []visual.Keycap {
	return []visual.Keycap{
		{Key: "esc", Label: "닫기", Priority: 0},
		{Key: "↑↓", Label: "이동", Priority: 1},
		{Key: "PgUp/PgDn", Label: "페이지", Priority: 2},
		{Key: "g/G", Label: "처음/끝", Priority: 3},
		{Key: "q", Label: "종료", Priority: 0},
	}
}

// resultScrollKeycaps adds the keys the result body really supports. They are
// appended to the phase row instead of replacing it: the menu keys still work
// here, and advertising a key that does nothing is worse than a shorter row.
func (m *Model) resultScrollKeycaps(items []visual.Keycap) []visual.Keycap {
	if m.detailOpen {
		return m.detailViewKeycaps()
	}
	if m.bodyScrolls() {
		items = append(items, visual.Keycap{Key: "PgUp/PgDn", Label: "스크롤", Priority: 2})
	}
	if m.hasDetail() && m.phase == phaseReady {
		items = append(items, visual.Keycap{Key: "d", Label: "상세", Priority: 2})
	}
	return items
}
