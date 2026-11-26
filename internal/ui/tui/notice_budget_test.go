package tui

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"

	tea "github.com/charmbracelet/bubbletea"
)

// A screen notice is part of the body budget. The busy-quit refusal is the case
// the review caught: it used to be appended after the band, so on a short frame
// the sentence explaining the risk was clipped with no way to scroll to it.
func TestNoticeIsCountedAgainstTheBodyBudget(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 18)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // the panel starts on cancel
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	if m.phase != phaseLoading {
		t.Fatalf("phase = %v, want loading", m.phase)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)

	if m.notice == "" {
		t.Fatal("the quit refusal set no notice")
	}
	// Every line of the frame must stay inside the terminal, and the whole
	// notice must be reachable: shown now, or reachable by scrolling.
	if !noticeReachable(t, m, m.notice) {
		t.Fatalf("notice is unreachable:\n%s", m.View())
	}
	for i, line := range viewLines(t, m) {
		if width := visual.Width(line); width > 120 {
			t.Fatalf("row %d is %d cells: %q", i, width, line)
		}
	}
}

// noticeReachable reports whether the whole notice text appears in the drawn
// rows or in the scrollable body the keys can reach.
func noticeReachable(t *testing.T, m *Model, notice string) bool {
	t.Helper()
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if strings.Contains(flat(m.View()), flat(notice)) {
		return true
	}
	params := m.layoutBody()
	for _, row := range params.lines {
		if strings.Contains(flat(row), flat(notice)) {
			return true
		}
	}
	// The body is bounded, so the notice may be split across rows; joining the
	// drawable body is the reachable test that the review's bug failed.
	return strings.Contains(flat(strings.Join(params.lines, " ")), flat(notice))
}
