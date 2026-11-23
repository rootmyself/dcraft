package visual

import (
	"strings"
	"testing"
)

// The approved scrollbar gutter is one cell in the last content column, a blank
// track and a solid thumb in the accent token. A band that fits paints nothing.
func TestScrollbarPaintsASolidThumbInTheGutter(t *testing.T) {
	th := Plain()
	gutter := Scrollbar(th, 10, 0, 5, 20)
	if len(gutter) != 10 {
		t.Fatalf("gutter rows = %d, want 10", len(gutter))
	}
	thumb := 0
	for _, cell := range gutter {
		if Width(cell) != 1 {
			t.Fatalf("gutter cell %q is %d cells", cell, Width(cell))
		}
		if strings.Contains(cell, th.Glyphs.Thumb) {
			thumb++
		}
	}
	if thumb < 1 {
		t.Fatalf("no thumb painted: %q", gutter)
	}
	// The thumb is at the top for a window at the top.
	if !strings.Contains(gutter[0], th.Glyphs.Thumb) {
		t.Fatalf("thumb is not at the top: %q", gutter)
	}
	// A window at the end puts the thumb at the bottom.
	last := Scrollbar(th, 10, 15, 5, 20)
	if !strings.Contains(strings.Join(last, ""), th.Glyphs.Thumb) {
		t.Fatalf("no thumb at the end: %q", last)
	}
	if !strings.Contains(last[len(last)-1], th.Glyphs.Thumb) {
		t.Fatalf("the last row is not the thumb: %q", last)
	}
}

// A gutter has no business on a band that fits: it would claim a position that
// does not exist.
func TestScrollbarIsAbsentWhenEverythingFits(t *testing.T) {
	th := Plain()
	if got := Scrollbar(th, 10, 0, 10, 10); len(got) != 0 {
		t.Fatalf("gutter drawn for a fitting band: %q", got)
	}
	if got := Scrollbar(th, 0, 0, 0, 0); len(got) != 0 {
		t.Fatalf("gutter drawn with no rows: %q", got)
	}
}

// The ASCII glyph set uses `#` and never the UTF-8 block.
func TestScrollbarASCIIUsesHash(t *testing.T) {
	ascii := ASCIIGlyphs()
	th := Theme{Palette: DarkPalette(), Glyphs: ascii}
	gutter := Scrollbar(th, 6, 0, 3, 12)
	joined := strings.Join(gutter, "")
	if !strings.Contains(joined, "#") {
		t.Fatalf("ASCII gutter has no #: %q", gutter)
	}
	if strings.Contains(joined, "█") {
		t.Fatalf("UTF-8 thumb leaked into ASCII: %q", gutter)
	}
}

// Every gutter cell is exactly one display cell, so appending it to a row can
// never change the frame's width contract.
func TestScrollbarCellsAreOneCellWide(t *testing.T) {
	for _, th := range []Theme{Plain(), {Palette: DarkPalette(), Glyphs: ASCIIGlyphs()}} {
		for _, cell := range Scrollbar(th, 12, 3, 6, 30) {
			if got := Width(cell); got != 1 {
				t.Fatalf("gutter cell %q is %d cells", cell, got)
			}
		}
	}
}
