package visual

import (
	"fmt"
	"strings"
)

// Viewport is the scrolling window over a list of rows. It is exported because
// more than one screen scrolls: the status table, the result body, the service
// selection table and the confirmation modal all use the same arithmetic.
//
// Top is the first visible row (0-based), Height is how many content rows the
// screen can draw, and Total is how many rows exist. A row window never exceeds
// the total, and the counter is derived from the same window that is drawn, so
// "16/16 while twelve rows are drawn" cannot happen.
//
// Height counts content rows only. When one row must be spent on the scroll
// hint, the caller budgets it (see Fit) so the hint never covers a real row.
type Viewport struct {
	Top    int
	Height int
	Total  int
}

// Clamp returns the viewport with Top inside the legal range and negatives
// removed. Every mutating method clamps, so an arbitrary Top cannot produce an
// empty window or a counter past the total.
func (v Viewport) Clamp() Viewport {
	if v.Height < 0 {
		v.Height = 0
	}
	if v.Total < 0 {
		v.Total = 0
	}
	maxTop := v.MaxTop()
	if v.Top < 0 {
		v.Top = 0
	}
	if v.Top > maxTop {
		v.Top = maxTop
	}
	return v
}

// MaxTop is the largest Top that still fills the window; zero when everything
// fits.
func (v Viewport) MaxTop() int {
	if v.Height >= v.Total {
		return 0
	}
	return v.Total - v.Height
}

// Visible is the number of rows the window draws.
func (v Viewport) Visible() int {
	v = v.Clamp()
	if v.Total <= v.Top {
		return 0
	}
	if n := v.Total - v.Top; n < v.Height {
		return n
	}
	return v.Height
}

// Fit reserves one row for the scroll hint when the content overflows, and
// returns the content height left for the rows themselves. Callers that draw a
// hint use it so the hint gets its own row instead of covering the last row.
func (v Viewport) Fit(available int) Viewport {
	if available < 0 {
		available = 0
	}
	v.Height = available
	if v.Total > available && available > 0 {
		v.Height = available - 1
	}
	return v.Clamp()
}

// Window is the half-open row range [start, end) the window draws. end is never
// past Total.
func (v Viewport) Window() (int, int) {
	v = v.Clamp()
	return v.Top, v.Top + v.Visible()
}

// First and Last are the 1-based numbers of the first and last drawn rows. They
// are zero when nothing is drawn; the counter omits them in that case.
func (v Viewport) First() int {
	if v.Visible() == 0 {
		return 0
	}
	return v.Clamp().Top + 1
}

func (v Viewport) Last() int {
	if v.Visible() == 0 {
		return 0
	}
	return v.Clamp().Top + v.Visible()
}

// Counter is the approved range form `first-last/total`, computed from the rows
// actually drawn. An empty window has no counter: claiming `0-0/25` would
// describe rows that are not on screen.
func (v Viewport) Counter() string {
	if v.Visible() == 0 {
		return ""
	}
	return fmt.Sprintf("%d-%d/%d", v.First(), v.Last(), v.Total)
}

// Overflow reports whether any row is outside the window.
func (v Viewport) Overflow() bool {
	v = v.Clamp()
	return v.Total > v.Height
}

// Hint is the one-row statement of what is off screen. It is empty when
// everything fits, and it always names PgUp/PgDn so the row is actionable.
func (v Viewport) Hint() string {
	v = v.Clamp()
	if !v.Overflow() {
		return ""
	}
	above := v.Top
	below := v.Total - (v.Top + v.Visible())
	switch {
	case above > 0 && below > 0:
		return fmt.Sprintf("↑ %d개 · ↓ %d개 더 있음 · PgUp/PgDn", above, below)
	case above > 0:
		return fmt.Sprintf("↑ %d개 더 있음 · PgUp/PgDn", above)
	case below > 0:
		return fmt.Sprintf("↓ %d개 더 있음 · PgUp/PgDn", below)
	default:
		return ""
	}
}

// Move scrolls by delta rows and clamps.
func (v *Viewport) Move(delta int) { v.Top += delta; *v = v.Clamp() }

// PgUp scrolls one window up.
func (v *Viewport) PgUp() { v.Move(-v.page()) }

// PgDn scrolls one window down.
func (v *Viewport) PgDn() { v.Move(v.page()) }

// Home scrolls to the first row.
func (v *Viewport) Home() { v.Top = 0; *v = v.Clamp() }

// End scrolls to the last window.
func (v *Viewport) End() { v.Top = v.MaxTop(); *v = v.Clamp() }

// Follow scrolls the minimum distance that keeps selection (0-based) inside the
// window, and does nothing when it already is.
func (v *Viewport) Follow(selection int) {
	v.Top = v.Clamp().Top
	if selection < 0 {
		v.Top = 0
		*v = v.Clamp()
		return
	}
	height := v.Visible()
	if height == 0 {
		v.Top = selection
		*v = v.Clamp()
		return
	}
	switch {
	case selection < v.Top:
		v.Top = selection
	case selection >= v.Top+height:
		v.Top = selection - height + 1
	}
	*v = v.Clamp()
}

func (v Viewport) page() int {
	if v.Height <= 0 {
		return 1
	}
	return v.Height
}

// ScrollHint pads and right-aligns the hint row across width cells. It returns
// an empty string when there is nothing to say, so a caller can append it
// conditionally and never reserve a blank row.
func (v Viewport) ScrollHint(th Theme, width int) string {
	hint := v.Hint()
	if hint == "" {
		return ""
	}
	if width <= 0 {
		return hint
	}
	return Fit(strings.Repeat(" ", max(width-Width(hint), 0))+hint, width, th.Glyphs.Ell)
}
