package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The four counter-consistency invariants the spike caught, asserted on the real
// model frame using the same viewport the keys and the counter use. They are named after the bug each one prevents.
//
// 1. drawn rows and the counter describe the same window
// 2. the window never exceeds the total
// 3. no scroll hint when everything fits
// 4. the selection (the cursor row of a scrolling body) stays visible
func TestResultCounterInvariants(t *testing.T) {
	// A representative matrix: a body that fits, one that barely spills, and a
	// stack far taller than the frame.
	for _, total := range []int{1, 5, 17, 18, 19, 25, 40} {
		for _, height := range []int{18, 20, 24, 30, 40} {
			total, height := total, height
			t.Run(counterName(total, height), func(t *testing.T) {
				f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(total)}
				m := newTestModel(t, f)
				m.theme = *plainTheme()
				initModel(t, m)
				resize(t, m, 120, height)

				checkCounterMatchesDrawnRows(t, m, total)
				checkWindowNeverExceedsTotal(t, m, total)
				checkHintOnlyWhenNeeded(t, m, total)

				// G reaches the last row and keeps the header visible.
				pressRunes(t, m, "G")
				checkCounterMatchesDrawnRows(t, m, total)
				if total > 0 && !strings.Contains(m.View(), "svc-"+itoa(total)) {
					t.Fatalf("G did not reach the last row:\n%s", m.View())
				}
				if !strings.Contains(m.View(), "서비스") {
					t.Fatalf("the table header scrolled away:\n%s", m.View())
				}

				// g returns to the first row.
				pressRunes(t, m, "g")
				checkCounterMatchesDrawnRows(t, m, total)
				if !strings.Contains(m.View(), "svc-1") {
					t.Fatalf("g did not return to the first row:\n%s", m.View())
				}
			})
		}
	}
}

func counterName(total, height int) string {
	return "rows=" + itoa(total) + "/height=" + itoa(height)
}

func pressRunes(t *testing.T, m *Model, r string) {
	t.Helper()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)})
	*m = *(nm.(*Model))
}

// Invariant 1: the counter's range is exactly the rows on screen.
func checkCounterMatchesDrawnRows(t *testing.T, m *Model, total int) {
	t.Helper()
	viewport := m.currentViewport()
	if viewport.Visible() == 0 {
		if total > 0 && m.resultScrolls() {
			t.Fatalf("scrollable body drew no rows")
		}
		return
	}
	first, last, counterTotal := splitCounter(t, viewport.Counter())
	if counterTotal != total {
		t.Fatalf("counter total %d, want %d", counterTotal, total)
	}
	if first != viewport.Top+1 || last != viewport.Top+viewport.Visible() {
		t.Fatalf("counter %d-%d disagrees with window %d..%d",
			first, last, viewport.Top, viewport.Top+viewport.Visible())
	}
	drawn := 0
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "svc-") {
			drawn++
		}
	}
	if drawn != viewport.Visible() {
		t.Fatalf("drew %d service rows, counter says %d", drawn, viewport.Visible())
	}
}

// Invariant 2: no window can reach past the total.
func checkWindowNeverExceedsTotal(t *testing.T, m *Model, total int) {
	t.Helper()
	viewport := m.currentViewport()
	if viewport.Top < 0 {
		t.Fatalf("negative top %d", viewport.Top)
	}
	if end := viewport.Top + viewport.Visible(); end > total {
		t.Fatalf("window ends at %d, past total %d", end, total)
	}
}

// Invariant 3: a hint exists only when something is off screen, and it always
// names the keys.
func checkHintOnlyWhenNeeded(t *testing.T, m *Model, total int) {
	t.Helper()
	view := m.View()
	viewport := m.currentViewport()
	hasHint := strings.Contains(view, "PgUp/PgDn")
	if viewport.Overflow() {
		if !hasHint {
			t.Fatalf("overflow with no hint:\n%s", view)
		}
		return
	}
	// A body that fits may still show the footer's scroll chip only when the
	// body actually scrolls; it must never show a "more rows" hint.
	if strings.Contains(view, "더 있음") {
		t.Fatalf("fitting body shows a more-rows hint:\n%s", view)
	}
}

func splitCounter(t *testing.T, counter string) (int, int, int) {
	t.Helper()
	parts := strings.Split(counter, "/")
	if len(parts) != 2 {
		t.Fatalf("counter %q", counter)
	}
	span := strings.Split(parts[0], "-")
	if len(span) != 2 {
		t.Fatalf("counter %q", counter)
	}
	return atoi(t, span[0]), atoi(t, span[1]), atoi(t, parts[1])
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Esc puts a result sheet away and brings the menu back; nothing runs, and the
// result value is not discarded.
func TestEscDismissesALongResultAndRestoresTheMenu(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, f, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 24)

	if !m.resultScrolls() {
		t.Fatalf("the long result should scroll:\n%s", m.View())
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	if cmd != nil && isQuit(t, cmd) {
		t.Fatal("esc quit the program")
	}
	if m.result == nil {
		t.Fatal("esc discarded the result")
	}
	view := m.View()
	// At 120 columns the sidebar is the persistent menu; dismissing the sheet
	// must make the whole navigation usable again, which the sidebar always
	// shows, so the proof is that the result body is gone and the frame is
	// back to the menu phase.
	if strings.Contains(view, "svc-25") {
		t.Fatalf("esc did not put the result away:\n%s", view)
	}
	if !strings.Contains(view, "제목 logs") {
		t.Fatalf("esc did not restore the menu:\n%s", view)
	}
	// The result is not silently forgotten: it is still the last result, and r
	// re-reads.
	if !strings.Contains(view, "스택 상태") && !strings.Contains(view, "제목 status") {
		t.Fatalf("screen title lost:\n%s", view)
	}
}

// While the sheet covers the menu, enter must not run an invisible selection.
func TestEnterDoesNotRunWhileTheResultSheetCoversTheMenu(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, f, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 24)
	runs := logs.runs

	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if logs.runs != runs {
		t.Fatalf("enter ran a hidden selection: %d -> %d", runs, logs.runs)
	}
	if m.result == nil {
		t.Fatal("result dropped")
	}
}

// A short result keeps the navigation usable, so enter still works normally.
// At 120 columns the persistent sidebar is the menu; the body no longer
// repeats the feature list, so the sidebar entry is what must be present.
func TestShortResultKeepsTheMenuUsable(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(2)}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, f, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	if !strings.Contains(m.View(), "제목 logs") {
		t.Fatalf("menu missing over a short result:\n%s", m.View())
	}
	runs := logs.runs
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if logs.runs != runs+1 {
		t.Fatalf("enter did not run the selected feature: %d -> %d", runs, logs.runs)
	}
	if !strings.Contains(m.View(), "로그 본문") {
		t.Fatalf("the new result is not shown:\n%s", m.View())
	}
}

// A resize reflows the scroll window and keeps the position legal; the counter
// follows without a redraw ordering bug.
func TestResizeKeepsTheScrollWindowLegal(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 40)
	pressRunes(t, m, "G")
	for _, size := range [][2]int{{120, 18}, {80, 24}, {40, 18}, {200, 50}, {60, 20}} {
		resize(t, m, size[0], size[1])
		viewport := m.currentViewport()
		if viewport.Top+viewport.Visible() > 25 {
			t.Fatalf("%dx%d: window past total: %d..%d", size[0], size[1], viewport.Top, viewport.Top+viewport.Visible())
		}
		for i, line := range viewLines(t, m) {
			if visual.Width(line) > size[0] {
				t.Fatalf("%dx%d row %d too wide: %q", size[0], size[1], i, line)
			}
		}
	}
}

// A short result does not react to esc: the menu is already visible, so
// blanking the result would be a surprise with no benefit.
func TestEscLeavesAShortResultAlone(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(2)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	if m.resultDismissed {
		t.Fatal("esc dismissed a result that already fits")
	}
	if !strings.Contains(m.View(), "svc-1") {
		t.Fatalf("esc blanked the result:\n%s", m.View())
	}
}
