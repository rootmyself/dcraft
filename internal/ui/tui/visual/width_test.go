package visual

import "testing"

const ansiRed = "\x1b[31m"
const ansiReset = "\x1b[0m"

func TestWidthMeasuresDisplayCells(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"ascii", "dcraft", 6},
		{"korean is two cells per syllable", "스택 상태", 9},
		{"mixed", "dcraft · 스택", 13},
		{"empty", "", 0},
		{"emoji-free punctuation", "a·b", 3},
		{"ansi is ignored", ansiRed + "스택" + ansiReset, 4},
		{"nested ansi", ansiRed + "a" + ansiReset + ansiRed + "b" + ansiReset, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Width(tc.in); got != tc.want {
				t.Fatalf("Width(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncateNeverExceedsWidthAndMarksTheCut(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
		ell  string
		want string
	}{
		{"short text is untouched", "api", 10, "…", "api"},
		{"exact fit is untouched", "abcde", 5, "…", "abcde"},
		{"ascii overflow", "abcdefgh", 5, "…", "abcd…"},
		{"korean overflow never splits a syllable", "스택상태확인", 6, "…", "스택…"},
		{"ascii ellipsis", "abcdefgh", 5, "~", "abcd~"},
		{"zero width", "abc", 0, "…", ""},
		{"one cell only has room for the marker", "abc", 1, "…", "…"},
		{"ansi input keeps escapes and width", ansiRed + "abcdef" + ansiReset, 4, "…", ansiRed + "abc" + "…" + ansiReset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Truncate(tc.in, tc.w, tc.ell)
			if got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
			if Width(got) > tc.w {
				t.Fatalf("Truncate(%q, %d) = %d cells, over the limit", tc.in, tc.w, Width(got))
			}
		})
	}
}

func TestFitPadsAndTruncatesToExactWidth(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"ab", 4, "ab  "},
		{"스택", 4, "스택"},
		{"스택", 3, "스…"},
		{"abcd", 4, "abcd"},
	}
	for _, tc := range cases {
		got := Fit(tc.in, tc.w, "…")
		if got != tc.want {
			t.Fatalf("Fit(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
		if Width(got) != tc.w {
			t.Fatalf("Fit(%q, %d) = %d cells, want %d", tc.in, tc.w, Width(got), tc.w)
		}
	}
}

func TestPadDoesNotTruncate(t *testing.T) {
	if got := Pad("스택", 6); got != "스택  " {
		t.Fatalf("Pad = %q", got)
	}
	if got := Pad("toolong", 3); got != "toolong" {
		t.Fatalf("Pad truncated: %q", got)
	}
	if got := PadLeft("7", 3); got != "  7" {
		t.Fatalf("PadLeft = %q", got)
	}
}

func TestWrapKoreanProse(t *testing.T) {
	lines := Wrap("서비스를 잠시 멈춘 뒤 로컬 폴더에 백업", 20)
	if len(lines) < 2 {
		t.Fatalf("wrap produced %d line(s): %q", len(lines), lines)
	}
	for i, line := range lines {
		if w := Width(line); w > 20 {
			t.Fatalf("line %d is %d cells: %q", i, w, line)
		}
		if line == "" {
			t.Fatalf("line %d is empty", i)
		}
	}
	// Words survive intact when they fit; the text is not cut mid-word.
	joined := ""
	for _, line := range lines {
		joined += line
	}
	if !contains(joined, "백업") || !contains(joined, "서비스를") {
		t.Fatalf("wrap broke words: %q", lines)
	}
}

func TestWrapBreaksAPathThatCannotFit(t *testing.T) {
	path := "/Users/npnl/src/acme/acme-platform/compose.staging.yaml"
	lines := Wrap(path, 10)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	for _, line := range lines {
		if w := Width(line); w > 10 {
			t.Fatalf("line %q is %d cells", line, w)
		}
	}
}

func TestWrapEmptyTextYieldsOneEmptyLine(t *testing.T) {
	if got := Wrap("", 10); len(got) != 1 || got[0] != "" {
		t.Fatalf("Wrap empty = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
