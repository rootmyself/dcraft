package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

type marqueeState struct {
	name, summary string
	offset, hold  int
	generation    uint64
	scheduled     bool
}

type marqueeTickMsg struct{ generation uint64 }

func (m *Model) marqueeTarget() (string, string, int) {
	if !m.sidebarVisible() || visual.BodyRows(m.height)-2 < 22 {
		return "", "", 0
	}
	f := m.selectedFeature()
	if f == nil || !m.navFilterMatches(f.Descriptor()) {
		return "", "", 0
	}
	d := f.Descriptor()
	return d.Name, d.Summary, visual.SidebarWidth(m.width) - 4
}

func (m *Model) scheduleMarquee() tea.Cmd {
	name, summary, width := m.marqueeTarget()
	s := &m.marquee
	if name != s.name || summary != s.summary {
		s.name, s.summary = name, summary
		s.offset, s.hold = 0, 0
		s.generation++
		s.scheduled = false
	}
	if name == "" || visual.Width(summary) <= width || m.helpOpen || m.confirmOpen() {
		if s.scheduled {
			s.generation++
			s.scheduled = false
		}
		if visual.Width(summary) <= width {
			s.offset, s.hold = 0, 0
		}
		return nil
	}
	if max := visual.Width(summary) - width; s.offset > max {
		s.offset, s.hold = max, 0
	}
	if s.scheduled {
		return nil
	}
	s.scheduled = true
	generation := s.generation
	return m.marqueeTick(m.marqueeInterval, func(time.Time) tea.Msg {
		return marqueeTickMsg{generation: generation}
	})
}

func (m *Model) advanceMarquee(tick marqueeTickMsg) {
	s := &m.marquee
	if !s.scheduled || tick.generation != s.generation {
		return
	}
	s.scheduled = false
	name, summary, width := m.marqueeTarget()
	if m.helpOpen || m.confirmOpen() || name != s.name || summary != s.summary || width <= 0 {
		return
	}
	max := visual.Width(summary) - width
	if s.offset < max {
		s.offset++
		return
	}
	s.hold++
	if time.Duration(s.hold)*m.marqueeInterval >= 3*time.Second {
		s.offset, s.hold = 0, 0
	}
}
