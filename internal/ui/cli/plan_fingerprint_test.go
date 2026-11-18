package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/cli"
)

// flipState changes its name-map fingerprint between the plan call and the run
// call, like an external writer changing names.json while the approval is on
// screen.
type flipState struct {
	memState
	calls int
}

func (s *flipState) PlanStateFingerprint(core.Target) string {
	s.calls++
	if s.calls <= 1 {
		return "plan-state"
	}
	return "changed-state"
}

type cliClock struct{}

func (cliClock) Now() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }

func TestCLIBindsTheRunToTheApprovedPlanFingerprint(t *testing.T) {
	stub := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planText:    "출처: https://example.test/v1",
	})
	reg := core.NewRegistry()
	if err := reg.Register(stub); err != nil {
		t.Fatal(err)
	}
	state := &flipState{}
	engine := core.NewEngine(reg, &captureResolver{target: core.Target{ProjectDir: "/project"}},
		emptyPreflight{}, state, cliClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_cli" }

	var stdout, stderr bytes.Buffer
	root := cli.New(cli.Options{Engine: engine, Registry: reg, Stdout: &stdout, Stderr: &stderr})
	root.SetArgs([]string{"install-compose", "--approve", "install"})
	err := root.ExecuteContext(context.Background())
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (%v); stderr=%s", exitCode(err), err, stderr.String())
	}
	if stub.ran {
		t.Fatal("CLI ran the feature with a stale approved plan")
	}
	if state.calls < 2 {
		t.Fatalf("CLI did not bind a plan fingerprint to the run: calls=%d", state.calls)
	}
	if !strings.Contains(stderr.String(), "승인한 계획") {
		t.Fatalf("stale-plan guidance missing:\n%s", stderr.String())
	}
}
