package visual

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// The approved decision sheet requires a short-label fallback: a state that
// does not fit its column falls back to a shorter label instead of becoming an
// anonymous truncated word.
func TestBadgeFallsBackToAShortLabel(t *testing.T) {
	th := Plain()
	cases := []struct {
		kind core.BadgeKind
		text string
		want string
	}{
		{core.BadgeNotCreated, "컨테이너 없음", "○ 미생성"},
		{core.BadgeRunning, "실행 중", "✓ 실행"},
		{core.BadgeStopped, "중지됨", "■ 중지"},
	}
	for _, tc := range cases {
		table := core.Table{
			// The state column is 8 cells: too narrow for `○ 컨테이너 없음`
			// (15 cells) but wide enough for `○ 미생성` (8).
			Columns: []core.Column{
				{Header: "서비스", Weight: 60, Min: 10},
				{Header: "상태", Fixed: 8},
			},
			Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
				{Text: "api"},
				{Text: tc.text, Badge: tc.kind},
			}}},
		}
		layout := LayoutTable(th, table, 32)
		if len(layout.Rows) != 1 {
			t.Fatalf("%s: rows = %d", tc.kind, len(layout.Rows))
		}
		row := layout.Rows[0]
		if !strings.Contains(row, tc.want) {
			t.Fatalf("%s: %q does not carry the short label %q", tc.kind, row, tc.want)
		}
		if strings.Contains(row, "컨테이너…") && tc.kind == core.BadgeNotCreated {
			t.Fatalf("%s: anonymous truncation instead of the short label: %q", tc.kind, row)
		}
		for i, line := range layout.Lines() {
			if Width(line) > 32 {
				t.Fatalf("%s: row %d too wide: %q", tc.kind, i, line)
			}
		}
	}
}

// `◐ 확인 중` is nine cells and the health column is eight. The starting badge
// therefore needs a one-cell-shorter Korean fallback; without it the cell fell
// straight to the bare icon, which names no state. Every badge kind must keep a
// Korean word at the approved column width.
func TestEveryBadgeKeepsAKoreanLabelInAnEightCellColumn(t *testing.T) {
	th := Plain()
	for _, kind := range []core.BadgeKind{
		core.BadgeRunning, core.BadgeStopped, core.BadgeUnhealthy,
		core.BadgeStarting, core.BadgeNotCreated, core.BadgeRisk,
	} {
		table := core.Table{
			Columns: []core.Column{
				{Header: "서비스", Weight: 60, Min: 10},
				{Header: "상태", Fixed: 8},
			},
			Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
				{Text: "api"},
				{Text: badgeFullText(th, kind), Badge: kind},
			}}},
		}
		row := LayoutTable(th, table, 32).Rows[0]
		cell := strings.TrimSpace(row[18:])
		if Width(cell) == 0 {
			t.Fatalf("%s: empty state cell: %q", kind, row)
		}
		// A bare glyph (one cell, no Korean) is the failure this guards.
		if Width(cell) <= 1 {
			t.Fatalf("%s: fell back to the icon alone in an 8-cell column: %q", kind, row)
		}
		if !containsKorean(cell) {
			t.Fatalf("%s: no Korean label survived the fallback: %q", kind, cell)
		}
	}
}

func badgeFullText(th Theme, kind core.BadgeKind) string {
	switch kind {
	case core.BadgeRunning:
		return "실행 중"
	case core.BadgeStopped:
		return "중지됨"
	case core.BadgeUnhealthy:
		return "비정상"
	case core.BadgeStarting:
		return "확인 중"
	case core.BadgeNotCreated:
		return "컨테이너 없음"
	case core.BadgeRisk:
		return "위험"
	default:
		return ""
	}
}

func containsKorean(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}
