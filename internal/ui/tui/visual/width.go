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
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		out = append(out, wrapLine(paragraph, w)...)
	}
	return out
}

func wrapLine(text string, w int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var out []string
	cur := ""
	flush := func() {
		out = append(out, cur)
		cur = ""
	}
	for _, word := range strings.Fields(text) {
		if cur != "" && Width(cur)+1+Width(word) <= w {
			cur += " " + word
			continue
		}
		if cur != "" {
			flush()
		}
		for Width(word) > w {
			head, rest := splitCells(word, w)
			out = append(out, head)
			word = rest
		}
		cur = word
	}
	if cur != "" {
		flush()
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// splitCells cuts s after n display cells, returning the head and the rest. A
// wide rune that does not fit in n cells ends the head one cell early.
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
