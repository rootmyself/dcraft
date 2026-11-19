package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type planningStub struct {
	*stubFeature
	planText string
	planErr  error
	events   *[]string
	plans    int
}

func (p *planningStub) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	p.plans++
	if p.events != nil {
		*p.events = append(*p.events, "plan")
	}
	if p.planErr != nil {
		return core.Plan{}, p.planErr
	}
	return core.Plan{Text: p.planText}, nil
}

func (p *planningStub) Run(ctx context.Context, in core.Invocation) (any, error) {
	if p.events != nil {
		*p.events = append(*p.events, "run")
	}
	p.ran = true
	return map[string]string{"ok": "true"}, nil
}

func approvalFeature(stub *planningStub) *planningStub {
	stub.desc = core.Descriptor{
		Name: "install-compose", Title: "설치", Summary: "설치합니다.", Usage: "dcraft install-compose",
		Approval: core.ApprovalInstall,
	}
	return stub
}

func TestApprovedHumanCommandShowsThePlanBeforeRunning(t *testing.T) {
	var events []string
	feature := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planText:    "출처: https://example.test/v1\n버전: v1\n대상 경로: /plugins/docker-compose",
		events:      &events,
	})
	h := newHarness(t, feature)
	if err := h.run("install-compose", "--approve", "install"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Join(events, ",") != "plan,run" {
		t.Fatalf("ordering = %v", events)
	}
	for _, want := range []string{"출처", "버전: v1", "/plugins/docker-compose"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Fatalf("plan on stderr missing %q:\n%s", want, h.stderr.String())
		}
	}
}

func TestUnapprovedHumanCommandShowsThePlanThenExitsTwo(t *testing.T) {
	feature := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planText:    "출처: https://example.test/v1\n버전: v1\n대상 경로: /plugins/docker-compose",
	})
	h := newHarness(t, feature)
	err := h.run("install-compose")
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (%v)", exitCode(err), err)
	}
	if feature.ran {
		t.Fatal("unapproved feature ran")
	}
	if feature.plans != 1 {
		t.Fatalf("plan calls = %d, want 1", feature.plans)
	}
	for _, want := range []string{"출처", "버전: v1", "/plugins/docker-compose"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Fatalf("plan on stderr missing %q:\n%s", want, h.stderr.String())
		}
	}
	if strings.Contains(h.stdout.String(), "출처") {
		t.Fatalf("plan leaked into stdout: %q", h.stdout.String())
	}
}

func TestUnapprovedJSONModeStaysOnePureObjectAndSkipsThePlan(t *testing.T) {
	feature := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planText:    "출처: https://example.test/v1",
	})
	h := newHarness(t, feature)
	err := h.run("install-compose", "--json")
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (%v)", exitCode(err), err)
	}
	if feature.plans != 0 || feature.ran {
		t.Fatalf("JSON approval failure ran plan=%d run=%v", feature.plans, feature.ran)
	}
	out := strings.TrimSpace(h.stdout.String())
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("stdout is not one JSON result: %v\n%s", err, out)
	}
	if decoded["outcome"] != "failure" || decoded["required_approval"] != "install" {
		t.Fatalf("unexpected JSON result: %s", out)
	}
	if _, hasPlan := decoded["plan"]; hasPlan {
		t.Fatalf("plan leaked into the JSON result: %s", out)
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON approval failure wrote stderr: %q", h.stderr.String())
	}
}

func TestJSONModeDoesNotPrintThePlan(t *testing.T) {
	feature := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planText:    "출처: https://example.test/v1",
	})
	h := newHarness(t, feature)
	if err := h.run("install-compose", "--approve", "install", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON mode wrote stderr: %q", h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "출처") {
		t.Fatalf("plan leaked into JSON stdout: %q", h.stdout.String())
	}
	if feature.plans != 0 {
		t.Fatalf("JSON mode ran a separate plan call: %d", feature.plans)
	}
	if !feature.ran {
		t.Fatal("feature did not run with approval")
	}
}

func TestPlanFailureStopsTheCommandBeforeRun(t *testing.T) {
	feature := approvalFeature(&planningStub{
		stubFeature: &stubFeature{},
		planErr: core.Fail(core.Diagnostic{
			Category:    core.CategoryComposeReleaseUnavailable,
			Severity:    core.SeverityError,
			Message:     "공식 릴리스 정보를 확인하지 못했습니다.",
			NextActions: []string{"네트워크 연결을 확인하세요."},
		}),
	})
	h := newHarness(t, feature)
	err := h.run("install-compose", "--approve", "install")
	if exitCode(err) != 1 {
		t.Fatalf("exit = %d, want 1 (%v)", exitCode(err), err)
	}
	if feature.ran {
		t.Fatal("feature ran after a failed plan")
	}
	if !strings.Contains(h.stderr.String(), "릴리스 정보를 확인하지 못했습니다") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}
