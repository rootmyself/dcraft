package visual

import "strings"

// asciiReplacements is the closed set of decorative glyphs the TUI itself
// draws, mapped to their approved one-cell ASCII forms. Korean text is
// deliberately absent: the contract keeps the Korean labels and says the
// locale only changes the glyphs.
var asciiReplacements = map[rune]string{
	'╭': "+", '╮': "+", '╰': "+", '╯': "+",
	'─': "-", '│': "|",
	'▸': ">", '▶': ">",
	'…': "~",
	'✓': "+", '✔': "+",
	'◐': "!", '◓': "!", '◑': "!", '◒': "!",
	'■': "x", '□': "o", '○': "o",
	'▲': "!", '△': "!",
	'✕': "!", '✗': "!",
	'█': "#",
	'·': "-", '•': "-",
}

// ASCIISafe replaces the TUI's decorative glyphs with their approved ASCII
// forms. It is applied to a whole frame in a non-UTF-8 locale so no glyph can
// bypass the table, including one embedded in a shared renderer's text.
//
// Only the closed set above is touched: Korean labels, paths, service names and
// user output keep their characters. Every replacement is exactly one cell, so
// the layout cannot shift.
func ASCIISafe(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if repl, ok := asciiReplacements[r]; ok {
			b.WriteString(repl)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ASCIISafeLines is ASCIISafe over multiple lines.
func ASCIISafeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ASCIISafe(line)
	}
	return out
}
