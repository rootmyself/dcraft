package visual

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// modalBody is the background the panel is composed over: enough rows that a
// long panel still fits.
func modalBody(rows int) []string {
	out := make([]string, rows)
	for i := range out {
		out[i] = fmt.Sprintf("배경 행 %d · 실행 중", i+1)
	}
	return out
}

func modalFrame(th Theme, w, h int) []string {
	return Compose(th, w, h, Frame{
		Header: Header(th, w, fullFacts()),
		Body:   modalBody(h),
		Footer: Footer(th, PhaseConfirm, w),
	})
}

func panelSpec(rows, risk int) ModalSpec {
	spec := ModalSpec{Title: "확인", Badge: "위험", RiskLabel: "위험"}
	for i := 0; i < rows; i++ {
		spec.Rows = append(spec.Rows, ModalRow{
			Label: fmt.Sprintf("라벨%d:", i+1),
			Value: fmt.Sprintf("값 %d", i+1),
		})
	}
	for i := 0; i < risk; i++ {
		spec.Risk = append(spec.Risk, fmt.Sprintf("위험 문장 %d", i+1))
	}
	return spec
}

func panelBounds(t *testing.T, lines []string, th Theme) (top, bottom, left, width int) {
	t.Helper()
	top, bottom = -1, -1
	for i, line := range lines {
		if top < 0 && strings.Contains(line, th.Glyphs.TL) && strings.Contains(line, th.Glyphs.TR) {
			top = i
			left = Width(line[:strings.Index(line, th.Glyphs.TL)])
			width = Width(line) - 2*left
			if !strings.Contains(line, th.Glyphs.BL) {
				continue
			}
		}
		if top >= 0 && strings.Contains(line, th.Glyphs.BL) && strings.Contains(line, th.Glyphs.BR) {
			bottom = i
			break
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("panel borders not found:\n%s", strings.Join(lines, "\n"))
	}
	return top, bottom, left, width
}

// The panel is centered in the body region, keeps the approved width and never
// produces a line wider than the terminal.
func TestModalIsCenteredAndFitsEveryWidth(t *testing.T) {
	th := Plain()
	for _, size := range [][2]int{{120, 36}, {80, 24}, {48, 20}, {40, 18}} {
		w, h := size[0], size[1]
		frame := modalFrame(th, w, h)
		spec := panelSpec(9, 1)
		out := ComposeModal(th, w, h, frame, spec)
		if len(out) != h {
			t.Fatalf("%dx%d: %d rows", w, h, len(out))
		}
		for i, line := range out {
			if Width(line) != w {
				t.Fatalf("%dx%d row %d = %d cells: %q", w, h, i, Width(line), line)
			}
		}
		top, bottom, left, width := panelBounds(t, out, th)
		if want := ModalWidth(w); width != want {
			t.Fatalf("%dx%d: panel width = %d, want %d", w, h, width, want)
		}
		right := w - left - width
		if d := left - right; d < -1 || d > 1 {
			t.Fatalf("%dx%d: panel not centered horizontally: left=%d right=%d", w, h, left, right)
		}
		above := top - 1
		below := (h - 1) - bottom - 1
		if d := above - below; d < -1 || d > 1 {
			t.Fatalf("%dx%d: panel not centered vertically: above=%d below=%d", w, h, above, below)
		}
		if out[0] != frame[0] || out[h-1] != frame[h-1] {
			t.Fatalf("%dx%d: header or footer changed", w, h)
		}
	}
}

// The panel's height follows its content: a short panel does not fill the
// screen, and the approved width formula holds at every breakpoint.
func TestModalWidthFollowsTheApprovedFormula(t *testing.T) {
	cases := []struct{ term, want int }{
		{200, 76}, {120, 76}, {80, 72}, {48, 40}, {40, 40}, {30, 30},
	}
	for _, tc := range cases {
		if got := ModalWidth(tc.term); got != tc.want {
			t.Fatalf("ModalWidth(%d) = %d, want %d", tc.term, got, tc.want)
		}
	}
}

// The risk band is clamped to clamp(innerHeight/8, 1, 3) rows, is the last
// thing above the bottom border, and states the cut with an ellipsis. The
// clipped remainder is reachable in the scroll band under the approved
// "위험 · 이어서" heading.
func TestModalPinsTheRiskBandAndKeepsTheRestReachable(t *testing.T) {
	th := Plain()
	cases := []struct {
		w, h    int
		riskRow int
	}{
		{120, 36, 3},
		{80, 24, 2},
		{48, 20, 2},
		{40, 18, 1},
	}
	for _, tc := range cases {
		frame := modalFrame(th, tc.w, tc.h)
		spec := panelSpec(9, 0)
		for i := 0; i < 9; i++ {
			spec.Risk = append(spec.Risk, fmt.Sprintf("위험 문장 %d: 복원이 끝나면 되돌릴 수 없음 끝%d", i+1, i+1))
		}
		out := ComposeModal(th, tc.w, tc.h, frame, spec)
		_, bottom, _, _ := panelBounds(t, out, th)
		// The pinned risk rows sit between the danger divider and the bottom
		// border; the divider is the last full-width rule inside the panel.
		divider := -1
		for i := bottom - 1; i > 0; i-- {
			if strings.Contains(ansi.Strip(out[i]), th.Glyphs.V+strings.Repeat(th.Glyphs.H, 10)) {
				divider = i
				break
			}
		}
		if divider < 0 {
			t.Fatalf("%dx%d: risk divider missing:\n%s", tc.w, tc.h, strings.Join(out, "\n"))
		}
		riskRows := bottom - divider - 1
		if riskRows != tc.riskRow {
			t.Fatalf("%dx%d: pinned risk rows = %d, want %d:\n%s", tc.w, tc.h, riskRows, tc.riskRow, strings.Join(out, "\n"))
		}
		if !strings.Contains(ansi.Strip(out[divider+1]), spec.RiskLabel) {
			t.Fatalf("%dx%d: risk label is not in the pinned band: %q", tc.w, tc.h, ansi.Strip(out[divider+1]))
		}
		last := out[bottom-1]
		if !strings.Contains(last, th.Glyphs.Ell) {
			t.Fatalf("%dx%d: truncated risk band has no explicit cut: %q", tc.w, tc.h, last)
		}
		// Nothing about the risk is deleted: every scroll position together
		// shows the continuation heading and the last risk sentence.
		reached := strings.Join(out, "\n")
		for top := 0; top <= 400; top += 5 {
			spec.ScrollTop = top
			scrolled := ComposeModal(th, tc.w, tc.h, frame, spec)
			reached += "\n" + strings.Join(scrolled, "\n")
			for i, line := range scrolled {
				if Width(line) > tc.w {
					t.Fatalf("%dx%d row %d too wide: %q", tc.w, tc.h, i, line)
				}
			}
		}
		if !strings.Contains(reached, "위험 · 이어서") {
			t.Fatalf("%dx%d: risk continuation heading is unreachable", tc.w, tc.h)
		}
		if !strings.Contains(reached, "끝9") {
			t.Fatalf("%dx%d: last risk sentence is unreachable", tc.w, tc.h)
		}
	}
}

// The position row reserves its own row only when the content scrolls, and it
// states the approved `position · keys` form from the rows actually drawn.
func TestModalPositionRowOnlyWhenContentScrolls(t *testing.T) {
	th := Plain()
	frame := modalFrame(th, 120, 36)
	short := ComposeModal(th, 120, 36, frame, panelSpec(3, 1))
	if strings.Contains(strings.Join(short, "\n"), "PgUp/PgDn") {
		t.Fatalf("short panel advertised scrolling:\n%s", strings.Join(short, "\n"))
	}

	long := panelSpec(60, 1)
	out := ComposeModal(th, 120, 36, frame, long)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "1/") || !strings.Contains(joined, "· PgUp/PgDn") {
		t.Fatalf("position row missing:\n%s", joined)
	}
	long.ScrollTop = 10
	out = ComposeModal(th, 120, 36, frame, long)
	if !strings.Contains(strings.Join(out, "\n"), "11/") {
		t.Fatalf("scrolled position not drawn:\n%s", strings.Join(out, "\n"))
	}
}

// The clickable button cells and the drawn button text come from one placement
// helper, so every returned rect must overlap the "[ label ]" text it claims.
// A second size proves the geometry is not tuned to one terminal.
func TestModalButtonRectsMatchTheDrawnButtons(t *testing.T) {
	th := Plain()
	for _, size := range [][2]int{{80, 24}, {40, 18}} {
		w, h := size[0], size[1]
		spec := panelSpec(9, 1)
		spec.Buttons = []string{"승인", "취소"}
		out := ComposeModal(th, w, h, modalFrame(th, w, h), spec)
		rects := ModalButtonRects(th, w, h, spec)
		if len(rects) != len(spec.Buttons) {
			t.Fatalf("%dx%d: %d rects, want %d", w, h, len(rects), len(spec.Buttons))
		}
		for _, rect := range rects {
			if rect.Y < 0 || rect.Y >= h {
				t.Fatalf("%dx%d: button %d row %d outside the frame", w, h, rect.Index, rect.Y)
			}
			line := ansi.Strip(out[rect.Y])
			if rect.X < 0 || rect.X+rect.Width > Width(line) {
				t.Fatalf("%dx%d: button %d spans %d..%d of a %d-cell row",
					w, h, rect.Index, rect.X, rect.X+rect.Width, Width(line))
			}
			cell := cellsAt(line, rect.X, rect.Width)
			want := "[ " + spec.Buttons[rect.Index] + " ]"
			if cell != want {
				t.Fatalf("%dx%d: button %d covers %q, want %q\n%s",
					w, h, rect.Index, cell, want, strings.Join(out, "\n"))
			}
		}
	}
}

// cellsAt returns the drawn cells [start, start+width) of a plain row.
func cellsAt(line string, start, width int) string {
	_, rest := splitCells(line, start)
	part, _ := splitCells(rest, width)
	return part
}

// The dim contract: the body keeps its text but loses its colors; the header
// and the keycap footer keep their original contrast.
func TestComposeModalDimsOnlyTheBody(t *testing.T) {
	th := Resolve(Inputs{ThemeSetting: "dark", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true})
	frame := modalFrame(th, 120, 36)
	spec := panelSpec(3, 1) // short panel: row 1 of the body stays outside it
	out := ComposeModal(th, 120, 36, frame, spec)

	if out[0] != frame[0] {
		t.Fatal("header lost its contrast")
	}
	if out[35] != frame[35] {
		t.Fatal("footer lost its contrast")
	}
	// A body row the panel does not cover is exactly the muted paint of the
	// same text: the dim lowers the foreground, it does not delete content.
	want := th.Paint(TokenMuted, ansi.Strip(frame[1]))
	if out[1] != want {
		t.Fatalf("body row is not dimmed to muted:\n got %q\nwant %q", out[1], want)
	}
	top, _, left, width := panelBounds(t, out, th)
	covered := out[top]
	plain := ansi.Strip(covered)
	if !strings.Contains(plain, spec.Title) {
		t.Fatalf("panel title missing from its own row: %q", plain)
	}
	if left > 0 {
		// The gutter outside the panel is dimmed background, not the frame's
		// own text.
		if !strings.Contains(covered, th.Paint(TokenMuted, "")) && !strings.HasPrefix(covered, th.Paint(TokenMuted, " ")) {
			t.Fatalf("panel gutter is not muted: %q", covered)
		}
	}
	if Width(covered) != 120 || width == 0 {
		t.Fatalf("panel row width = %d", Width(covered))
	}
	// The panel sits on the only background token: its rows carry the surface
	// plate so it reads as raised even with color stripped.
	r, g, b := HexRGB(th.Palette.Surface)
	if !strings.Contains(covered, fmt.Sprintf("48;2;%d;%d;%d", r, g, b)) {
		t.Fatalf("panel row has no surface plate: %q", covered)
	}
}

// An unknown size (before the first WindowSizeMsg) still renders the whole
// confirmation content between the header and the footer, with no geometry
// guessed.
func TestComposeModalWithoutASizeKeepsTheContent(t *testing.T) {
	th := Plain()
	frame := []string{"dcraft", "body", Footer(th, PhaseConfirm, 0)}
	spec := panelSpec(4, 1)
	out := ComposeModal(th, 0, 0, frame, spec)
	joined := strings.Join(out, "\n")
	for _, want := range []string{"dcraft", "라벨1:", "값 4", "위험 문장 1", "[esc] 취소"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sizeless panel missing %q:\n%s", want, joined)
		}
	}
}

// The panel gutters are the dimmed background. A wide glyph in the background
// that straddles an edge must be dropped, not kept whole: keeping it whole
// makes the row one cell wider than the terminal, which wraps and corrupts the
// frame. This is the Korean-text case the contract singles out.
//
// The shift loop matters: a syllable only straddles the boundary at particular
// alignments, so a test with one alignment can pass while the bug is present.
func TestComposeModalGuttersNeverOverflowOnStraddlingWideGlyphs(t *testing.T) {
	th := Plain()
	for _, w := range []int{40, 48, 80, 120, 200} {
		h := 24
		for shift := 0; shift < 8; shift++ {
			prefix := strings.Repeat(" ", shift)
			body := make([]string, h)
			for i := range body {
				body[i] = Fit(prefix+strings.Repeat("가나다라마", 40), w, th.Glyphs.Ell)
			}
			frame := Compose(th, w, h, Frame{
				Header: Header(th, w, fullFacts()),
				Body:   body,
				Footer: Footer(th, PhaseConfirm, w),
			})
			out := ComposeModal(th, w, h, frame, panelSpec(9, 1))
			if len(out) != h {
				t.Fatalf("%d columns: %d rows", w, len(out))
			}
			for i, line := range out {
				if got := Width(line); got != w {
					t.Fatalf("%d columns shift %d row %d = %d cells: %q", w, shift, i, got, ansi.Strip(line))
				}
			}
		}
	}
}
