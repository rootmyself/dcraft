package tui

import (
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// composeFrame lays out the screen: the header, the body, the keycap footer and
// the wide layout's persistent sidebar. Header and footer still span the whole
// terminal.
//
// With a panel open the same frame becomes the background and the panel is
// composed over its body: Bubble Tea v1 has no overlay API, so the screen is
// composed by hand. There is no frame-level pinned row: the one pinned statement
// in the TUI (the confirmation risk) lives inside the panel, where it cannot be
// scrolled away and cannot be drawn twice.
func (m *Model) composeFrame(body []string) []string {
	frame := visual.Compose(m.theme, m.width, m.height, visual.Frame{
		Header: visual.Header(m.theme, m.width, m.facts),
		Left:   m.sidebar(),
		Body:   body,
		Footer: visual.Row(m.theme, m.footerItems(), m.width),
	})
	spec, ok := m.overlaySpec()
	if !ok {
		return frame
	}
	return visual.ComposeModal(m.theme, m.width, m.height, frame, spec)
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
