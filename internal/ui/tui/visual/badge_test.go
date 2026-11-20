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
