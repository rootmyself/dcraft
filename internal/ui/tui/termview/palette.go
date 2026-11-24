package termview

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// cellPalette encodes a cell's paint as the terminal sequence that reproduces it.
//
// The child asked for a colour in its own terms: `31` means the terminal's first
// palette red, and `38;5;208` means palette entry 208. Re-encoding those as
// 24-bit values would change what the terminal is asked to draw (a palette
// terminal would have to approximate, and a themed terminal would lose its own
// red), so each form passes through in the form it arrived.
type cellPalette struct{}

func (cellPalette) SGR(c Color) string {
	var codes []string
	for _, attr := range []struct {
		bit  uint8
		code string
	}{
		{AttrBold, "1"},
		{AttrFaint, "2"},
		{AttrItalic, "3"},
		{AttrBlink, "5"},
		{AttrRapidBlink, "6"},
		{AttrReverse, "7"},
		{AttrConceal, "8"},
		{AttrStrikethrough, "9"},
	} {
		if c.Attrs&attr.bit != 0 {
			codes = append(codes, attr.code)
		}
	}
	if sgr := colorSGR(c.Fg, false); sgr != "" {
		codes = append(codes, sgr)
	}
	if sgr := colorSGR(c.Bg, true); sgr != "" {
		codes = append(codes, sgr)
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// colorSGR renders one colour in the form the child asked for. A nil colour means
// "the terminal's default", so it emits nothing and the cell inherits the
// frame's own paint.
func colorSGR(c color.Color, background bool) string {
	if c == nil {
		return ""
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		// 0-7 are the normal slots and 8-15 the bright ones; the background form
		// is the foreground form plus ten.
		base, n := 30, int(v)
		if background {
			base = 40
		}
		if n >= 8 {
			base += 60
			n -= 8
		}
		return strconv.Itoa(base + n)
	case ansi.ExtendedColor:
		// The 256-colour form: 38;5;n for the foreground, 48;5;n for the
		// background.
		prefix := 38
		if background {
			prefix = 48
		}
		return fmt.Sprintf("%d;5;%d", prefix, int(v))
	}
	// A 24-bit colour has no named form; emit its channels. RGBA returns 16-bit
	// channels, so the high byte is the 8-bit value.
	r, g, b, _ := c.RGBA()
	prefix := 38
	if background {
		prefix = 48
	}
	return fmt.Sprintf("%d;2;%d;%d;%d", prefix, r>>8, g>>8, b>>8)
}

// NewPalette returns the palette the TUI uses for a terminal that may emit paint.
// It is a function rather than a package-level value so a caller cannot depend on
// a shared mutable one.
func NewPalette() Palette { return cellPalette{} }
