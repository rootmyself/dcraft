package tui_test

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// These are feature-level snapshot tests: they render the real status document
// with the real renderer, so a change to either side cannot silently break the
// screen. They intentionally assert structure (order, columns, wording) rather
// than pixel-exact spacing, which the width contract already covers.

func snapshotFor(services []stack.Service) stack.Snapshot {
	return stack.Snapshot{
		Backend:     "compose",
		ProjectName: "acme-platform",
		ProjectDir:  "/work/acme-platform",
		Services:    services,
		Summary:     summaryOf(services),
	}
}

func summaryOf(services []stack.Service) stack.Summary {
	s := stack.Summary{Services: len(services)}
	for _, svc := range services {
		s.Instances += len(svc.Instances)
		switch svc.State {
		case stack.StateRunning:
			s.Running++
		case stack.StateExited:
			s.Stopped++
		case stack.StateNotCreated:
			s.NotCreated++
		}
		switch svc.Health {
		case stack.HealthHealthy:
			s.Healthy++
		case stack.HealthUnhealthy:
			s.Unhealthy++
		}
	}
	return s
}

func statusResult(snap stack.Snapshot) core.Result {
	return core.Result{
		Outcome: core.OutcomeSuccess,
		Action:  "status",
		Target:  core.Target{ProjectDir: "/work/acme-platform", Files: []string{"/work/acme-platform/compose.yaml"}},
		Data:    status.Payload{Stack: snap},
	}
}

func renderStatus(t *testing.T, doc core.Document, width int) []string {
	t.Helper()
	th := visual.Plain()
	lines := visual.RenderDocument(th, doc, width)
	if len(lines) == 0 {
		t.Fatal("document rendered nothing")
	}
	for i, line := range lines {
		if got := visual.Width(line); got > width {
			t.Fatalf("width %d: row %d is %d cells: %q", width, i, got, line)
		}
	}
	return lines
}

func TestStatusDocumentSnapshot(t *testing.T) {
	feature := status.New(nil)
	doc := feature.Document(statusResult(snapshotFor([]stack.Service{
		{Name: "api", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy,
			Image: "api:local", Build: true, Instances: []stack.Instance{{Name: "acme-api-1"}}},
		{Name: "web", Configured: true, State: stack.StateRunning, Health: stack.HealthUnhealthy,
			Image: "nginx:alpine", Instances: []stack.Instance{{Name: "acme-web-1"}},
			Connections: []stack.Connection{{Source: stack.SourceRuntime, HostIP: "0.0.0.0", PublishedPort: 8080, TargetPort: 80, Protocol: "tcp"}}},
		{Name: "db", Configured: false, State: stack.StateNotCreated, Health: stack.HealthNone},
	})))

	lines := renderStatus(t, doc, 100)
	joined := strings.Join(lines, "\n")

	// Block order: the summary comes first, then identity, then the table,
	// then the guidance. This is the approved order, not an accident.
	badges := strings.Index(joined, "실행 중 2")
	identity := strings.Index(joined, "Compose 파일")
	// The table header row is the one that carries every column name.
	header := strings.Index(joined, "서비스                 상태")
	if header < 0 {
		header = strings.Index(joined, "서비스")
	}
	guidance := strings.Index(joined, "상태를 새로 고치려면")
	for name, idx := range map[string]int{"badges": badges, "identity": identity, "table": header, "guidance": guidance} {
		if idx < 0 {
			t.Fatalf("%s block missing:\n%s", name, joined)
		}
	}
	if !(badges < identity && identity < header && header < guidance) {
		t.Fatalf("block order is wrong (badges=%d identity=%d table=%d guidance=%d):\n%s",
			badges, identity, header, guidance, joined)
	}

	// The TUI health wording, not the CLI's.
	for _, want := range []string{"정상", "비정상", "없음"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing TUI health label %q:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"앱 확인됨", "앱 확인 안 됨", "healthcheck 없음"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("CLI wording %q leaked into the TUI:\n%s", forbidden, joined)
		}
	}
}

func TestStatusDocumentNarrowWidthKeepsEssentials(t *testing.T) {
	feature := status.New(nil)
	doc := feature.Document(statusResult(snapshotFor([]stack.Service{
		{Name: "api", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy, Instances: []stack.Instance{{Name: "acme-api-1"}}},
		{Name: "web", Configured: true, State: stack.StateExited, Instances: []stack.Instance{{Name: "acme-web-1"}}},
	})))
	lines := renderStatus(t, doc, 40)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"서비스", "상태", "api", "web"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("40 columns dropped %q:\n%s", want, joined)
		}
	}
}

func TestStatusDocumentEmptyStackSnapshot(t *testing.T) {
	feature := status.New(nil)
	doc := feature.Document(statusResult(snapshotFor(nil)))
	joined := strings.Join(renderStatus(t, doc, 80), "\n")
	if !strings.Contains(joined, "서비스 없음") {
		t.Fatalf("empty stack has no badge:\n%s", joined)
	}
	if !strings.Contains(joined, "서비스가 없습니다") {
		t.Fatalf("empty stack is not explained:\n%s", joined)
	}
	if strings.Contains(joined, "작업 실패") {
		t.Fatalf("empty stack rendered as a failure:\n%s", joined)
	}
}
