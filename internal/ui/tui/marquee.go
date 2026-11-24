package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The sidebar summary animation. The timings are named here so the screen and
// its tests agree on the cadence: the summary rests before its first movement,
// moves one cell per tick, rests at the end, and then rests again before each
// repeat.
const (
	// defaultMarqueeInterval is one movement step.
	defaultMarqueeInterval = 100 * time.Millisecond
	// marqueeInitialDelay is the rest before the first movement, on every
	// appearance, selection change and repeat. Reading the first cell is the
	// point of the rest, so a cycle never starts already moving.
	marqueeInitialDelay = time.Second
	// marqueeHold is the rest at the end. It gives the reader the whole
	// summary, then the repeat starts over with the initial delay.
	marqueeHold = 2 * time.Second
)

type marqueeState struct {
	name, summary string
	offset, hold  int
	// delay counts the scheduled ticks that still pass without movement.
	delay      int
	generation uint64
	scheduled  bool
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

// marqueeRestTicks converts a rest into the number of ticks that pass without
// movement. The tick causally after them is the one that moves, so a one-second
// rest at a 100ms interval is nine silent ticks and a tenth that moves.
func (m *Model) marqueeRestTicks(d time.Duration) int {
	if m.marqueeInterval <= 0 {
		return 0
	}
	rest := int((d + m.marqueeInterval - 1) / m.marqueeInterval)
	if rest > 0 {
		rest--
	}
	return rest
}

func (m *Model) scheduleMarquee() tea.Cmd {
	name, summary, width := m.marqueeTarget()
	s := &m.marquee
	if name != s.name || summary != s.summary {
		s.name, s.summary = name, summary
		s.offset, s.hold = 0, 0
		s.delay = m.marqueeRestTicks(marqueeInitialDelay)
		s.generation++
		s.scheduled = false
	}
	if name == "" || visual.Width(summary) <= width || m.overlayOpen() {
		if s.scheduled {
			s.generation++
			s.scheduled = false
		}
		if name == "" || visual.Width(summary) <= width {
			s.offset, s.hold, s.delay = 0, 0, 0
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
	if m.overlayOpen() || name != s.name || summary != s.summary || width <= 0 {
		return
	}
	if s.delay > 0 {
		s.delay--
		return
	}
	max := visual.Width(summary) - width
	if s.offset < max {
		s.offset++
		return
	}
	s.hold++
	if time.Duration(s.hold)*m.marqueeInterval >= marqueeHold {
		// Back to the start with the same rest the first appearance paid, so a
		// repeat never begins mid-movement.
		s.offset, s.hold = 0, 0
		s.delay = m.marqueeRestTicks(marqueeInitialDelay)
	}
}
