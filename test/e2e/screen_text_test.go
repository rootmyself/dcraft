package e2e

import (
	"regexp"
	"strings"
)

// ansiSGR matches a CSI sequence that ends in a letter (colors, cursor moves).
// PTY captures interleave them with the text, and the TUI styles a keycap chip
// and its label as two runs, so a raw substring check on "[q] 종료" would fail
// even though the screen shows exactly that. Screen assertions therefore match
// visible text; escape-byte assertions stay in the dedicated purity tests.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// visibleText strips ANSI escape sequences from a PTY capture.
func visibleText(s string) string {
	s = ansiSGR.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\r", "")
}

// screenContains reports whether the visible screen contains text.
func screenContains(screen, text string) bool {
	return strings.Contains(visibleText(screen), text)
}

// screenRowWith returns the last visible row containing label. A panel pads a
// label column, so a label and its value are not adjacent characters; the row
// is the unit that proves they belong together.
func screenRowWith(screen, label string) (string, bool) {
	rows := strings.Split(visibleText(screen), "\n")
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.Contains(rows[i], label) {
			return rows[i], true
		}
	}
	return "", false
}
