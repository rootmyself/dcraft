package lifecycle_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

func snapshot() stack.Snapshot {
	return stack.Snapshot{
		ProjectName: "acme-platform",
		ProjectDir:  "/work/acme-platform",
		Services: []stack.Service{
			{Name: "api", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy,
				Image: "api:local", Instances: []stack.Instance{{Name: "acme-api-1"}}},
			{Name: "web", Configured: true, State: stack.StateRunning, Health: stack.HealthNone,
				Image: "nginx:alpine", Instances: []stack.Instance{{Name: "acme-web-1"}},
				Connections: []stack.Connection{{Source: stack.SourceRuntime, HostIP: "0.0.0.0", PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"}}},
		},
		Summary: stack.Summary{Services: 2, Instances: 2, Running: 2, Healthy: 1, Unverified: 1},
	}
}

func lifecycleResult(action string, snap *stack.Snapshot) core.Result {
	return core.Result{
		Outcome:    core.OutcomeSuccess,
		Action:     action,
		Target:     core.Target{ProjectDir: "/work/acme-platform", Files: []string{"/work/acme-platform/compose.yaml"}},
		FinishedAt: time.Date(2026, 11, 20, 13, 0, 0, 0, time.Local),
		Data: lifecycle.Payload{Lifecycle: lifecycle.Report{
			Action:            action,
			ProjectName:       "acme-platform",
			ProjectNameSource: "configured",
			Scope:             "stack",
			State:             snap,
		}},
	}
}

func renderDoc(t *testing.T, doc core.Document, width int) string {
	t.Helper()
	th := visual.Plain()
	lines := visual.RenderDocument(th, doc, width)
	for i, line := range lines {
		if got := visual.Width(line); got > width {
			t.Fatalf("row %d is %d cells: %q", i, got, line)
		}
	}
	return strings.Join(lines, "\n")
}

// Every lifecycle action publishes the same document shape: outcome badges,
// identity and effect, the observed stack, then next actions.
func TestLifecycleDocumentShapeForEveryAction(t *testing.T) {
	snap := snapshot()
	for _, action := range []string{"up", "stop", "restart", "down"} {
		doc := lifecycle.Document(lifecycleResult(action, &snap))
		var kinds []core.BlockKind
		for _, b := range doc.Blocks {
			kinds = append(kinds, b.Kind)
		}
		want := []core.BlockKind{core.BlockBadges, core.BlockKeyValues, core.BlockTable, core.BlockCallout}
		if len(kinds) != len(want) {
			t.Fatalf("%s: blocks = %v", action, kinds)
		}
		for i := range want {
			if kinds[i] != want[i] {
				t.Fatalf("%s: block %d = %q, want %q", action, i, kinds[i], want[i])
			}
		}
	}
}

// The document's table uses the same column order as the status screen: the two
// screens compare the same facts.
func TestLifecycleStateTableMatchesTheStatusColumns(t *testing.T) {
	snap := snapshot()
	doc := lifecycle.Document(lifecycleResult("up", &snap))
	var table *core.Table
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockTable {
			table = b.Table
		}
	}
	if table == nil {
		t.Fatal("no table")
	}
	var headers []string
	for _, col := range table.Columns {
		headers = append(headers, col.Header)
	}
	if got := strings.Join(headers, "·"); got != "서비스·상태·인스턴스·health·이미지·포트" {
		t.Fatalf("columns = %q", got)
	}
	if len(table.Rows) != 2 {
		t.Fatalf("rows = %d", len(table.Rows))
	}
	if table.Rows[0].Cells[3].Text != "정상" {
		t.Fatalf("health label = %q, want 정상", table.Rows[0].Cells[3].Text)
	}
	if table.Rows[1].Cells[3].Text != "없음" {
		t.Fatalf("health label = %q, want 없음", table.Rows[1].Cells[3].Text)
	}
}

// An unread state is a warning, not an empty stack: the document must not render
// an empty table that would read as "no services".
func TestLifecycleDocumentUnreadStateIsAWarning(t *testing.T) {
	doc := lifecycle.Document(lifecycleResult("up", nil))
	joined := renderDoc(t, doc, 80)
	if strings.Contains(joined, "서비스") && strings.Contains(joined, "인스턴스") {
		// A table header would claim rows exist.
		if strings.Contains(joined, "서비스 ") && strings.Contains(joined, "health") {
			t.Fatalf("unread state rendered a table:\n%s", joined)
		}
	}
	if !strings.Contains(joined, "확인 실패") {
		t.Fatalf("unread state not explained:\n%s", joined)
	}
	var callout core.CalloutKind
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockCallout && strings.Contains(b.Text, "확인 실패") {
			callout = b.Callout
		}
	}
	if callout != core.CalloutWarn {
		t.Fatalf("unread state callout = %q, want warn", callout)
	}
}

// The scope must never widen silently: an explicit service selection is shown as
// itself, and a whole-stack run says so.
func TestLifecycleDocumentShowsTheExactScope(t *testing.T) {
	snap := snapshot()
	res := lifecycleResult("stop", &snap)
	res.Data = lifecycle.Payload{Lifecycle: lifecycle.Report{
		Action: "stop", ProjectName: "acme-platform", ProjectNameSource: "configured",
		Scope: "services", Services: []string{"api", "web"}, State: &snap,
	}}
	joined := renderDoc(t, lifecycle.Document(res), 80)
	if !strings.Contains(joined, "api, web") {
		t.Fatalf("selected scope missing:\n%s", joined)
	}
	whole := renderDoc(t, lifecycle.Document(lifecycleResult("stop", &snap)), 80)
	if !strings.Contains(whole, "스택 전체") {
		t.Fatalf("whole-stack scope missing:\n%s", whole)
	}
}

// The document fits every approved width, and the columns drop in order rather
// than breaking the frame.
func TestLifecycleDocumentFitsEveryWidth(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		doc := lifecycle.Document(lifecycleResult("up", ptrSnapshot()))
		joined := renderDoc(t, doc, width)
		for _, want := range []string{"스택 시작", "프로젝트", "서비스"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("width %d: missing %q:\n%s", width, want, joined)
			}
		}
	}
}

func ptrSnapshot() *stack.Snapshot {
	snap := snapshot()
	return &snap
}

// The CLI presenter is untouched: it keeps its own wording and layout.
func TestLifecycleCLIPresenterUnchanged(t *testing.T) {
	snap := snapshot()
	out := lifecycle.Present(lifecycleResult("up", &snap), 0)
	for _, want := range []string{"스택 시작.", "프로젝트:", "다음 행동"} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "다음 행동\n1. ") {
		t.Fatalf("CLI output adopted the TUI numbered callout:\n%s", out)
	}
}
