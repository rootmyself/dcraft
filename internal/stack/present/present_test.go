package present_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/lifecycle"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// presentSnapshot is one stack that exercises every state and health the
// mapping knows about.
func presentSnapshot() stack.Snapshot {
	return stack.Snapshot{
		Backend:     "compose",
		ProjectName: "acme-platform",
		ProjectDir:  "/work/acme-platform",
		Services: []stack.Service{
			{Name: "running", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy,
				Image: "api:1", Instances: []stack.Instance{{Name: "api-1"}}},
			{Name: "unhealthy", Configured: true, State: stack.StateRunning, Health: stack.HealthUnhealthy,
				Image: "web:1", Instances: []stack.Instance{{Name: "web-1"}}},
			{Name: "starting", Configured: true, State: stack.StateRunning, Health: stack.HealthStarting,
				Image: "worker:1", Instances: []stack.Instance{{Name: "worker-1"}}},
			{Name: "exited", Configured: true, State: stack.StateExited, Health: stack.HealthNone,
				Image: "cache:1", Instances: []stack.Instance{{Name: "cache-1"}}},
			{Name: "paused", Configured: true, State: stack.StatePaused, Health: stack.HealthNone,
				Image: "db:1", Instances: []stack.Instance{{Name: "db-1"}}},
			{Name: "restarting", Configured: true, State: stack.StateRestarting, Health: stack.HealthNone,
				Image: "queue:1", Instances: []stack.Instance{{Name: "queue-1"}}},
			{Name: "created", Configured: true, State: stack.StateCreated, Health: stack.HealthNone,
				Image: "migrate:1"},
			{Name: "missing", Configured: true, State: stack.StateNotCreated, Health: stack.HealthNone,
				Image: "seed:1"},
		},
	}
}

// The status and lifecycle documents must render the same state and health text
// for the same input. They used to keep private copies of the mapping; this is
// the assertion that they cannot drift apart again.
func TestStatusAndLifecycleTablesAgreeOnStateAndHealth(t *testing.T) {
	snap := presentSnapshot()

	statusDoc := status.New(nil).Document(core.Result{
		Outcome: core.OutcomeSuccess,
		Action:  "status",
		Target:  core.Target{ProjectDir: "/work/acme-platform"},
		Data:    status.Payload{Stack: snap},
	})
	lifecycleDoc := lifecycle.Document(core.Result{
		Outcome:    core.OutcomeSuccess,
		Action:     "up",
		Target:     core.Target{ProjectDir: "/work/acme-platform"},
		FinishedAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
		Data: lifecycle.Payload{Lifecycle: lifecycle.Report{
			Action:            "up",
			ProjectName:       "acme-platform",
			ProjectNameSource: "configured",
			Scope:             "stack",
			State:             &snap,
		}},
	})

	statusRows := tableRows(t, statusDoc)
	lifecycleRows := tableRows(t, lifecycleDoc)
	if len(statusRows) != len(lifecycleRows) {
		t.Fatalf("row counts differ: status %d, lifecycle %d", len(statusRows), len(lifecycleRows))
	}
	for i := range statusRows {
		// Column 0 is the service name, 1 the state, 2 the instance count, 3
		// the health. The two documents describe the same stack, so every one
		// of those cells must be byte-identical.
		for col := 0; col < 4; col++ {
			if got, want := statusRows[i].Cells[col].Text, lifecycleRows[i].Cells[col].Text; got != want {
				t.Fatalf("row %d column %d: status %q, lifecycle %q", i, col, got, want)
			}
			if got, want := statusRows[i].Cells[col].Badge, lifecycleRows[i].Cells[col].Badge; got != want {
				t.Fatalf("row %d column %d badge: status %q, lifecycle %q", i, col, got, want)
			}
		}
	}
}

// The rendered table text is identical too, which is what the reader sees.
func TestStatusAndLifecycleTablesRenderTheSameText(t *testing.T) {
	snap := presentSnapshot()
	statusDoc := status.New(nil).Document(core.Result{
		Outcome: core.OutcomeSuccess, Action: "status",
		Target: core.Target{ProjectDir: "/work/acme-platform"},
		Data:   status.Payload{Stack: snap},
	})
	lifecycleDoc := lifecycle.Document(core.Result{
		Outcome: core.OutcomeSuccess, Action: "up",
		Target:     core.Target{ProjectDir: "/work/acme-platform"},
		FinishedAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
		Data: lifecycle.Payload{Lifecycle: lifecycle.Report{
			Action: "up", ProjectName: "acme-platform",
			ProjectNameSource: "configured", Scope: "stack", State: &snap,
		}},
	})

	th := visual.Plain()
	statusTable := tableLines(t, statusDoc, th)
	lifecycleTable := tableLines(t, lifecycleDoc, th)
	if strings.Join(statusTable, "\n") != strings.Join(lifecycleTable, "\n") {
		t.Fatalf("status and lifecycle tables render differently:\n--- status ---\n%s\n--- lifecycle ---\n%s",
			strings.Join(statusTable, "\n"), strings.Join(lifecycleTable, "\n"))
	}
}

func tableRows(t *testing.T, doc core.Document) []core.Row {
	t.Helper()
	for _, block := range doc.Blocks {
		if block.Kind == core.BlockTable && block.Table != nil {
			return block.Table.Rows
		}
	}
	t.Fatal("document has no table")
	return nil
}

// tableLines renders only the document's table block: the surrounding summary
// and guidance are surface-specific and legitimately differ.
func tableLines(t *testing.T, doc core.Document, th visual.Theme) []string {
	t.Helper()
	var lines []string
	for _, block := range doc.Blocks {
		if block.Kind != core.BlockTable || block.Table == nil {
			continue
		}
		lines = append(lines, visual.RenderDocument(th, core.Document{
			Blocks: []core.Block{{Kind: core.BlockTable, Table: block.Table}},
		}, 120)...)
	}
	if len(lines) == 0 {
		t.Fatal("document has no rendered table")
	}
	return lines
}
