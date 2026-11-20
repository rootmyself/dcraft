package tui

import (
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// composeFrame lays out the screen and pins the lines that must never be the
// part that gets cut. The wide layout adds the persistent sidebar as a left
// pane; header and footer still span the whole terminal.
func (m *Model) composeFrame(body []string) []string {
	return visual.Compose(m.theme, m.width, m.height, visual.Frame{
		Header: visual.Header(m.theme, m.width, m.facts),
		Left:   m.sidebar(),
		Body:   body,
		Pinned: m.pinnedLines(),
		Footer: visual.Row(m.theme, m.footerItems(), m.width),
	})
}

// sidebar is the persistent navigation pane for the wide layout. It is empty
// in every other layout, which is how the breakpoint switch is expressed: the
// pane is always there at >=100 columns, so the current position never
// disappears and switching tasks stays one keystroke away.
func (m *Model) sidebar() []string {
	if !m.sidebarVisible() {
		return nil
	}
	width := visual.SidebarWidth(m.width)
	height := visual.BodyRows(m.height)
	return visual.Sidebar(m.theme, width, height, m.nav())
}

// pinnedLines are the rows the shell keeps directly above the footer, outside
// the scrolling body. The confirmation risk sentence is the approved pinned
// line: the fields may scroll out of view, the risk statement may not.
func (m *Model) pinnedLines() []string {
	if m.phase != phaseConfirm || m.pending == nil {
		return nil
	}
	return []string{m.noticeText(msgConfirmRisk)}
}
