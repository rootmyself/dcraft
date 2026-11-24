package core

import (
	"strings"
	"testing"
)

// A label starts a section, so exactly one blank line comes before it and the
// content stays attached to it.
func TestWriteSectionPutsOneBlankLineBeforeTheLabel(t *testing.T) {
	var b strings.Builder
	b.WriteString("첫 문단\n")
	b.WriteString("이어지는 줄\n")
	WriteSection(&b, "주의:")
	b.WriteString("  - 첫 번째\n")

	got := b.String()
	want := "첫 문단\n이어지는 줄\n\n주의:\n  - 첫 번째\n"
	if got != want {
		t.Fatalf("section = %q, want %q", got, want)
	}
}

// A rendered body never starts with a blank line, even when its first label is
// written through the section rule.
func TestNormalizeSectionsDropsLeadingAndTrailingBlanks(t *testing.T) {
	got := NormalizeSections("\n\n주의:\n  - 하나\n\n\n")
	if got != "주의:\n  - 하나" {
		t.Fatalf("normalized = %q", got)
	}
}

// Consecutive blank lines collapse to one, in a body that mixes a label's own
// separator with a paragraph's blank line.
func TestNormalizeSectionsCollapsesBlankRuns(t *testing.T) {
	in := "첫 문단\n\n\n\n주의:\n  - 하나\n\n참고:\n  - 둘\n"
	got := NormalizeSections(in)
	want := "첫 문단\n\n주의:\n  - 하나\n\n참고:\n  - 둘"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

// A body with no blank lines is returned unchanged, so the collapse cannot
// reflow a caller's deliberate single-line layout.
func TestNormalizeSectionsLeavesTightBodiesAlone(t *testing.T) {
	in := "제목\n값: 하나\n값: 둘"
	if got := NormalizeSections(in); got != in {
		t.Fatalf("normalized = %q, want %q", got, in)
	}
}
