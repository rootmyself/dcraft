package visual

import (
	"strings"
	"testing"
)

// The ASCII table from the approved contract. Every replacement is exactly one
// cell, so column math does not change between glyph sets.
func TestASCIIGlyphTable(t *testing.T) {
	g := ASCIIGlyphs()
	if !g.ASCII {
		t.Fatal("ASCIIGlyphs does not report ASCII")
	}
	pairs := []struct{ got, want string }{
		{g.TL, "+"}, {g.TR, "+"}, {g.BL, "+"}, {g.BR, "+"},
		{g.H, "-"}, {g.V, "|"},
		{g.Sel, ">"}, {g.Ell, "~"}, {g.OK, "+"}, {g.Warn, "!"},
		{g.Stopped, "x"}, {g.None, "o"}, {g.Danger, "!"}, {g.Thumb, "#"},
		{g.Spin[0], "/"}, {g.Spin[1], "-"}, {g.Spin[2], "\\"}, {g.Spin[3], "|"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Fatalf("ASCII glyph = %q, want %q", p.got, p.want)
		}
		if Width(p.got) != 1 {
			t.Fatalf("ASCII glyph %q is %d cells", p.got, Width(p.got))
		}
	}
}

func TestUTF8GlyphTable(t *testing.T) {
	g := UTF8Glyphs()
	if g.ASCII {
		t.Fatal("UTF8Glyphs reports ASCII")
	}
	pairs := []struct{ got, want string }{
		{g.TL, "╭"}, {g.TR, "╮"}, {g.BL, "╰"}, {g.BR, "╯"},
		{g.H, "─"}, {g.V, "│"},
		{g.Sel, "▸"}, {g.Ell, "…"}, {g.OK, "✓"}, {g.Warn, "◐"},
		{g.Stopped, "■"}, {g.None, "○"}, {g.Danger, "▲"}, {g.Thumb, "█"},
		{g.Spin[0], "◐"}, {g.Spin[3], "◒"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Fatalf("UTF-8 glyph = %q, want %q", p.got, p.want)
		}
		if Width(p.got) != 1 {
			t.Fatalf("glyph %q is %d cells", p.got, Width(p.got))
		}
	}
}

func TestSpinnerFramesWrap(t *testing.T) {
	g := UTF8Glyphs()
	if g.SpinnerFrame(4) != g.Spin[0] || g.SpinnerFrame(5) != g.Spin[1] {
		t.Fatal("spinner does not wrap")
	}
	if g.SpinnerFrame(-1) != g.Spin[3] {
		t.Fatalf("negative frame = %q", g.SpinnerFrame(-1))
	}
}

// The theme's ASCII mode is exactly what a non-UTF-8 locale gets, and it must
// be used everywhere a marker is drawn.
func TestResolvedASCIIThemeUsesASCIIMarkers(t *testing.T) {
	th := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: false})
	if th.Ellipsis() != "~" {
		t.Fatalf("ellipsis = %q", th.Ellipsis())
	}
	if got := th.Glyphs.Sel; got != ">" {
		t.Fatalf("selection glyph = %q", got)
	}
	if strings.Contains(Truncate("abcdefgh", 5, th.Ellipsis()), "…") {
		t.Fatal("UTF-8 ellipsis in ASCII mode")
	}
}

// A shared renderer can emit a glyph the TUI did not choose (view.StageList's
// ✕). ASCIISafe is the frame-wide safety net: it maps the closed decorative set
// and leaves Korean text, paths and user output untouched.
func TestASCIISafeReplacesOnlyDecorativeGlyphs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  ✕ 대상 확인 (실패)", "  ! 대상 확인 (실패)"},
		{"╭─ 기능 ─╮", "+- 기능 -+"},
		{"▸ 스택 상태", "> 스택 상태"},
		{"… 더 있음", "~ 더 있음"},
		{"✓ 실행 중", "+ 실행 중"},
		{"서비스 · 상태", "서비스 - 상태"},
		{"/Users/npnl/데이터/backup.tar", "/Users/npnl/데이터/backup.tar"},
		{"proj-web-1 | hello", "proj-web-1 | hello"},
	}
	for _, tc := range cases {
		if got := ASCIISafe(tc.in); got != tc.want {
			t.Fatalf("ASCIISafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if Width(ASCIISafe(tc.in)) != Width(tc.in) {
			t.Fatalf("ASCIISafe changed the cell width of %q", tc.in)
		}
	}
}

func TestASCIISafeLinesPreservesLineCount(t *testing.T) {
	lines := ASCIISafeLines([]string{"✓ a", "… b", ""})
	if len(lines) != 3 || lines[0] != "+ a" || lines[1] != "~ b" || lines[2] != "" {
		t.Fatalf("ASCIISafeLines = %q", lines)
	}
}
