package tui

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The status table is a scrolling content list, so it carries the approved
// one-cell scrollbar gutter with a solid thumb. The thumb must be inside the
// frame and must describe the window the counter names.
func TestStatusTableRendersTheScrollbarThumb(t *testing.T) {
	m := statusResultModel(t)
	// A short frame is needed now that the result has no guidance block: eight
	// service rows have to overflow the band for the gutter to exist.
	resize(t, m, 99, 18)
	if !m.bodyScrolls() {
		t.Fatalf("the fixture does not scroll:\n%s", m.View())
	}
	view := m.View()
	if !strings.Contains(view, m.theme.Glyphs.Thumb) {
		t.Fatalf("no scrollbar thumb in a scrolling table:\n%s", view)
	}
	// The thumb sits in the last cell of a band row, never on the frame border.
	for _, line := range viewLines(t, m) {
		if !strings.Contains(line, m.theme.Glyphs.Thumb) {
			continue
		}
		if got := visual.Width(line); got > 120 {
			t.Fatalf("row with a thumb is %d cells: %q", got, line)
		}
	}
	// A short result has no gutter: nothing scrolls, so nothing claims to.
	short := statusResultModel(t)
	resize(t, short, 120, 40)
	if strings.Contains(short.View(), short.theme.Glyphs.Thumb) {
		t.Fatalf("a fitting table shows a scrollbar:\n%s", short.View())
	}
}

// The thumb moves with the window: at the top it is near the top, at the end
// near the bottom.
func TestScrollbarThumbFollowsTheWindow(t *testing.T) {
	m := statusResultModel(t)
	// A short frame keeps the table band while the eight rows overflow it.
	resize(t, m, 99, 18)
	topRows := thumbRows(t, m)
	if len(topRows) == 0 {
		t.Fatal("no thumb at the top")
	}
	pressRunes(t, m, "G")
	endRows := thumbRows(t, m)
	if len(endRows) == 0 {
		t.Fatal("no thumb at the end")
	}
	if endRows[0] <= topRows[0] {
		t.Fatalf("thumb did not move down: top rows %v, end rows %v", topRows, endRows)
	}
}

// thumbRows is the frame row index of every row carrying the thumb glyph.
func thumbRows(t *testing.T, m *Model) []int {
	t.Helper()
	var rows []int
	for i, line := range viewLines(t, m) {
		if strings.Contains(line, m.theme.Glyphs.Thumb) {
			rows = append(rows, i)
		}
	}
	return rows
}

// The log viewport is the other scrolling content list with the approved
// scrollbar gutter.
func TestLogViewportRendersTheScrollbarThumb(t *testing.T) {
	m := goldenLogs(t)
	resize(t, m, 120, 18)
	if !m.liveScrolls() {
		t.Fatalf("the log fixture does not scroll:\n%s", m.View())
	}
	if !strings.Contains(m.View(), m.theme.Glyphs.Thumb) {
		t.Fatalf("no scrollbar thumb in a scrolling log:\n%s", m.View())
	}
}
