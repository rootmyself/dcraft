package visual

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeLine turns one line of child output into text the screen can measure.
// The width helpers below count display cells; a control character is not a
// cell the terminal draws, so measuring it as zero would let a hostile line pass
// the width check and then move the cursor over the sidebar.
//
// The rules, in order:
//
//	\r rewrites the current line (a progress bar is one row, not dozens)
//	\t becomes four spaces (a tab stops in the terminal would misalign a log)
//	CSI and OSC sequences are removed (colour moves the cursor)
//	every other C0/C1 control character is removed
//
// A carriage return can shorten the line, so the rewritten text replaces what
// was there: `100%\r90%` draws `90%` and no tail of the longer earlier line
// survives.
func SanitizeLine(line string) string {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return ""
	}
	// A carriage return means the next segment overwrote the current row. Join
	// the segments on the last one so no stale tail is left behind.
	if strings.Contains(line, "\r") {
		parts := strings.Split(line, "\r")
		line = parts[len(parts)-1]
	}
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); {
		r := line[i]
		switch {
		case r == '\t':
			b.WriteString("    ")
			i++
		case r == 0x1b:
			n := controlSequenceLen(line[i:])
			if n == 0 {
				// A lone escape has no sequence to decode; drop it rather than
				// printing a byte the terminal would act on.
				i++
				continue
			}
			seq := line[i : i+n]
			if keepsPaint(seq) {
				b.WriteString(seq)
			}
			i += n
		case r < 0x20 || r == 0x7f:
			i++
		case r == 0xc2 && i+1 < len(line) && line[i+1] >= 0x80 && line[i+1] <= 0x9f:
			// C1 control characters arrive as two bytes in UTF-8.
			i += 2
		default:
			b.WriteByte(r)
			i++
		}
	}
	return b.String()
}

// controlSequenceLen returns the byte length of the escape sequence at the
// start of s, or zero when s does not begin with one. It handles CSI
// (`ESC [ … final`) and OSC (`ESC ] … BEL|ST`) plus the two-byte escapes that
// only change a mode.
func controlSequenceLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		// A two-byte escape such as ESC ( B or ESC 7.
		return 2
	}
}

// keepsPaint reports whether a sequence may stay. A colour SGR keeps coloured
// tool output readable; every other sequence moves the cursor, clears the
// screen or changes the window, so it is removed before the width is measured.
func keepsPaint(seq string) bool {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return false
	}
	body := seq[2 : len(seq)-1]
	if body == "" {
		return true
	}
	// `ESC [ m` is a reset, so it also keeps paint state consistent.
	for _, part := range strings.Split(body, ";") {
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return false
		}
		// SGR only. Private modes start with '?' and never reach here because
		// the final byte would not be 'm'.
		if n < 0 || n > 107 {
			return false
		}
	}
	return true
}

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
