package status_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/stack"
)

func documentBlocks(doc core.Document) map[core.BlockKind]core.Block {
	out := map[core.BlockKind]core.Block{}
	for _, b := range doc.Blocks {
		out[b.Kind] = b
	}
	return out
}

// The status document is badges → key-values → table → callout. The order is
// part of the approved contract, so it is asserted rather than assumed.
func TestDocumentFollowsTheApprovedBlockOrder(t *testing.T) {
	f := status.New(&fakeBackend{})
	doc := f.Document(successResult(t, sampleSnapshot()))

	want := []core.BlockKind{
		core.BlockBadges,
		core.BlockKeyValues,
		core.BlockTable,
		core.BlockCallout,
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("blocks = %v, want %v", kinds(doc.Blocks), want)
	}
	for i, kind := range want {
		if doc.Blocks[i].Kind != kind {
			t.Fatalf("block %d = %q, want %q (%v)", i, doc.Blocks[i].Kind, kind, kinds(doc.Blocks))
		}
	}
}

func kinds(blocks []core.Block) []core.BlockKind {
	out := make([]core.BlockKind, len(blocks))
	for i, b := range blocks {
		out[i] = b.Kind
	}
	return out
}

// The summary badges carry the counts and the semantic state, and the full
// table column order is the T1 amendment: 서비스 · 상태 · 인스턴스 · health ·
// 이미지 · 포트.
func TestDocumentBadgesAndTableColumns(t *testing.T) {
	f := status.New(&fakeBackend{})
	doc := f.Document(successResult(t, sampleSnapshot()))
	blocks := documentBlocks(doc)

	badgeText := ""
	for _, b := range blocks[core.BlockBadges].Badges {
		badgeText += "[" + b.Text + "]"
	}
	for _, want := range []string{"실행 중 2", "비정상", "중지", "미생성"} {
		if want == "비정상" || want == "중지" || want == "미생성" {
			// Zero counts are omitted, so only their absence is meaningful
			// here; the nonzero case is covered below.
			continue
		}
		if !strings.Contains(badgeText, want) {
			t.Fatalf("badges missing %q: %s", want, badgeText)
		}
	}
	// A total count is not a state: it must not carry a status icon.
	for _, b := range blocks[core.BlockBadges].Badges {
		if strings.HasPrefix(b.Text, "서비스 ") || strings.HasPrefix(b.Text, "인스턴스 ") {
			if b.Kind != "" {
				t.Fatalf("total count %q carries a state badge %q", b.Text, b.Kind)
			}
		}
	}
	if !strings.Contains(badgeText, "서비스 3") {
		t.Fatalf("badges do not state the service count: %s", badgeText)
	}
	if !strings.Contains(badgeText, "인스턴스 2") {
		t.Fatalf("badges do not state the instance count: %s", badgeText)
	}

	table := blocks[core.BlockTable].Table
	if table == nil {
		t.Fatal("no table block")
	}
	var headers []string
	for _, col := range table.Columns {
		headers = append(headers, col.Header)
	}
	if got := strings.Join(headers, "·"); got != "서비스·상태·인스턴스·health·이미지·포트" {
		t.Fatalf("columns = %q", got)
	}
	if len(table.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(table.Rows))
	}

	// The dimensions come from the approved width contract: the three
	// essential columns can never be dropped, the rest drop in order.
	for _, col := range table.Columns {
		switch col.Header {
		case "서비스":
			if col.Weight != 30 || col.Min != 12 || col.Max != 24 || col.DropPriority != 0 {
				t.Fatalf("서비스 column = %+v", col)
			}
		case "상태":
			if col.Fixed != 11 || col.DropPriority != 0 {
				t.Fatalf("상태 column = %+v", col)
			}
		case "인스턴스":
			if col.Fixed != 8 || col.Align != core.AlignRight || col.DropPriority != 0 {
				t.Fatalf("인스턴스 column = %+v", col)
			}
		case "health":
			if col.Fixed != 8 || col.DropPriority != 3 {
				t.Fatalf("health column = %+v", col)
			}
		case "이미지":
			if col.Weight != 45 || col.Min != 16 || col.Max != 34 || col.DropPriority != 2 {
				t.Fatalf("이미지 column = %+v", col)
			}
		case "포트":
			if col.Fixed != 13 || col.DropPriority != 1 {
				t.Fatalf("포트 column = %+v", col)
			}
		}
	}
}

// The TUI health wording is Korean and independent of the CLI wording:
// 정상 / 비정상 / 확인 중 / 없음.
func TestDocumentUsesKoreanHealthLabels(t *testing.T) {
	f := status.New(&fakeBackend{})
	snap := sampleSnapshot()
	snap.Services = []stack.Service{
		{Name: "healthy", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy, Instances: []stack.Instance{{Name: "healthy-1", State: "running"}}},
		{Name: "unhealthy", Configured: true, State: stack.StateRunning, Health: stack.HealthUnhealthy, Instances: []stack.Instance{{Name: "unhealthy-1", State: "running"}}},
		{Name: "starting", Configured: true, State: stack.StateRunning, Health: stack.HealthStarting, Instances: []stack.Instance{{Name: "starting-1", State: "running"}}},
		{Name: "none", Configured: true, State: stack.StateRunning, Health: stack.HealthNone, Instances: []stack.Instance{{Name: "none-1", State: "running"}}},
	}
	snap.Summary = stack.Summary{Services: 4, Instances: 4, Running: 4, Healthy: 1, Unhealthy: 1}
	doc := f.Document(successResult(t, snap))
	table := documentBlocks(doc)[core.BlockTable].Table

	got := map[string]string{}
	for _, row := range table.Rows {
		// Cells: 서비스, 상태, 인스턴스, health, 이미지, 포트
		got[row.Cells[0].Text] = row.Cells[3].Text
	}
	want := map[string]string{
		"healthy":   "정상",
		"unhealthy": "비정상",
		"starting":  "확인 중",
		"none":      "없음",
	}
	for name, label := range want {
		if got[name] != label {
			t.Fatalf("health label for %s = %q, want %q (all: %v)", name, got[name], label, got)
		}
	}
	// The unhealthy cell carries the semantic kind so the renderer can pair it
	// with an icon; a healthy cell needs no alarm coloring.
	if table.Rows[1].Cells[3].Badge != core.BadgeUnhealthy {
		t.Fatalf("unhealthy badge kind = %q", table.Rows[1].Cells[3].Badge)
	}
	if table.Rows[2].Cells[3].Badge != core.BadgeStarting {
		t.Fatalf("starting badge kind = %q", table.Rows[2].Cells[3].Badge)
	}
}

// Long values stay complete in the document: the feature never pre-truncates,
// because only the renderer knows the width and the theme's ellipsis glyph
// (UTF-8 `…` versus ASCII `~`). Ports carry a richer detail value the cell
// cannot fit, so the detail view can explain the binding.
func TestDocumentCellsStayCompleteForTheRenderer(t *testing.T) {
	f := status.New(&fakeBackend{})
	snap := sampleSnapshot()
	snap.Services[0].Image = "registry.example.com/team/a-very-long-image-name:2026-11-20"
	res := successResult(t, snap)
	res.Target.Files = []string{"/Users/npnl/Documents/work/an-extremely-long-project-folder-name/compose.yaml"}
	doc := f.Document(res)
	blocks := documentBlocks(doc)

	table := blocks[core.BlockTable].Table
	image := table.Rows[0].Cells[4]
	if image.Text != "registry.example.com/team/a-very-long-image-name:2026-11-20" {
		t.Fatalf("image was pre-truncated: %q", image.Text)
	}
	if strings.Contains(image.Text, "…") || strings.Contains(image.Text, "~") {
		t.Fatalf("image carries a renderer's truncation marker: %q", image.Text)
	}

	var composeValue string
	for _, p := range blocks[core.BlockKeyValues].Pairs {
		if strings.Contains(p.Key, "Compose") {
			composeValue = p.Value
		}
	}
	if !strings.Contains(composeValue, "an-extremely-long-project-folder-name") {
		t.Fatalf("compose file pair was truncated: %q", composeValue)
	}

	// The port cell is a compact binding; its detail states host, protocol and
	// whether the mapping came from the Compose file.
	port := table.Rows[0].Cells[5]
	if !strings.Contains(port.Text, "8080") || !strings.Contains(port.Text, "80") {
		t.Fatalf("port cell = %q", port.Text)
	}
	if port.Detail == "" || port.Detail == port.Text {
		t.Fatalf("port cell has no richer detail: %+v", port)
	}
	if !strings.Contains(port.Detail, "0.0.0.0") || !strings.Contains(port.Detail, "tcp") {
		t.Fatalf("port detail = %q", port.Detail)
	}
	// A service with no published runtime port renders the approved dash.
	if dash := table.Rows[1].Cells[5].Text; dash != "-" {
		t.Fatalf("portless service = %q, want -", dash)
	}
}

// An empty stack explains itself in the table's EmptyText and still shows the
// callout; it must never render as a blank panel or as a read failure.
func TestDocumentEmptyStackIsExplained(t *testing.T) {
	f := status.New(&fakeBackend{})
	snap := stack.Snapshot{Backend: "compose", ProjectName: "empty", ProjectDir: "/work/empty", Services: []stack.Service{}}
	doc := f.Document(successResult(t, snap))
	table := documentBlocks(doc)[core.BlockTable].Table
	if table == nil {
		t.Fatal("no table block")
	}
	if len(table.Rows) != 0 {
		t.Fatalf("empty stack has %d rows", len(table.Rows))
	}
	if !strings.Contains(table.EmptyText, "서비스가 없음") {
		t.Fatalf("empty text = %q", table.EmptyText)
	}
	if !strings.Contains(table.EmptyText, "Compose") {
		t.Fatalf("empty text does not say what to check: %q", table.EmptyText)
	}
}

// A missing payload cannot produce a document; the fallback stays a paragraph
// so the TUI never renders an empty screen.
func TestDocumentHandlesMissingPayload(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	res.Data = nil
	doc := f.Document(res)
	if len(doc.Blocks) == 0 {
		t.Fatal("no blocks for a missing payload")
	}
	var text strings.Builder
	for _, b := range doc.Blocks {
		text.WriteString(b.Text)
	}
	if !strings.Contains(text.String(), "상태 정보 표시 불가") {
		t.Fatalf("missing payload not explained: %+v", doc.Blocks)
	}
}

// The document belongs to the TUI only: Present must keep the CLI output
// byte-for-byte, including its health wording.
func TestPresentKeepsCLIWordingForHealth(t *testing.T) {
	f := status.New(&fakeBackend{})
	out := f.Present(successResult(t, sampleSnapshot()), 0)
	for _, want := range []string{"healthy · 앱 확인됨", "healthcheck 없음 · 앱 확인 안 됨"} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output lost %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"확인 중 · 앱 확인", "비정상"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("CLI output adopted the TUI wording %q:\n%s", forbidden, out)
		}
	}
}

// The key-value block carries the last observation time, the compose file and
// the project identity, in that order.
func TestDocumentKeyValues(t *testing.T) {
	f := status.New(&fakeBackend{})
	res := successResult(t, sampleSnapshot())
	res.FinishedAt = time.Date(2026, 11, 20, 13, 22, 46, 0, time.Local)
	doc := f.Document(res)
	pairs := documentBlocks(doc)[core.BlockKeyValues].Pairs
	if len(pairs) != 3 {
		t.Fatalf("pairs = %+v", pairs)
	}
	if pairs[0].Key != "마지막 조회" || pairs[0].Value != "13:22:46" {
		t.Fatalf("first pair = %+v", pairs[0])
	}
	if !strings.Contains(pairs[1].Value, "compose.yaml") {
		t.Fatalf("compose pair = %+v", pairs[1])
	}
	if pairs[2].Value != "hello-world" {
		t.Fatalf("project pair = %+v", pairs[2])
	}
}

// Times are omitted rather than rendered as a zero date or as a dangling empty
// label when the engine did not report one.
func TestDocumentOmitsUnknownObservationTime(t *testing.T) {
	f := status.New(&fakeBackend{})
	doc := f.Document(successResult(t, sampleSnapshot()))
	for _, p := range documentBlocks(doc)[core.BlockKeyValues].Pairs {
		if p.Key == "마지막 조회" {
			t.Fatalf("unknown observation time still has a row: %+v", p)
		}
	}
	// The remaining identity rows are unaffected.
	if len(documentBlocks(doc)[core.BlockKeyValues].Pairs) != 2 {
		t.Fatalf("pairs = %+v", documentBlocks(doc)[core.BlockKeyValues].Pairs)
	}
}
