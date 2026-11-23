package visual

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// everyLayout walks the whole matrix the review asked for: 1..40 rows, 1..20
// visible rows and every possible selection. The assertions below must hold for
// every combination, not just the happy path.
func everyLayout(t *testing.T, check func(t *testing.T, total, height, selection int)) {
	t.Helper()
	for total := 1; total <= 40; total++ {
		for height := 1; height <= 20; height++ {
			for selection := 0; selection < total; selection++ {
				selection := selection
				t.Run(fmt.Sprintf("rows=%d/height=%d/sel=%d", total, height, selection), func(t *testing.T) {
					check(t, total, height, selection)
				})
			}
		}
	}
}

// parseCounter reads the approved `first-last/total` form.
func parseCounter(t *testing.T, counter string) (first, last, total int) {
	t.Helper()
	parts := strings.Split(counter, "/")
	if len(parts) != 2 {
		t.Fatalf("counter %q is not first-last/total", counter)
	}
	span := strings.Split(parts[0], "-")
	if len(span) != 2 {
		t.Fatalf("counter %q has no range", counter)
	}
	first, err := strconv.Atoi(span[0])
	if err != nil {
		t.Fatalf("counter %q first: %v", counter, err)
	}
	last, err = strconv.Atoi(span[1])
	if err != nil {
		t.Fatalf("counter %q last: %v", counter, err)
	}
	total, err = strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("counter %q total: %v", counter, err)
	}
	return first, last, total
}

// Invariant 1: the drawn rows and the counter describe the same window. This is
// the bug the spike caught: a counter said "16/16" while twelve rows were drawn.
func TestViewportCounterMatchesDrawnRows(t *testing.T) {
	everyLayout(t, func(t *testing.T, total, height, selection int) {
		v := Viewport{Height: height, Total: total}
		v.Follow(selection)

		start, end := v.Window()
		drawn := end - start
		first, last, counterTotal := parseCounter(t, v.Counter())

		if counterTotal != total {
			t.Fatalf("counter total %d, want %d", counterTotal, total)
		}
		if first != start+1 {
			t.Fatalf("counter first %d, want %d (window %d..%d)", first, start+1, start, end)
		}
		if last != end {
			t.Fatalf("counter last %d, want %d (window %d..%d)", last, end, start, end)
		}
		if want := min(height, total); drawn != want {
			t.Fatalf("drawn rows %d, want %d", drawn, want)
		}
		// Every selection move must keep the last row drawn inside the total.
		if end > total {
			t.Fatalf("window ends at %d, past total %d", end, total)
		}
	})
}

// Invariant 2: the window never exceeds the total, at any position or size, and
// a top past the end is clamped instead of producing an empty screen.
func TestViewportWindowNeverExceedsTotal(t *testing.T) {
	everyLayout(t, func(t *testing.T, total, height, selection int) {
		v := Viewport{Height: height, Total: total}
		v.Follow(selection)
		v.End()
		if v.Top > max(total-height, 0) {
			t.Fatalf("top %d exceeds max top %d", v.Top, max(total-height, 0))
		}
		if v.Visible() > total {
			t.Fatalf("visible %d exceeds total %d", v.Visible(), total)
		}
		if v.Visible() > height {
			t.Fatalf("visible %d exceeds height %d", v.Visible(), height)
		}
		if v.First() < 1 && total > 0 && height > 0 {
			t.Fatalf("first row %d is not 1-based", v.First())
		}
		if v.Last() > total {
			t.Fatalf("last row %d exceeds total %d", v.Last(), total)
		}

		// Any overshoot from an explicit Top must clamp back into range.
		wild := Viewport{Top: total + 10, Height: height, Total: total}.Clamp()
		if wild.Top != max(total-height, 0) {
			t.Fatalf("clamped top %d, want %d", wild.Top, max(total-height, 0))
		}
	})
}

// Invariant 3: a scroll hint only exists when something is actually off screen,
// and it costs its own row instead of covering the last drawn row.
func TestViewportHidesHintWhenEverythingFits(t *testing.T) {
	for total := 1; total <= 40; total++ {
		for height := 1; height <= 20; height++ {
			v := Viewport{Height: height, Total: total}.Clamp()
			if v.Overflow() {
				if v.Hint() == "" {
					t.Fatalf("rows=%d height=%d overflows with no hint", total, height)
				}
				if strings.Contains(v.Hint(), "PgUp/PgDn") == false {
					t.Fatalf("rows=%d height=%d hint %q does not name the keys", total, height, v.Hint())
				}
				continue
			}
			if v.Hint() != "" {
				t.Fatalf("rows=%d height=%d fits but shows %q", total, height, v.Hint())
			}
		}
	}

	// A stale Top from a larger window must not invent a hint: the content
	// fits, so there is nothing to announce even though Top is non-zero.
	for _, top := range []int{1, 3, 10} {
		v := Viewport{Top: top, Height: 20, Total: 5}.Clamp()
		if v.Hint() != "" {
			t.Fatalf("top=%d: fitting content shows %q", top, v.Hint())
		}
	}

	// The hint names which side has more rows.
	v := Viewport{Top: 5, Height: 10, Total: 25}
	if got := v.Hint(); !strings.Contains(got, "↑ 5개") || !strings.Contains(got, "↓ 10개") {
		t.Fatalf("hint %q does not separate above and below", got)
	}
}

// Invariant 4: after Follow, the selection is always inside the window; the
// view moves the minimum distance and never scrolls on its own.
func TestViewportKeepsSelectionVisible(t *testing.T) {
	everyLayout(t, func(t *testing.T, total, height, selection int) {
		v := Viewport{Height: height, Total: total}
		before := v.Top
		v.Follow(selection)
		if selection < v.Top || selection >= v.Top+v.Visible() {
			t.Fatalf("selection %d outside window %d..%d", selection, v.Top, v.Top+v.Visible())
		}
		if v.Top < before {
			t.Fatalf("follow scrolled up from %d to %d", before, v.Top)
		}
		if v.Top != 0 {
			// Scrolling further down must be the minimum needed: the selection
			// is the last visible row.
			if v.Top+v.Visible()-1 != selection {
				t.Fatalf("follow put selection %d at %d..%d, not the bottom", selection, v.Top, v.Top+v.Visible())
			}
		}
	})
}

// Invariant 4, the other half: when the selection is already visible, Follow is
// a no-op. A view that recentres on every call would jump while the reader is
// scrolling through a page.
func TestViewportFollowDoesNotScrollWhenSelectionIsVisible(t *testing.T) {
	for total := 1; total <= 40; total++ {
		for height := 1; height <= 20; height++ {
			for top := 0; top <= max(total-height, 0); top++ {
				v := Viewport{Top: top, Height: height, Total: total}.Clamp()
				for _, selection := range []int{top, top + v.Visible()/2, top + v.Visible() - 1} {
					if selection < top || selection >= top+v.Visible() {
						continue
					}
					moved := v
					moved.Follow(selection)
					if moved.Top != top {
						t.Fatalf("rows=%d height=%d top=%d sel=%d: follow moved to %d",
							total, height, top, selection, moved.Top)
					}
				}
			}
		}
	}
}

// The keys move the window by a page, to the ends, and by one row, and the
// counter follows each move.
func TestViewportKeysMoveByApprovedAmounts(t *testing.T) {
	v := Viewport{Height: 10, Total: 25}
	if got := v.Counter(); got != "1-10/25" {
		t.Fatalf("initial counter = %q", got)
	}
	v.PgDn()
	if got := v.Counter(); got != "11-20/25" {
		t.Fatalf("after PgDn = %q", got)
	}
	v.PgDn()
	if got := v.Counter(); got != "16-25/25" {
		t.Fatalf("after second PgDn = %q", got)
	}
	v.PgUp()
	if got := v.Counter(); got != "6-15/25" {
		t.Fatalf("after PgUp = %q", got)
	}
	v.Home()
	if got := v.Counter(); got != "1-10/25" {
		t.Fatalf("after Home = %q", got)
	}
	v.End()
	if got := v.Counter(); got != "16-25/25" {
		t.Fatalf("after End = %q", got)
	}
	v.Move(-100)
	if got := v.Counter(); got != "1-10/25" {
		t.Fatalf("after Move(-100) = %q", got)
	}
	v.Move(100)
	if got := v.Counter(); got != "16-25/25" {
		t.Fatalf("after Move(100) = %q", got)
	}

	// A window taller than the content shows everything and no hint.
	all := Viewport{Height: 20, Total: 3}
	if got := all.Counter(); got != "1-3/3" {
		t.Fatalf("small content counter = %q", got)
	}
	if all.Overflow() {
		t.Fatal("small content reported overflow")
	}
}

// A zero-height viewport draws nothing and claims nothing; the counter must not
// say "0-0/25" as if rows were visible.
func TestViewportZeroHeightDrawsNothing(t *testing.T) {
	v := Viewport{Height: 0, Total: 25}
	if v.Visible() != 0 || v.Counter() != "" || v.Last() != 0 {
		t.Fatalf("zero height: visible=%d counter=%q", v.Visible(), v.Counter())
	}
	empty := Viewport{Height: 10, Total: 0}
	if empty.Visible() != 0 || empty.Counter() != "" || empty.Overflow() {
		t.Fatalf("empty total: visible=%d counter=%q overflow=%v", empty.Visible(), empty.Counter(), empty.Overflow())
	}
}
