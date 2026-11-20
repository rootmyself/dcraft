package visual

import "testing"

// Breakpoints are decided from width and height only, never from content.
func TestBreakpointSelection(t *testing.T) {
	cases := []struct {
		w, h int
		want Mode
	}{
		{200, 50, ModeWide},
		{120, 36, ModeWide},
		{100, 30, ModeWide},
		{99, 30, ModeMedium},
		{80, 24, ModeMedium},
		{60, 18, ModeMedium},
		{59, 30, ModeCompact},
		{48, 20, ModeCompact},
		{40, 18, ModeCompact},
		{120, 17, ModeCompact},
		{100, 17, ModeCompact},
		{0, 0, ModeCompact},
	}
	for _, tc := range cases {
		if got := PickMode(tc.w, tc.h); got != tc.want {
			t.Fatalf("PickMode(%d, %d) = %v, want %v", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestSidebarWidthFormula(t *testing.T) {
	cases := []struct {
		w    int
		want int
	}{
		{100, 26},
		{120, 26},
		{140, 30},
		{200, 30},
		{118, 26},
		{136, 29},
	}
	for _, tc := range cases {
		if got := SidebarWidth(tc.w); got != tc.want {
			t.Fatalf("SidebarWidth(%d) = %d, want %d", tc.w, got, tc.want)
		}
	}
}

// The contract is exact: 100 -> sidebar 26 + gap 1 + content 73.
func TestWidePaneGeometryLeavesTheContentColumnUsable(t *testing.T) {
	for _, w := range []int{100, 120, 140, 200} {
		content := ContentWidth(w, PickMode(w, 30))
		if content < 69 {
			t.Fatalf("w=%d: content column = %d, want >= 69", w, content)
		}
		if content != w-SidebarWidth(w)-1 {
			t.Fatalf("w=%d: content = %d, want %d", w, content, w-SidebarWidth(w)-1)
		}
	}
	if got := ContentWidth(100, ModeWide); got != 73 {
		t.Fatalf("100 cols content = %d, want 73", got)
	}
	if got := ContentWidth(120, ModeWide); got != 93 {
		t.Fatalf("120 cols content = %d, want 93", got)
	}
	if got := ContentWidth(140, ModeWide); got != 109 {
		t.Fatalf("140 cols content = %d, want 109", got)
	}
}

func TestMediumAndCompactUseTheFullWidth(t *testing.T) {
	if got := ContentWidth(80, ModeMedium); got != 80 {
		t.Fatalf("medium content = %d", got)
	}
	if got := ContentWidth(48, ModeCompact); got != 48 {
		t.Fatalf("compact content = %d", got)
	}
}

func TestCompactDropsDecoration(t *testing.T) {
	if ModeCompact.Decorated() {
		t.Fatal("compact must drop borders and descriptions")
	}
	if !ModeWide.Decorated() || !ModeMedium.Decorated() {
		t.Fatal("wide and medium keep decoration")
	}
}
