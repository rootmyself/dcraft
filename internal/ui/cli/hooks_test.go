package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/cli"
)

type cliHookService struct {
	plan     core.HookPlan
	approves int
	executes int
}

func (s *cliHookService) Plan(context.Context, core.Target) (core.HookPlan, error) {
	return s.plan, nil
}
func (s *cliHookService) Approve(context.Context, core.Target, core.HookPlan, time.Time) error {
	s.approves++
	return nil
}
func (s *cliHookService) Execute(context.Context, core.HookRequest) (core.HookExecution, error) {
	s.executes++
	return core.HookExecution{Output: "hook says hello"}, nil
}

func hookUpFeature(ran *bool) *stubFeature {
	return &stubFeature{
		desc: core.Descriptor{
			Name: "up", Title: "시작", Summary: "스택을 시작합니다.", Usage: "dcraft up",
			RecordsHistory: true, Hooks: core.HookUpUpdate,
		},
		run: func(context.Context, core.Invocation) (any, error) {
			*ran = true
			return map[string]string{"ok": "true"}, nil
		},
	}
}

func newHookHarness(t *testing.T, feature core.Feature, hooks *cliHookService) (*cliHarness, *core.Engine) {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(feature); err != nil {
		t.Fatalf("Register: %v", err)
	}
	resolver := &captureResolver{target: core.Target{ProjectDir: "/project"}}
	engine := core.NewEngine(reg, resolver, emptyPreflight{}, memState{}, fakeClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_cli_hooks" }
	engine.Hooks = hooks
	h := &cliHarness{}
	h.root = cli.New(cli.Options{
		Engine:   engine,
		Registry: reg,
		Stdout:   &h.stdout,
		Stderr:   &h.stderr,
	})
	return h, engine
}

type cliHarness struct {
	root interface {
		SetArgs([]string)
		Execute() error
	}
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func untrustedCLIHookPlan() core.HookPlan {
	return core.HookPlan{
		Entries: []core.HookEntry{{
			Path: "scripts/pre.sh", Stage: core.HookStagePre,
			SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		}},
		RequiresApproval: true,
	}
}

func TestCLIShowsTheHookPlanBeforeRunningAndFailsWithoutTrust(t *testing.T) {
	ran := false
	hooks := &cliHookService{plan: untrustedCLIHookPlan()}
	h, _ := newHookHarness(t, hookUpFeature(&ran), hooks)
	h.root.SetArgs([]string{"up"})
	err := h.root.Execute()
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (%v)", exitCode(err), err)
	}
	for _, want := range []string{"scripts/pre.sh", "abcdef0123456789", "script_trust", "스크립트 신뢰"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, h.stderr.String())
		}
	}
	if ran || hooks.executes != 0 || hooks.approves != 0 {
		t.Fatalf("untrusted hook ran: feature=%v exec=%d approve=%d", ran, hooks.executes, hooks.approves)
	}
}

func TestCLIJSONStaysPureWithHookOutput(t *testing.T) {
	ran := false
	hooks := &cliHookService{plan: untrustedCLIHookPlan()}
	h, _ := newHookHarness(t, hookUpFeature(&ran), hooks)
	h.root.SetArgs([]string{"up", "--approve", "script_trust", "--json"})
	if err := h.root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ran || hooks.executes != 1 || hooks.approves != 1 {
		t.Fatalf("trusted hook did not run: feature=%v exec=%d approve=%d", ran, hooks.executes, hooks.approves)
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, h.stdout.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(h.stdout.String()), "{") || strings.Count(strings.TrimSpace(h.stdout.String()), "\n}") != 1 {
		t.Fatalf("stdout is not one JSON object:\n%s", h.stdout.String())
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON mode wrote stderr: %q", h.stderr.String())
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryHookOutput && strings.Contains(d.Detail, "hook says hello") {
			found = true
		}
	}
	if !found {
		t.Fatalf("hook output not carried in diagnostics: %+v", res.Diagnostics)
	}
}
