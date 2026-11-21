package visual

import "strings"

// Frame is one composed screen. Header and Footer own one row each; Body takes
// the remaining rows. Overflow is the explicit note shown when the body does not
// fit.
//
// There is no frame-level pinned band: the one pinned statement in the product
// is the confirmation risk, and it lives inside the modal panel, where it cannot
// be scrolled away and cannot be drawn twice.
type Frame struct {
	Header string
	// Left is a prepared pane (already exactly as tall and wide as it wants
	// to be) shown to the left of the body. The body keeps the remaining
	// width after the approved one-cell pane gap; header and footer still
	// span the whole terminal.
	Left     []string
	Body     []string
	Footer   string
	Overflow string
}

// Compose lays the frame out at the given size.
//
// With a positive width every returned line occupies exactly that width. Body
// lines wider than the frame are truncated with the theme's explicit marker.
// When the body does not fit, its last visible row becomes the overflow note,
// so the note itself is never hidden.
//
// With no known size (width<=0 or height<=0) Compose returns the content
// unchanged plus the header and footer, so a model built before its first
// WindowSizeMsg still renders something complete.
func Compose(th Theme, width, height int, f Frame) []string {
	ell := th.Glyphs.Ell
	if width <= 0 || height <= 0 {
		out := make([]string, 0, len(f.Body)+2)
		out = append(out, f.Header)
		for _, line := range f.Body {
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

	bodyRows := height - 2

	// A left pane owns a fixed slice of every body row. The body is measured
	// against what is left, so a content line can never cross the pane.
	leftWidth := 0
	if len(f.Left) > 0 {
		leftWidth = Width(f.Left[0]) + 1
	}
	bodyWidth := width - leftWidth
	if bodyWidth < 1 {
		bodyWidth = 1
	}

	body := make([]string, 0, bodyRows)
	for _, line := range f.Body {
		if len(body) == bodyRows {
			break
		}
		body = append(body, Truncate(line, bodyWidth, ell))
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
		body = append(body, Truncate(f.Overflow, bodyWidth, ell))
	}

	blank := strings.Repeat(" ", width)
	out := make([]string, 0, height)
	out = append(out, Fit(f.Header, width, ell))
	for i := 0; i < bodyRows; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		if leftWidth == 0 {
			out = append(out, Fit(line, width, ell))
			continue
		}
		pane := ""
		if i < len(f.Left) {
			pane = f.Left[i]
		}
		// The pane is padded to its own width and followed by exactly one
		// gap cell; the content then fills the rest of the row.
		out = append(out, Fit(pane, leftWidth-1, ell)+" "+Fit(line, bodyWidth, ell))
	}
	for len(out) < height-1 {
		out = append(out, blank)
	}
	out = append(out, Fit(f.Footer, width, ell))
	if th.Glyphs.ASCII {
		// A non-UTF-8 locale must not receive a decorative glyph from a
		// shared renderer either, so the finished frame passes the closed
		// glyph table once.
		return ASCIISafeLines(out)
	}
	return out
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
