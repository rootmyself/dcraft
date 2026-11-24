package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// marqueeSummaryWidth is what the sidebar band shows of the summary at 100
// columns: the pane width minus the frame and the two-cell indent.
const marqueeSummaryWidth = 22

// marqueeHarness drives the animation without a wall clock: every call to step
// runs one injected tick and the model schedules the next one itself.
type marqueeHarness struct {
	model *Model
	cmd   tea.Cmd
}

func newMarqueeHarness(t *testing.T, summary string) *marqueeHarness {
	t.Helper()
	m, primary := navModel(t)
	primary.desc.Summary = summary
	m.marqueeTick = func(_ time.Duration, tick func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tick(time.Time{}) }
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	return &marqueeHarness{model: m, cmd: cmd}
}

func (h *marqueeHarness) step(t *testing.T) {
	t.Helper()
	if h.cmd == nil {
		t.Fatal("animation stopped")
	}
	_, h.cmd = h.model.Update(h.cmd())
}

func (h *marqueeHarness) ticks(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		h.step(t)
	}
}

// key sends one key and keeps the command the model scheduled, so a later step
// runs the tick that belongs to the current generation.
func (h *marqueeHarness) key(t *testing.T, msg tea.KeyMsg) {
	t.Helper()
	_, h.cmd = h.model.Update(msg)
}

func (h *marqueeHarness) view() string { return h.model.View() }

// shows reports whether the summary window starts at the given cell offset. The
// marker is exactly one window wide, so two positions cannot be confused.
func (h *marqueeHarness) shows(t *testing.T, offset int) bool {
	t.Helper()
	start := offset
	if start+marqueeSummaryWidth > len("abcdefghijklmnopqrstuvwxyz") {
		start = len("abcdefghijklmnopqrstuvwxyz") - marqueeSummaryWidth
	}
	return strings.Contains(h.view(), " "+"abcdefghijklmnopqrstuvwxyz"[start:start+marqueeSummaryWidth])
}

func TestSidebarLongSummaryStartsWithoutEllipsis(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "가나다라마바사아자차카타파하 긴 설명의 마지막"
	resize(t, m, 100, 34)
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "가나다라") {
			if strings.Contains(line, "…") {
				t.Fatalf("summary has an ellipsis: %q", line)
			}
			return
		}
	}
	t.Fatalf("summary not visible:\n%s", view)
}

// The first movement waits for the initial delay, so the reader can take in the
// first characters instead of watching them leave.
func TestSidebarMarqueeWaitsBeforeFirstMovement(t *testing.T) {
	h := newMarqueeHarness(t, "abcdefghijklmnopqrstuvwxyz")
	if h.cmd == nil {
		t.Fatal("long summary did not schedule a tick")
	}
	delay := h.model.marqueeRestTicks(marqueeInitialDelay)
	h.ticks(t, delay)
	if !h.shows(t, 0) {
		t.Fatalf("summary moved during the initial delay:\n%s", h.view())
	}
	h.step(t)
	if !h.shows(t, 1) {
		t.Fatalf("summary did not move after the initial delay:\n%s", h.view())
	}
}

// A repeat starts moving immediately: the hold already gave the reader the whole
// summary, so paying the initial delay again would only lengthen the cycle.
func TestSidebarMarqueeRepeatSkipsInitialDelay(t *testing.T) {
	h := newMarqueeHarness(t, "abcdefghijklmnopqrstuvwxyz")
	// Advance to the end of the summary. Reaching it is the precondition of the
	// hold, so the count that follows is exact.
	for i := 0; i < 100 && !h.shows(t, 4); i++ {
		h.step(t)
	}
	if !h.shows(t, 4) {
		t.Fatalf("summary did not reach the end:\n%s", h.view())
	}
	// One tick reaches the end and starts the hold; the hold then lasts
	// marqueeHold. Checking one tick before the threshold proves the rest and
	// one tick after proves the repeat is not cut short.
	hold := h.model.marqueeRestTicks(marqueeHold)
	h.step(t)
	if !h.shows(t, 4) {
		t.Fatalf("summary left the end while holding:\n%s", h.view())
	}
	h.ticks(t, hold-1)
	if !h.shows(t, 4) {
		t.Fatalf("summary did not hold for %s:\n%s", marqueeHold, h.view())
	}
	h.step(t)
	if !h.shows(t, 0) {
		t.Fatalf("summary did not restart after the hold:\n%s", h.view())
	}
	h.step(t)
	if !h.shows(t, 1) {
		t.Fatalf("repeat waited for the initial delay again:\n%s", h.view())
	}
}

// A selection change is a new summary: it starts at the beginning and pays the
// initial delay again instead of resuming mid-animation.
func TestSidebarMarqueeSelectionChangeResetsToDelayedStart(t *testing.T) {
	h := newMarqueeHarness(t, "abcdefghijklmnopqrstuvwxyz")
	delay := h.model.marqueeRestTicks(marqueeInitialDelay)
	h.ticks(t, delay)
	h.step(t)
	if !h.shows(t, 1) {
		t.Fatalf("summary did not start moving:\n%s", h.view())
	}
	pressKey(t, h.model, tea.KeyMsg{Type: tea.KeyDown})
	h.key(t, tea.KeyMsg{Type: tea.KeyUp})
	if !h.shows(t, 0) {
		t.Fatalf("selection change did not reset the summary:\n%s", h.view())
	}
	h.ticks(t, delay)
	if !h.shows(t, 0) {
		t.Fatalf("selection change skipped the initial delay:\n%s", h.view())
	}
	h.step(t)
	if !h.shows(t, 1) {
		t.Fatalf("summary did not move after the selection change delay:\n%s", h.view())
	}
}

func TestSidebarMarqueePausesForHelp(t *testing.T) {
	h := newMarqueeHarness(t, "abcdefghijklmnopqrstuvwxyz")
	h.ticks(t, h.model.marqueeRestTicks(marqueeInitialDelay)+1)
	if !h.shows(t, 1) {
		t.Fatalf("summary did not move before help:\n%s", h.view())
	}
	pending := h.cmd
	pressKey(t, h.model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	_, cmd := h.model.Update(pending())
	if cmd != nil {
		t.Fatal("help scheduled another tick")
	}
	cmd = pressKey(t, h.model, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || !h.shows(t, 1) {
		t.Fatal("closing help did not resume at the same position")
	}
}

func TestSidebarMarqueeDoesNotSplitKoreanCharacters(t *testing.T) {
	h := newMarqueeHarness(t, "가나다라마바사아자차카타파하")
	h.ticks(t, h.model.marqueeRestTicks(marqueeInitialDelay))
	h.step(t)
	for _, line := range strings.Split(h.view(), "\n") {
		if strings.Contains(line, "나다라마바사") {
			if !strings.Contains(line, "│  나") || strings.Contains(line, "�") {
				t.Fatalf("wide character straddled the window: %q", line)
			}
			return
		}
	}
	t.Fatalf("shifted Korean summary not visible:\n%s", h.view())
}

func TestSidebarShortSummaryDoesNotScheduleAnimation(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "짧음"
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	if cmd != nil {
		t.Fatal("short summary scheduled animation")
	}
}
