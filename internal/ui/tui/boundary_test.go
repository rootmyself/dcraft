package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The boundary cases the contract singles out: the 40-column frame, Korean
// display width, long-path truncation, and a 25-service stack that must stay
// reachable. They are asserted on the real model frame, not on a helper.

// 40 columns is the smallest approved width. The frame must stay exactly 40
// cells wide on every row, keep its header and keycaps, and never split a
// Korean syllable.
func TestBoundary40ColumnsFrameIntegrity(t *testing.T) {
	for _, screen := range goldenScreens() {
		screen := screen
		t.Run(screen.name, func(t *testing.T) {
			m := screen.build(t)
			resize(t, m, 40, 18)
			lines := viewLines(t, m)
			if len(lines) != 18 {
				t.Fatalf("%d rows, want 18", len(lines))
			}
			for i, line := range lines {
				if got := visual.Width(line); got > 40 {
					t.Fatalf("row %d is %d cells: %q", i, got, line)
				}
				if !validRunes(line) {
					t.Fatalf("row %d is not valid UTF-8: %q", i, line)
				}
			}
			// The header keeps the stack identity and the footer keeps a key:
			// decoration may yield, these may not.
			if !strings.Contains(lines[0], "dcraft") {
				t.Fatalf("header lost the identity: %q", lines[0])
			}
			if !strings.Contains(lines[len(lines)-1], "[") {
				t.Fatalf("footer lost its keycaps: %q", lines[len(lines)-1])
			}
		})
	}
}

func validRunes(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// Korean text is measured in display cells: a syllable is two cells, so a
// padding or truncation decision based on rune count would misalign the frame.
// The test states the difference explicitly at the widths where it bites.
func TestBoundaryKoreanDisplayWidth(t *testing.T) {
	cases := []struct {
		text  string
		cells int
	}{
		{"스택 상태", 9},        // 4 syllables + a space
		{"서비스", 6},          // 3 syllables
		{"dcraft · 스택", 13}, // mixed
		{"데이터 복원 ▲ 위험", 18}, // icon is one cell, Korean is two
		{"✓ 실행 중", 9},       // icon + space + three syllables
	}
	for _, tc := range cases {
		if got := visual.Width(tc.text); got != tc.cells {
			t.Fatalf("Width(%q) = %d, want %d", tc.text, got, tc.cells)
		}
		if runes := len([]rune(tc.text)); runes == tc.cells {
			t.Fatalf("%q has %d runes and %d cells: the test does not distinguish them", tc.text, runes, tc.cells)
		}
	}

	// The frame's rows are exactly the terminal width even though the Korean
	// content has fewer runes than cells: a rune-count layout would leave the
	// rows short.
	features := goldenBaseFeatures()
	m := newGoldenModel(t, features...)
	resize(t, m, 80, 30)
	for i, line := range viewLines(t, m) {
		if got := visual.Width(line); got != 80 {
			t.Fatalf("row %d = %d cells, want exactly 80: %q", i, got, line)
		}
	}

	// Truncation drops the whole syllable instead of splitting it, and marks
	// the cut.
	got := visual.Truncate("스택상태확인", 6, "…")
	if got != "스택…" {
		t.Fatalf("Truncate = %q, want the whole syllables plus the marker", got)
	}
	if visual.Width(got) > 6 {
		t.Fatalf("Truncate = %d cells, over the limit", visual.Width(got))
	}
}

// A long path is shortened with an explicit ellipsis and stays reachable in
// full through the detail view. It is never wrapped: a wrapped path cannot be
// read back.
func TestBoundaryLongPathTruncation(t *testing.T) {
	longImage := "registry.example.com/team/a-very-long-image-name-that-does-not-fit:2026-11-20"
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: &core.Table{
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "이미지", Weight: 45, Min: 16, Max: 34},
		},
		Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
			{Text: "api"},
			{Text: longImage, Detail: longImage},
		}}},
	}}}}
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: doc}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 60, 24)

	view := m.View()
	if !strings.Contains(view, "…") {
		t.Fatalf("the long image was not marked as shortened:\n%s", view)
	}
	if strings.Contains(view, longImage) {
		t.Fatalf("the full image already fits; the test proves nothing:\n%s", view)
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 60 {
			t.Fatalf("row %d overflows 60 columns: %q", i, line)
		}
	}

	// The full value is one key away, wraps at the frame width, and the footer
	// advertised that key.
	if !strings.Contains(view, "[d] 상세") {
		t.Fatalf("no detail key advertised:\n%s", view)
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = nm.(*Model)
	if !m.detailOpen {
		t.Fatalf("d did not open the detail view:\n%s", m.View())
	}
	joined := strings.Join(strings.Fields(strings.ReplaceAll(m.View(), "\n", " ")), "")
	if !strings.Contains(joined, longImage) {
		t.Fatalf("the detail view lost the full path:\n%s", m.View())
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 60 {
			t.Fatalf("detail row %d overflows: %q", i, line)
		}
	}
}

// 25 services is the large-stack case: every row must be reachable, the header
// must never scroll away, and the counter must describe the rows actually
// drawn.
func TestBoundary25ServiceScroll(t *testing.T) {
	m := scopeModel(t, manyServices(25))
	for _, size := range [][2]int{{80, 24}, {40, 18}, {120, 30}} {
		resize(t, m, size[0], size[1])
		seen := map[string]bool{}
		header := tableHeaderLine(t, viewOf(m))
		// Walk down to the last row and back to the first.
		for i := 0; i <= len(manyServices(25)); i++ {
			view := viewOf(m)
			if got := tableHeaderLine(t, view); got != header {
				t.Fatalf("%dx%d row %d: the table header moved:\n%q\n%q", size[0], size[1], i, header, got)
			}
			for _, svc := range manyServices(25) {
				if strings.Contains(view, svc.Name) {
					seen[svc.Name] = true
				}
			}
			assertFits(t, m, size[0], size[1])
			if i < 25 {
				pressDown(m)
			}
		}
		for _, svc := range manyServices(25) {
			if !seen[svc.Name] {
				t.Fatalf("%dx%d: %q was never visible", size[0], size[1], svc.Name)
			}
		}
		// Home returns to the pinned whole-stack row and the first service.
		press(m, tea.KeyMsg{Type: tea.KeyHome})
		if view := viewOf(m); !strings.Contains(view, "svc-00") {
			t.Fatalf("%dx%d: home did not return to the first row:\n%s", size[0], size[1], view)
		}
	}
}

// The status result table has the same contract on a 25-row stack: G reaches
// the last row, g returns to the first, and the header stays.
func TestBoundary25ServiceResultScroll(t *testing.T) {
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: bigStackDocument(25)}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 24)

	if strings.Contains(m.View(), "svc-25") {
		t.Fatalf("the last row is visible before scrolling:\n%s", m.View())
	}
	pressRunes(t, m, "G")
	view := m.View()
	if !strings.Contains(view, "svc-25") {
		t.Fatalf("G did not reach the last row:\n%s", view)
	}
	if !strings.Contains(view, "서비스") {
		t.Fatalf("the table header scrolled away:\n%s", view)
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 80 {
			t.Fatalf("row %d overflows: %q", i, line)
		}
	}
	pressRunes(t, m, "g")
	if !strings.Contains(m.View(), "svc-1") {
		t.Fatalf("g did not return to the first row:\n%s", m.View())
	}
}
