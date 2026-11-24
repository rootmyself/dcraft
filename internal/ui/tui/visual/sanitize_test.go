package visual

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A tab is four spaces: a terminal's own tab stops would misalign a log row
// against the column it shares with the sidebar.
func TestSanitizeLineExpandsTabs(t *testing.T) {
	if got := SanitizeLine("a\tb"); got != "a    b" {
		t.Fatalf("sanitized = %q", got)
	}
	if Width(SanitizeLine("a\tb")) != 6 {
		t.Fatalf("width = %d, want 6", Width(SanitizeLine("a\tb")))
	}
}

// A carriage return rewrites the row. The last segment is what the terminal
// would show, so nothing of the longer earlier segment may survive as a tail.
func TestSanitizeLineRewritesCarriageReturn(t *testing.T) {
	got := SanitizeLine("progress 100%\r90%")
	if got != "90%" {
		t.Fatalf("sanitized = %q, want the last segment", got)
	}
	if strings.Contains(got, "100%") {
		t.Fatalf("stale tail survived: %q", got)
	}
}

// A cursor-movement CSI is removed, but a colour SGR survives so coloured tool
// output still reads.
func TestSanitizeLineRemovesCursorMovesAndKeepsColor(t *testing.T) {
	line := "\x1b[2Aover\x1b[31mred\x1b[0m"
	got := SanitizeLine(line)
	if strings.Contains(got, "\x1b[2A") {
		t.Fatalf("cursor move survived: %q", got)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("colour was removed: %q", got)
	}
	if ansi.Strip(got) != "overred" {
		t.Fatalf("text = %q", ansi.Strip(got))
	}
}

// An OSC sequence (a window title, a hyperlink) is removed whole, not partly
// printed.
func TestSanitizeLineRemovesOSC(t *testing.T) {
	for _, line := range []string{
		"\x1b]0;title\x07visible",
		"\x1b]8;;https://example.test\x07link\x1b]8;;\x07",
	} {
		got := SanitizeLine(line)
		if strings.ContainsAny(got, "\x1b\x07") {
			t.Fatalf("escape survived in %q", got)
		}
	}
	if got := SanitizeLine("\x1b]0;title\x07visible"); got != "visible" {
		t.Fatalf("sanitized = %q", got)
	}
}

// A lone escape byte and the remaining C0 controls are dropped, so the measured
// width is the drawn width.
func TestSanitizeLineDropsLoneEscapesAndControls(t *testing.T) {
	// A trailing escape has no sequence to decode, so it is the lone case.
	got := SanitizeLine("a\x00b\x07c\x1b")
	if got != "abc" {
		t.Fatalf("sanitized = %q", got)
	}
	if Width(got) != 3 {
		t.Fatalf("width = %d, want 3", Width(got))
	}
}

// The width a hostile line reports after sanitation is the width the terminal
// draws, which is what the frame's column math needs.
func TestSanitizeLineWidthMatchesDrawnCells(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"plain", 5},
		{"a\tb", 6},
		{"100%\r90%", 3},
		{"\x1b[2Aover", 4},
		{"\x1b[31mred\x1b[0m", 3},
		{"가나", 4},
	}
	for _, tc := range cases {
		if got := Width(SanitizeLine(tc.in)); got != tc.want {
			t.Fatalf("Width(SanitizeLine(%q)) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
