package visual

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Width is the display-cell width of s, ignoring ANSI sequences. Korean
// syllables count as two cells, exactly as the terminal draws them.
func Width(s string) int {
	return ansi.StringWidth(s)
}

// Truncate shortens s to at most w display cells and appends the explicit
// ellipsis when it had to cut. ANSI sequences are preserved and a wide rune
// that would straddle the boundary is dropped instead of splitting.
//
// If w has no room for both content and the marker, the marker alone is
// returned: a partial Korean syllable or a silently cut value is worse than an
// explicit "there is more".
func Truncate(s string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	tail := ell
	if tail == "" {
		tail = "…"
	}
	if Width(tail) > w {
		tail = ""
	}
	return ansi.Truncate(s, w, tail)
}

// Fit makes s occupy exactly w cells: truncating with an explicit ellipsis when
// it is wider and padding with spaces when it is narrower. A wide rune that
// would straddle the boundary is dropped instead of split, so the truncated
// result may be one cell short; the padding makes up that cell rather than
// leaving a ragged row.
func Fit(s string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if Width(s) > w {
		return Pad(Truncate(s, w, ell), w)
	}
	return Pad(s, w)
}

// Pad appends spaces until s occupies w cells. It never truncates; callers that
// need an exact width use Fit.
func Pad(s string, w int) string {
	if n := w - Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// PadLeft right-aligns s inside w cells. It never truncates.
func PadLeft(s string, w int) string {
	if n := w - Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// Wrap breaks text into lines of at most w display cells. Words stay whole when
// they fit; a single word wider than the line (a long path) is hard-broken
// without an ellipsis because the value continues on the next line.
func Wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return []string{""}
	}
	lines := strings.Split(ansi.Wrap(text, w, ""), "\n")
	var sgr string
	for i, line := range lines {
		prefix := sgr
		var state byte
		for rest := line; len(rest) > 0; {
			seq, _, n, next := ansi.DecodeSequence(rest, state, nil)
			state = next
			rest = rest[n:]
			if (strings.HasPrefix(seq, "\x1b[") || strings.HasPrefix(seq, "\x9b")) && strings.HasSuffix(seq, "m") {
				if seq == "\x1b[m" || seq == "\x1b[0m" || seq == "\x9bm" || seq == "\x9b0m" {
					sgr = ""
				} else {
					sgr += seq
				}
			}
		}
		lines[i] = prefix + line
		if sgr != "" {
			lines[i] += "\x1b[0m"
		}
	}
	return lines
}

func splitCells(s string, n int) (string, string) {
	width := 0
	for i, r := range s {
		rw := ansi.StringWidth(string(r))
		if width+rw > n {
			return s[:i], s[i:]
		}
		width += rw
	}
	return s, ""
}
