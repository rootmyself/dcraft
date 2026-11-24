package core

import "strings"

// The one section-spacing rule for human-readable output: a section label gets
// exactly one blank line before it and none after it. The label starts a
// section, so the boundary belongs to the label rather than to the content
// above or below it. Both the CLI text and the TUI render through this rule,
// which is why it lives beside the text builders instead of in one interface.

// WriteSection writes one blank line and then the label with its own newline.
// Content written next stays attached to the label. A body that opens with a
// label passes through NormalizeSections, which drops the leading blank line.
func WriteSection(b *strings.Builder, label string) {
	b.WriteString("\n")
	b.WriteString(label)
	b.WriteString("\n")
}

// CollapseBlankRuns keeps at most one blank line in a row. The document renderer
// and a string-backed screen both need it: a paragraph may carry a leading blank
// line and the block gap adds another, which would draw a two-line hole instead
// of a section boundary.
func CollapseBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// NormalizeSections collapses every run of blank lines to one and trims the
// leading and trailing blank lines of a rendered body. A label supplies the one
// separator before it, so a run of two or more is a duplicate rather than a
// deliberate gap.
func NormalizeSections(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "" {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
