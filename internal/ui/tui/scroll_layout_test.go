package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The regression cases the review reproduced. Each one is asserted on the real
// model frame: the counter, the hint, the footer chip and the key handler must
// all describe the rows the frame actually draws. They were written against the
// broken layout (a tail trim and a nav row invisible to the viewport), so they
// fail before the fix and stay as the contract after it.

// at every size where the status result overflows, the drawn service rows equal
// the viewport's visible rows, and the hint only appears when a row is outside
// the window.
func TestResultViewportMatchesDrawnRowsAtEveryApprovedSize(t *testing.T) {
	sizes := [][2]int{
		{120, 18}, {99, 24}, {98, 24}, {90, 24}, {80, 24}, {120, 30}, {120, 40},
	}
	for _, size := range sizes {
		size := size
		t.Run(sizeName(size[0], size[1]), func(t *testing.T) {
			m := statusResultModel(t)
			resize(t, m, size[0], size[1])

			vp := m.currentViewport()
			drawn := drawnRegionRows(m)
			if drawn != vp.Visible() {
				t.Fatalf("the scroll region draws %d rows, viewport says %d (counter %q):\n%s",
					drawn, vp.Visible(), vp.Counter(), m.View())
			}
			// A service row the counter counts must be on screen when the table
			// band itself is the window.
			if svcRows := drawnServiceRows(m); svcRows > 0 && svcRows > vp.Visible() {
				t.Fatalf("frame draws %d service rows, viewport allows %d:\n%s", svcRows, vp.Visible(), m.View())
			}
			// The overflow hint and the scroll state agree in both directions.
			hasHint := strings.Contains(m.View(), "더 있음")
			if hasHint != m.bodyScrolls() {
				t.Fatalf("hint=%v bodyScrolls=%v (viewport %+v):\n%s",
					hasHint, m.bodyScrolls(), vp, m.View())
			}
			// A scrollable result advertises the scroll key in the footer.
			if m.bodyScrolls() && !strings.Contains(m.View(), "PgUp/PgDn") {
				t.Fatalf("scrollable result has no scroll key:\n%s", m.View())
			}
		})
	}
}

// The key handler must move the window whenever the frame says there is more
// below. This is the direct regression: at 99x24 the hint was drawn but
// bodyScrolls() was false, so PgDn did nothing.
func TestResultScrollKeysWorkWheneverTheHintIsDrawn(t *testing.T) {
	for _, size := range [][2]int{{120, 18}, {99, 24}, {98, 24}, {90, 24}} {
		size := size
		t.Run(sizeName(size[0], size[1]), func(t *testing.T) {
			m := statusResultModel(t)
			resize(t, m, size[0], size[1])
			if !strings.Contains(m.View(), "↓") {
				t.Skipf("nothing below the window at %dx%d:\n%s", size[0], size[1], m.View())
			}
			before := m.currentViewport().Top
			nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			m = nm.(*Model)
			after := m.currentViewport().Top
			if after <= before {
				t.Fatalf("PgDn did not move the window (%d -> %d):\n%s", before, after, m.View())
			}
			// G reaches the last row and the header stays.
			pressRunes(t, m, "G")
			if !strings.Contains(m.View(), "legacy") && !strings.Contains(m.View(), "svc-") {
				t.Fatalf("G did not reach the last row:\n%s", m.View())
			}
			if !strings.Contains(m.View(), "서비스") {
				t.Fatalf("the table header scrolled away:\n%s", m.View())
			}
			if got := drawnRegionRows(m); got != m.currentViewport().Visible() {
				t.Fatalf("after G the scroll region draws %d rows, viewport says %d:\n%s",
					got, m.currentViewport().Visible(), m.View())
			}
		})
	}
}

// Medium layouts used to omit the navigation row from the result accounting:
// the row cost a line but the scroll decision never saw it, so the last
// guidance line was clipped with no way to reach it.
func TestMediumResultCountsTheNavigationRow(t *testing.T) {
	for _, width := range []int{99, 98, 90} {
		m := statusResultModel(t)
		resize(t, m, width, 24)
		// Every guidance line the model renders must be visible or reachable.
		for _, want := range []string{
			"상태 새로 고침",
			"비정상·중지 서비스의 로그를 먼저 확인.",
			"컨테이너가 없는 서비스는 스택 시작으로 생성됨.",
		} {
			if !viewContains(m, want) {
				pressRunes(t, m, "G")
				if !viewContains(m, want) {
					t.Fatalf("%d columns: guidance line %q is unreachable:\n%s", width, want, m.View())
				}
				pressRunes(t, m, "g")
			}
		}
	}
}

// At 120x18 the frame drew no service rows at all while the counter claimed
// `1-1/8`. A short wide frame now pins nothing: the whole result scrolls under
// the counter, so every row it names is reachable.
func TestWideShortFrameNeverCountsAnUndrawnRow(t *testing.T) {
	m := statusResultModel(t)
	resize(t, m, 120, 18)
	view := m.View()
	vp := m.currentViewport()
	drawn := drawnRegionRows(m)
	if drawn != vp.Visible() {
		t.Fatalf("scroll region draws %d rows, viewport says %d:\n%s", drawn, vp.Visible(), view)
	}
	// The counter may never name a row past the region it describes.
	if vp.Top+vp.Visible() > vp.Total {
		t.Fatalf("counter window %d..%d is past total %d:\n%s", vp.Top, vp.Top+vp.Visible(), vp.Total, view)
	}
	// The guidance is reachable instead of being cut.
	pressRunes(t, m, "G")
	if !strings.Contains(strings.Join(m.layoutBody().lines, "\n"), "스택 시작으로 생성됨") {
		t.Fatalf("G did not reach the guidance:\n%s", m.View())
	}
}

// statusResultModel renders the realistic status document used by the golden
// matrix: eight services, of which the short frames can only show a few.
func statusResultModel(t *testing.T) *Model {
	t.Helper()
	f := goldenStatusFeature(goldenSnapshot())
	return newPlainModel(t, f, &stubFeature{desc: descriptor("logs", false), body: "로그 본문"})
}

func newPlainModel(t *testing.T, features ...core.Feature) *Model {
	t.Helper()
	m := newTestModel(t, features...)
	m.theme = *plainTheme()
	initModel(t, m)
	return m
}

// drawnRegionRows counts the frame rows the scroll window owns. It is measured
// by position, never by content, so a region that mixes table rows with pinned
// summary rows or guidance is still measured exactly:
//
//	start = the rows the shell draws above the band, plus the pinned fixed rows
//	        (the whole result is the band when nothing could be pinned)
//	end   = the one-row scroll hint, when one is drawn
func drawnRegionRows(m *Model) int {
	if !m.bodyScrolls() {
		// Nothing scrolls: the counter is empty and the whole band is drawn.
		return len(m.resultGeometry().screen.Band)
	}
	layout := m.layoutBody()
	geom := m.resultGeometry()
	head := m.navRowCount() + m.titleRows()
	if len(m.lastLive) > 0 {
		head += len(m.liveTailLines(m.titleRows() + minResultRows))
	}
	start := head
	if !geom.band.whole {
		start += len(geom.screen.Fixed)
	}
	if start >= len(layout.lines) {
		return 0
	}
	end := len(layout.lines)
	for i := start; i < len(layout.lines); i++ {
		plain := ansi.Strip(layout.lines[i])
		if strings.Contains(plain, "PgUp/PgDn") && strings.Contains(plain, "있음") {
			end = i
			break
		}
	}
	return end - start
}

// drawnServiceRows counts the result table rows in the frame. The status table
// puts the service name in the first column, so a row is a body row whose first
// field is one of the fixture's services.
func drawnServiceRows(m *Model) int {
	known := map[string]bool{}
	for _, svc := range goldenSnapshot().Services {
		known[svc.Name] = true
	}
	body := strings.Split(m.View(), "\n")
	if len(body) > 2 {
		body = body[1 : len(body)-1]
	}
	count := 0
	for _, line := range body {
		// Strip the sidebar pane and its one-cell gap when it is drawn.
		if m.sidebarVisible() {
			width := visual.SidebarWidth(m.width) + 1
			line = visual.Truncate(line, visual.Width(line), "")
			line = cutCells(line, width)
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && known[fields[0]] {
			count++
		}
	}
	return count
}

// cutCells drops the first n display cells from s.
func cutCells(s string, n int) string {
	if n <= 0 {
		return s
	}
	width := 0
	for i, r := range s {
		w := visual.Width(string(r))
		if width+w > n {
			return s[i:]
		}
		width += w
	}
	return ""
}

// viewContains reports whether the whole frame contains text, including rows
// the reader can reach by scrolling.
func viewContains(m *Model, text string) bool {
	return strings.Contains(strings.Join(m.layoutBody().lines, "\n"), text)
}

// sizeName is the subtest name for a frame size.
func sizeName(width, height int) string {
	return itoa(width) + "x" + itoa(height)
}
