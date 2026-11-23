package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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

func TestSidebarMarqueeMovesHoldsRepeatsAndResets(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "abcdefghijklmnopqrstuvwxyz"
	m.marqueeTick = func(_ time.Duration, tick func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tick(time.Time{}) }
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	if cmd == nil {
		t.Fatal("long summary did not schedule a tick")
	}
	step := func() {
		t.Helper()
		if cmd == nil {
			t.Fatal("animation stopped")
		}
		_, cmd = m.Update(cmd())
	}
	step()
	if !strings.Contains(m.View(), "bcdefghijklmnopqrstuvw") {
		t.Fatalf("summary did not move one cell:\n%s", m.View())
	}
	for i := 0; i < 3; i++ {
		step()
	}
	if !strings.Contains(m.View(), "efghijklmnopqrstuvwxyz") {
		t.Fatalf("summary did not reach the end:\n%s", m.View())
	}
	for i := 0; i < 29; i++ {
		step()
	}
	if !strings.Contains(m.View(), "efghijklmnopqrstuvwxyz") {
		t.Fatal("summary did not hold for three seconds")
	}
	step()
	if !strings.Contains(m.View(), "abcdefghijklmnopqrstuv") {
		t.Fatal("summary did not restart")
	}
	step()
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if !strings.Contains(m.View(), "abcdefghijklmnopqrstuv") {
		t.Fatal("selection change did not reset summary")
	}
}

func TestSidebarMarqueePausesForHelp(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "abcdefghijklmnopqrstuvwxyz"
	m.marqueeTick = func(_ time.Duration, tick func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tick(time.Time{}) }
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	_, cmd = m.Update(cmd())
	pending := cmd
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	_, cmd = m.Update(pending())
	if cmd != nil {
		t.Fatal("help scheduled another tick")
	}
	cmd = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || !strings.Contains(m.View(), "bcdefghijklmnopqrstuvw") {
		t.Fatal("closing help did not resume at the same position")
	}
}

func TestSidebarMarqueeDoesNotSplitKoreanCharacters(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "가나다라마바사아자차카타파하"
	m.marqueeTick = func(_ time.Duration, tick func(time.Time) tea.Msg) tea.Cmd {
		return func() tea.Msg { return tick(time.Time{}) }
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	_, _ = m.Update(cmd())
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "나다라마바사") {
			if !strings.Contains(line, "│  나") || strings.Contains(line, "�") {
				t.Fatalf("wide character straddled the window: %q", line)
			}
			return
		}
	}
	t.Fatalf("shifted Korean summary not visible:\n%s", m.View())
}

func TestSidebarShortSummaryDoesNotScheduleAnimation(t *testing.T) {
	m, primary := navModel(t)
	primary.desc.Summary = "짧음"
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 34})
	if cmd != nil {
		t.Fatal("short summary scheduled animation")
	}
}
