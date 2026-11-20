package visual

import "strings"

// Frame is one composed screen. Header and Footer own one row each; Pinned
// lines sit directly above the footer and are always visible; Body takes the
// remaining rows. Overflow is the explicit note shown when the body does not
// fit.
//
// Pinned exists because some lines must never be the part that gets cut. The
// approved contract pins the risk sentence of a confirmation; the shell keeps
// it visible at every size instead of relying on each renderer.
type Frame struct {
	Header   string
	Body     []string
	Pinned   []string
	Footer   string
	Overflow string
}

// Compose lays the frame out at the given size.
//
// With a positive width every returned line occupies exactly that width. Body
// lines wider than the frame are truncated with the theme's explicit marker.
// When the body does not fit, its last visible row becomes the overflow note,
// so the note itself is never hidden. Pinned lines are laid out last and kept
// whenever their rows are available.
//
// With no known size (width<=0 or height<=0) Compose returns the content
// unchanged plus the header and footer, so a model built before its first
// WindowSizeMsg still renders something complete.
func Compose(th Theme, width, height int, f Frame) []string {
	ell := th.Glyphs.Ell
	if width <= 0 || height <= 0 {
		out := make([]string, 0, len(f.Body)+len(f.Pinned)+2)
		out = append(out, f.Header)
		for _, line := range f.Body {
			if width > 0 {
				line = Truncate(line, width, ell)
			}
			out = append(out, line)
		}
		for _, line := range f.Pinned {
			if width > 0 {
				line = Truncate(line, width, ell)
			}
			out = append(out, line)
		}
		out = append(out, f.Footer)
		if th.Glyphs.ASCII {
			return ASCIISafeLines(out)
		}
		return out
	}

	if height == 1 {
		return []string{Fit(f.Header, width, ell)}
	}
	if height == 2 {
		return []string{Fit(f.Header, width, ell), Fit(f.Footer, width, ell)}
	}

	rows := height - 2
	// Pinned lines get their rows first; the body gets what is left. When
	// even the pinned lines cannot all fit, the last pinned line is the one
	// kept, because callers pin in increasing importance order.
	pinned := make([]string, 0, len(f.Pinned))
	if len(f.Pinned) > rows {
		start := len(f.Pinned) - rows
		for _, line := range f.Pinned[start:] {
			pinned = append(pinned, Truncate(line, width, ell))
		}
	} else {
		for _, line := range f.Pinned {
			pinned = append(pinned, Truncate(line, width, ell))
		}
	}
	bodyRows := rows - len(pinned)

	body := make([]string, 0, bodyRows)
	for _, line := range f.Body {
		if len(body) == bodyRows {
			break
		}
		body = append(body, Truncate(line, width, ell))
	}
	if bodyRows > 0 && len(f.Body) > bodyRows {
		// The note replaces the last body row so it is never hidden and the
		// frame keeps its height.
		keep := bodyRows - 1
		if keep < 0 {
			keep = 0
		}
		if len(body) > keep {
			body = body[:keep]
		}
		body = append(body, Truncate(f.Overflow, width, ell))
	}

	blank := strings.Repeat(" ", width)
	out := make([]string, 0, height)
	out = append(out, Fit(f.Header, width, ell))
	out = append(out, body...)
	for len(out) < height-1-len(pinned) {
		out = append(out, blank)
	}
	out = append(out, pinned...)
	out = append(out, Fit(f.Footer, width, ell))
	if th.Glyphs.ASCII {
		// A non-UTF-8 locale must not receive a decorative glyph from a
		// shared renderer either, so the finished frame passes the closed
		// glyph table once.
		return ASCIISafeLines(out)
	}
	return out
}

// Shell is Compose without pinned lines.
func Shell(th Theme, width, height int, header string, body []string, footer, overflowNote string) []string {
	return Compose(th, width, height, Frame{
		Header: header, Body: body, Footer: footer, Overflow: overflowNote,
	})
}

// Facts are the last observed header values. The TUI never polls: it keeps the
// last report it saw and passes it here.
type Facts struct {
	Project     string
	Environment string
	Docker      string
	Compose     string
	// Local is the environment kind shown last (로컬). Empty hides it.
	Local string
}

// headerIndent keeps one cell of breathing room on both sides of the plate.
const headerIndent = 1

// Header renders the one-row header plate: `dcraft · project · environment` on
// the left and the observed tool facts on the right.
//
// When cells are short, the right side is dropped first (the 로컬 suffix, then
// Compose, then all tool facts), because the stack identity matters more than
// the tool versions. Only after the right side is empty does the left shrink to
// `dcraft · project` and then `dcraft`.
func Header(th Theme, width int, f Facts) string {
	left := identityText(f)
	right := factsText(f)
	if width <= 0 {
		// Before the first WindowSizeMsg the header still states the stack
		// identity; there is nothing to fit yet.
		if right == "" {
			return left
		}
		return left + "  " + right
	}

	// Right-hand candidates, richest first. The 로컬 suffix goes before the
	// Docker/Compose facts because it is derivable from the environment.
	candidates := []string{right}
	if right != "" {
		candidates = append(candidates, toolText(f, true), toolText(f, false), "")
	}
	chosen := ""
	for _, cand := range candidates {
		if fitsHeader(left, cand, width) {
			chosen = cand
			break
		}
	}
	// Then shrink the identity; the project survives the environment.
	if !fitsHeader(left, chosen, width) {
		shorter := []string{}
		if f.Project != "" {
			shorter = append(shorter, "dcraft · "+f.Project)
		}
		shorter = append(shorter, "dcraft")
		for _, cand := range shorter {
			left = cand
			if fitsHeader(left, chosen, width) {
				break
			}
		}
	}
	return paintHeader(th, width, left, chosen)
}

func identityText(f Facts) string {
	parts := []string{"dcraft"}
	if f.Project != "" {
		parts = append(parts, f.Project)
	}
	if f.Environment != "" {
		parts = append(parts, f.Environment)
	}
	return strings.Join(parts, " · ")
}

func factsText(f Facts) string {
	parts := []string{}
	if tools := toolText(f, true); tools != "" {
		parts = append(parts, tools)
	}
	if f.Local != "" {
		parts = append(parts, f.Local)
	}
	return strings.Join(parts, " · ")
}

// toolText renders the version facts. withCompose=false keeps only Docker.
func toolText(f Facts, withCompose bool) string {
	parts := []string{}
	if f.Docker != "" {
		parts = append(parts, "Docker "+f.Docker)
	}
	if withCompose && f.Compose != "" {
		parts = append(parts, "Compose "+f.Compose)
	}
	return strings.Join(parts, " · ")
}

func fitsHeader(left, right string, width int) bool {
	n := Width(left)
	if right != "" {
		n += 2 + Width(right)
	}
	return n+2*headerIndent <= width
}

func paintHeader(th Theme, width int, left, right string) string {
	gap := width - Width(left) - Width(right) - 2*headerIndent
	if gap < 0 {
		gap = 0
	}
	row := strings.Repeat(" ", headerIndent) + left
	if right != "" {
		row += strings.Repeat(" ", gap) + right
	}
	row += strings.Repeat(" ", headerIndent)
	row = Fit(row, width, th.Glyphs.Ell)
	if !th.Color {
		return row
	}
	// The plate is the whole row, so the header reads as a band even when the
	// text is short. Surface is the only background token.
	return th.PaintOn(TokenMuted, TokenSurface, row)
}

// BodyRows is the number of body rows inside a frame of the given total height.
// Header and footer each own one row.
func BodyRows(height int) int {
	if height <= 2 {
		return 0
	}
	return height - 2
}
