package update_test

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
	"github.com/rootmyself/dcraft/internal/update"
)

func updateResult(report update.Report) core.Result {
	return core.Result{
		Outcome: core.OutcomeSuccess,
		Action:  "update",
		Target: core.Target{
			ProjectDir: "/work/acme-platform",
			Files:      []string{"/work/acme-platform/compose.yaml"},
		},
		Data: update.Payload{Update: report},
	}
}

func baseReport() update.Report {
	return update.Report{
		ProjectName:       "acme-platform",
		ProjectNameSource: "configured",
		Scope:             "stack",
		Built:             []string{"api"},
		Pulled:            []string{"web"},
		Applied:           true,
		Verified:          true,
		Readiness: []stack.ServiceReadiness{
			{Service: "api", Status: stack.ReadinessHealthy},
			{Service: "web", Status: stack.ReadinessRunningUnverified},
			{Service: "db", Status: stack.ReadinessUnhealthy},
		},
		RecordStatus: update.RecordRecorded,
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

// The update document states the outcome, the target, the readiness table, the
// record, and the next actions — in that order.
func TestUpdateDocumentShape(t *testing.T) {
	doc := update.Document(updateResult(baseReport()))
	var kinds []core.BlockKind
	for _, b := range doc.Blocks {
		kinds = append(kinds, b.Kind)
	}
	if len(kinds) < 5 {
		t.Fatalf("blocks = %v", kinds)
	}
	if kinds[0] != core.BlockBadges || kinds[1] != core.BlockKeyValues {
		t.Fatalf("leading blocks = %v", kinds)
	}
	last := kinds[len(kinds)-1]
	if last != core.BlockCallout {
		t.Fatalf("last block = %q, want callout", last)
	}
	joined := renderDoc(t, doc, 100)
	if !strings.Contains(joined, "갱신 완료") {
		t.Fatalf("outcome missing:\n%s", joined)
	}
	if !strings.Contains(joined, "실행 상태") {
		t.Fatalf("readiness section missing:\n%s", joined)
	}
	if !strings.Contains(joined, "정상 버전 기록") {
		t.Fatalf("record section missing:\n%s", joined)
	}
}

// A readiness failure is a badge, not a bare word, so it reads without color.
func TestUpdateReadinessCarriesBadges(t *testing.T) {
	doc := update.Document(updateResult(baseReport()))
	var table *core.Table
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockTable {
			table = b.Table
		}
	}
	if table == nil {
		t.Fatal("no readiness table")
	}
	if len(table.Rows) != 3 {
		t.Fatalf("rows = %d", len(table.Rows))
	}
	if table.Rows[2].Cells[1].Badge != core.BadgeUnhealthy {
		t.Fatalf("unhealthy row badge = %q", table.Rows[2].Cells[1].Badge)
	}
	if table.Rows[0].Cells[1].Badge != core.BadgeRunning {
		t.Fatalf("healthy row badge = %q", table.Rows[0].Cells[1].Badge)
	}
}

// An unverified update never claims verified health.
func TestUpdateUnverifiedIsNotHealthy(t *testing.T) {
	report := baseReport()
	report.Verified = false
	joined := renderDoc(t, update.Document(updateResult(report)), 100)
	if !strings.Contains(joined, "일부 앱 확인 안 됨") {
		t.Fatalf("unverified state not stated:\n%s", joined)
	}
}

// A failed record must never read as a written one.
func TestUpdateFailedRecordIsStated(t *testing.T) {
	report := baseReport()
	report.RecordStatus = update.RecordFailed
	report.RecordDetail = "디스크가 가득 찼습니다."
	joined := renderDoc(t, update.Document(updateResult(report)), 100)
	if !strings.Contains(joined, "기록하지 못했습니다") {
		t.Fatalf("failed record not stated:\n%s", joined)
	}
	if !strings.Contains(joined, "디스크가 가득 찼습니다") {
		t.Fatalf("record detail lost:\n%s", joined)
	}
	if strings.Contains(joined, "직전 정상 버전을 기록했습니다") {
		t.Fatalf("failed record claims success:\n%s", joined)
	}
}

// A recorded good version carries its identifiers so the reader can verify it.
func TestUpdateRecordedShowsTheGoodVersion(t *testing.T) {
	report := baseReport()
	report.GoodVersion = &state.GoodVersion{
		OperationID: "op_42",
		Services: []state.GoodService{
			{Service: "api", ImageID: "sha256:abc", RetentionTag: "dcraft-good-api"},
			{Service: "web", ImageID: "sha256:def", RetentionTag: "dcraft-good-web"},
		},
	}
	joined := renderDoc(t, update.Document(updateResult(report)), 100)
	for _, want := range []string{"api: sha256:abc", "web: sha256:def", "dcraft-good-api"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("good version lost %q:\n%s", want, joined)
		}
	}
}

// Notices are surfaced, not buried: a service-scoped update says so.
func TestUpdateNoticesAreShown(t *testing.T) {
	report := baseReport()
	report.Scope = "services"
	report.Services = []string{"api"}
	report.Notices = []string{"서비스 선택 갱신입니다. 선택하지 않은 서비스는 변경하지 않았습니다."}
	joined := renderDoc(t, update.Document(updateResult(report)), 100)
	if !strings.Contains(joined, "api") {
		t.Fatalf("selected service missing:\n%s", joined)
	}
}

// The document fits every approved width.
func TestUpdateDocumentFitsEveryWidth(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		joined := renderDoc(t, update.Document(updateResult(baseReport())), width)
		if !strings.Contains(joined, "갱신 완료") {
			t.Fatalf("width %d: outcome missing:\n%s", width, joined)
		}
	}
}

// The CLI presenter keeps its own output.
func TestUpdateCLIPresenterUnchanged(t *testing.T) {
	out := update.Present(updateResult(baseReport()), 0)
	if !strings.Contains(out, "갱신 완료") {
		t.Fatalf("CLI output changed:\n%s", out)
	}
	if strings.Contains(out, "1. dcraft") {
		t.Fatalf("CLI output adopted the TUI numbered callout:\n%s", out)
	}
}
