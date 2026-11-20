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
