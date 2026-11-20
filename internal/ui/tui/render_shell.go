package tui

import (
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// composeFrame lays out the screen and pins the lines that must never be the
// part that gets cut.
func (m *Model) composeFrame(body []string) []string {
	return visual.Compose(m.theme, m.width, m.height, visual.Frame{
		Header: visual.Header(m.theme, m.width, m.facts),
		Body:   body,
		Pinned: m.pinnedLines(),
		Footer: visual.Row(m.theme, m.footerItems(), m.width),
	})
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
