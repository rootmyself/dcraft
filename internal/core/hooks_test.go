package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// scriptedResolver changes its answer after the first call so the
// revalidate_inputs stage can be observed.
type scriptedResolver struct {
	targets []core.Target
	calls   int
}

func (r *scriptedResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	i := r.calls
	if i >= len(r.targets) {
		i = len(r.targets) - 1
	}
	r.calls++
	return r.targets[i], nil, nil
}

type fakeHooks struct {
	events   *[]string
	plans    []core.HookPlan
	planCall int

	approved    []core.HookPlan
	approveErr  error
	executeErr  map[string]error
	executed    []string
	output      string
	planFailing error
}

func (f *fakeHooks) Plan(ctx context.Context, target core.Target) (core.HookPlan, error) {
	if f.events != nil {
		*f.events = append(*f.events, "plan")
	}
	if f.planFailing != nil {
		return core.HookPlan{}, f.planFailing
	}
	if len(f.plans) == 0 {
		return core.HookPlan{}, nil
	}
	i := f.planCall
	if i >= len(f.plans) {
		i = len(f.plans) - 1
	}
	f.planCall++
	return f.plans[i], nil
}

func (f *fakeHooks) Approve(ctx context.Context, target core.Target, plan core.HookPlan, at time.Time) error {
	if f.events != nil {
		*f.events = append(*f.events, "approve")
	}
	f.approved = append(f.approved, plan)
	return f.approveErr
}

func (f *fakeHooks) Execute(ctx context.Context, req core.HookRequest) (core.HookExecution, error) {
	if f.events != nil {
		*f.events = append(*f.events, string(req.Entry.Stage)+":"+req.Entry.Path)
	}
	if err := ctx.Err(); err != nil {
		return core.HookExecution{}, err
	}
	f.executed = append(f.executed, string(req.Entry.Stage)+":"+req.Entry.Path)
	if err := f.executeErr[req.Entry.Path]; err != nil {
		return core.HookExecution{ExitCode: 1, Output: f.output}, err
	}
	return core.HookExecution{Output: f.output}, nil
}

func hookPlan(entries ...core.HookEntry) core.HookPlan {
	plan := core.HookPlan{Entries: entries}
	for _, e := range entries {
		if !e.Trusted {
			plan.RequiresApproval = true
		}
	}
	return plan
}

func hookDescriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name: name, Title: "배포", Summary: "배포합니다.", Usage: "dcraft " + name,
		Section:        core.SectionInspect,
		RecordsHistory: true, Hooks: core.HookUpUpdate,
	}
}

func TestEngineRunsPreHooksBeforeFeatureAndPostHooksAfterVerify(t *testing.T) {
	var events []string
	feature := &verifyingFeature{
		testFeature: &testFeature{
			desc: hookDescriptor("up"),
			run: func(ctx context.Context, in core.Invocation) (any, error) {
				events = append(events, "feature")
				return "ok", nil
			},
		},
		verify: func(ctx context.Context, in core.Invocation, payload any) error {
			events = append(events, "verify")
			return nil
		},
	}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{
		events: &events,
		plans: []core.HookPlan{hookPlan(
			core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "aaa", Trusted: true},
			core.HookEntry{Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "bbb", Trusted: true},
		)},
		output: "hook output",
	}
	res := h.run(t, core.Request{Feature: "up"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if got, want := strings.Join(events, ","), "plan,pre:scripts/pre.sh,plan,feature,verify,post:scripts/post.sh"; got != want {
		t.Fatalf("ordering = %q, want %q", got, want)
	}
	wantStages := "resolve_target,preflight,hooks_pre,revalidate_inputs,execute,verify,hooks_post,history"
	if got := strings.Join(stageNames(res), ","); got != wantStages {
		t.Fatalf("stages = %q, want %q", got, wantStages)
	}
	if st := stageByName(t, res, "hooks_pre"); st.Status != core.StageSucceeded {
		t.Fatalf("hooks_pre = %q", st.Status)
	}
	if st := stageByName(t, res, "revalidate_inputs"); st.Status != core.StageSucceeded {
		t.Fatalf("revalidate_inputs = %q", st.Status)
	}
	if st := stageByName(t, res, "hooks_post"); st.Status != core.StageSucceeded {
		t.Fatalf("hooks_post = %q", st.Status)
	}
	if !hasCategory(res, core.CategoryHookOutput) {
		t.Fatalf("hook output was not published: %+v", res.Diagnostics)
	}
}

func TestEnginePreHookFailureBlocksTheFeature(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: hookDescriptor("update"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return "ok", nil },
	}
	h := newHarness(t, feature)
	hooks := &fakeHooks{
		plans: []core.HookPlan{hookPlan(
			core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "aaa", Trusted: true},
			core.HookEntry{Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "bbb", Trusted: true},
		)},
		executeErr: map[string]error{
			"scripts/pre.sh": core.Fail(core.Diagnostic{
				Category: core.CategoryHookFailed, Severity: core.SeverityError,
				Message: "사전 훅이 실패했습니다.",
			}),
		},
	}
	h.engine.Hooks = hooks
	res := h.run(t, core.Request{Feature: "update"})
	if ran {
		t.Fatal("feature ran after a failed pre-hook")
	}
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryHookFailed) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "hooks_pre"); st.Status != core.StageFailed {
		t.Fatalf("hooks_pre = %q", st.Status)
	}
	for _, name := range stageNames(res) {
		if name == "hooks_post" || name == "history" {
			t.Fatalf("unexpected stage after pre failure: %v", stageNames(res))
		}
	}
	if len(hooks.executed) != 1 || hooks.executed[0] != "pre:scripts/pre.sh" {
		t.Fatalf("executed = %v", hooks.executed)
	}
}

func TestEngineRequiresIndependentScriptTrustApproval(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return "ok", nil },
	}
	h := newHarness(t, feature)
	hooks := &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "abc123"},
	)}}
	h.engine.Hooks = hooks
	res := h.run(t, core.Request{Feature: "up"})
	if ran || len(hooks.executed) > 0 || len(hooks.approved) > 0 {
		t.Fatalf("untrusted script ran: feature=%v executed=%v approved=%v", ran, hooks.executed, hooks.approved)
	}
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome/exit = %q/%d", res.Outcome, res.ExitCode())
	}
	if res.RequiredApproval != core.ApprovalScriptTrust {
		t.Fatalf("required approval = %q", res.RequiredApproval)
	}
	diag := res.Diagnostics[0]
	if diag.Category != core.CategoryApprovalRequired || !strings.Contains(diag.Detail, "scripts/pre.sh") || !strings.Contains(diag.Detail, "abc123") {
		t.Fatalf("diagnostic does not name the script and content identity: %+v", diag)
	}
	if !strings.Contains(diag.Message, "스크립트 신뢰") && !strings.Contains(diag.Message, "승인") {
		t.Fatalf("message = %q", diag.Message)
	}
}

func TestEngineScriptTrustIsNotTheFeatureApproval(t *testing.T) {
	feature := &testFeature{
		desc: core.Descriptor{
			Name: "up", Title: "시작", Summary: "시작합니다.", Usage: "dcraft up",
			Section:  core.SectionOperate,
			Approval: core.ApprovalInstall, Hooks: core.HookUpUpdate, RecordsHistory: true,
		},
	}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "abc"},
	)}}
	res := h.run(t, core.Request{Feature: "up", Approvals: []core.ApprovalKind{core.ApprovalInstall}})
	if res.Outcome != core.OutcomeFailure || res.RequiredApproval != core.ApprovalScriptTrust {
		t.Fatalf("feature approval substituted trust: %q / %q", res.Outcome, res.RequiredApproval)
	}

	h2 := newHarness(t, feature)
	hooks := &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "abc"},
	)}}
	h2.engine.Hooks = hooks
	res2 := h2.run(t, core.Request{Feature: "up", Approvals: []core.ApprovalKind{
		core.ApprovalInstall, core.ApprovalScriptTrust,
	}})
	if res2.Outcome != core.OutcomeSuccess {
		t.Fatalf("both approvals did not run: %q (%+v)", res2.Outcome, res2.Diagnostics)
	}
	if len(hooks.approved) != 1 {
		t.Fatalf("trust approvals recorded = %d", len(hooks.approved))
	}
}

func TestEnginePostHookFailureIsSeparateFromCompletedWork(t *testing.T) {
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { return "ok", nil },
	}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{
		plans: []core.HookPlan{hookPlan(
			core.HookEntry{Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "bbb", Trusted: true},
		)},
		executeErr: map[string]error{
			"scripts/post.sh": core.Fail(core.Diagnostic{
				Category: core.CategoryHookPostFailed, Severity: core.SeverityError,
				Message: "후처리 훅이 실패했습니다.",
			}),
		},
	}
	res := h.run(t, core.Request{Feature: "up"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("post-hook failure changed the completed outcome: %q", res.Outcome)
	}
	if st := stageByName(t, res, "hooks_post"); st.Status != core.StageFailed {
		t.Fatalf("hooks_post = %q", st.Status)
	}
	if !hasCategory(res, core.CategoryHookPostFailed) {
		t.Fatalf("post failure not reported separately: %+v", res.Diagnostics)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryHookPostFailed {
			found = d.Severity == core.SeverityWarning
		}
	}
	if !found {
		t.Fatalf("post failure is not a warning: %+v", res.Diagnostics)
	}
	if len(h.state.records) != 1 {
		t.Fatalf("completed work was not recorded: %+v", h.state.records)
	}
}

func TestEngineNeverRunsHooksForOtherFeatures(t *testing.T) {
	hooks := &fakeHooks{planFailing: core.Fail(core.Diagnostic{
		Category: core.CategoryInternal, Severity: core.SeverityError, Message: "should not be called",
	})}
	feature := &testFeature{desc: readOnlyDescriptor("status")}
	h := newHarness(t, feature)
	h.engine.Hooks = hooks
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if hooks.planCall != 0 || len(hooks.executed) > 0 {
		t.Fatalf("hooks used for a read-only feature: %v", hooks.executed)
	}
	wantStages := "resolve_target,preflight,execute"
	if got := strings.Join(stageNames(res), ","); got != wantStages {
		t.Fatalf("stages = %q", got)
	}
}

func TestEngineRevalidatesInputsAndUsesTheFreshTarget(t *testing.T) {
	var got core.Target
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			got = in.Target
			return "ok", nil
		},
	}
	h := newHarness(t, feature)
	h.engine.Resolver = &scriptedResolver{targets: []core.Target{
		{ProjectDir: "/project", Files: []string{"/project/compose.yaml"}},
		{ProjectDir: "/project", Files: []string{"/project/compose.next.yaml"}},
	}}
	h.engine.Hooks = &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "aaa", Trusted: true},
	)}}
	res := h.run(t, core.Request{Feature: "up"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if got.Files[0] != "/project/compose.next.yaml" {
		t.Fatalf("feature saw stale inputs: %+v", got)
	}
	if res.Target.Files[0] != "/project/compose.next.yaml" {
		t.Fatalf("result target is stale: %+v", res.Target)
	}
}

func TestEngineRefusesPostHookChangedAfterApproval(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return "ok", nil },
	}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{
		plans: []core.HookPlan{
			hookPlan(
				core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "aaa", Trusted: true},
				core.HookEntry{Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "old", Trusted: true},
			),
			hookPlan(
				core.HookEntry{Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "new", Trusted: true},
			),
		},
	}
	res := h.run(t, core.Request{Feature: "up"})
	if ran {
		t.Fatal("feature ran with a post-hook whose content changed after approval")
	}
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome/exit = %q/%d", res.Outcome, res.ExitCode())
	}
	if !hasCategory(res, core.CategoryHookContentChanged) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestEngineRejectsHookContentChangedAfterTheApprovedPlan(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return "ok", nil },
	}
	h := newHarness(t, feature)
	hooks := &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "new-content"},
	)}}
	h.engine.Hooks = hooks
	res := h.run(t, core.Request{
		Feature:   "up",
		Approvals: []core.ApprovalKind{core.ApprovalScriptTrust},
		ApprovedHooks: []core.HookEntry{
			{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "approved-content"},
		},
	})
	if ran || len(hooks.executed) > 0 || len(hooks.approved) > 0 {
		t.Fatalf("changed script ran on the old approval: feature=%v exec=%v approve=%v", ran, hooks.executed, hooks.approved)
	}
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome/exit = %q/%d", res.Outcome, res.ExitCode())
	}
	if !hasCategory(res, core.CategoryHookContentChanged) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestEngineAcceptsMatchingApprovedPlan(t *testing.T) {
	feature := &testFeature{desc: hookDescriptor("up")}
	h := newHarness(t, feature)
	hooks := &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "same-content"},
	)}}
	h.engine.Hooks = hooks
	res := h.run(t, core.Request{
		Feature:   "up",
		Approvals: []core.ApprovalKind{core.ApprovalScriptTrust},
		ApprovedHooks: []core.HookEntry{
			{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "same-content"},
		},
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if len(hooks.approved) != 1 || len(hooks.executed) != 1 {
		t.Fatalf("approve=%v exec=%v", hooks.approved, hooks.executed)
	}
}

func TestEnginePlanCarriesHookIdentities(t *testing.T) {
	feature := &testFeature{desc: hookDescriptor("up")}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "abc123"},
	)}}
	report := h.engine.Plan(context.Background(), core.Request{Feature: "up"}, nil)
	if len(report.Hooks) != 1 || report.Hooks[0].SHA256 != "abc123" {
		t.Fatalf("plan hooks = %+v", report.Hooks)
	}
}

func TestEnginePlanShowsHooksWithoutAPlanner(t *testing.T) {
	feature := &testFeature{desc: hookDescriptor("up")}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{plans: []core.HookPlan{hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "abc123"},
	)}}
	report := h.engine.Plan(context.Background(), core.Request{Feature: "up"}, nil)
	if report.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", report.Outcome, report.Diagnostics)
	}
	for _, want := range []string{"scripts/pre.sh", "pre", "abc123", "script_trust"} {
		if !strings.Contains(report.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, report.Text)
		}
	}
	if len(report.RequiredApprovals) != 1 || report.RequiredApprovals[0] != core.ApprovalScriptTrust {
		t.Fatalf("required approvals = %v", report.RequiredApprovals)
	}
	if got := strings.Join(stageNamesReport(report), ","); got != "resolve_target,preflight" {
		t.Fatalf("stages = %q", got)
	}
}

func TestEngineContentChangedDiagnosticSurvivesExecution(t *testing.T) {
	feature := &testFeature{desc: hookDescriptor("up")}
	h := newHarness(t, feature)
	h.engine.Hooks = &fakeHooks{
		plans: []core.HookPlan{hookPlan(
			core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "approved", Trusted: true},
		)},
		executeErr: map[string]error{
			"scripts/pre.sh": core.Fail(core.Diagnostic{
				Category: core.CategoryHookContentChanged, Severity: core.SeverityError,
				Message: "승인 후 스크립트 내용이 바뀌었습니다.",
			}),
		},
	}
	res := h.run(t, core.Request{Feature: "up"})
	if res.ExitCode() != 2 || !hasCategory(res, core.CategoryHookContentChanged) {
		t.Fatalf("outcome/exit = %q/%d %+v", res.Outcome, res.ExitCode(), res.Diagnostics)
	}
}

func TestEngineCancellationDuringPreHookSkipsTheFeature(t *testing.T) {
	started := make(chan struct{})
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { return "ok", nil },
	}
	h := newHarness(t, feature)
	hooks := &cancelingHooks{plan: hookPlan(
		core.HookEntry{Path: "scripts/pre.sh", Stage: core.HookStagePre, SHA256: "aaa", Trusted: true},
	), started: started}
	h.engine.Hooks = hooks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.Result, 1)
	go func() { done <- h.engine.Run(ctx, core.Request{Feature: "up"}, nil) }()
	<-started
	cancel()
	res := <-done
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "hooks_pre"); st.Status != core.StageCanceled {
		t.Fatalf("hooks_pre = %q", st.Status)
	}
}

type cancelingHooks struct {
	plan    core.HookPlan
	started chan struct{}
}

func (c *cancelingHooks) Plan(context.Context, core.Target) (core.HookPlan, error) {
	return c.plan, nil
}
func (c *cancelingHooks) Approve(context.Context, core.Target, core.HookPlan, time.Time) error {
	return nil
}
func (c *cancelingHooks) Execute(ctx context.Context, req core.HookRequest) (core.HookExecution, error) {
	close(c.started)
	<-ctx.Done()
	return core.HookExecution{}, ctx.Err()
}

// Cancellation after the feature has produced a payload but while a post hook
// is running must not leak that payload into a canceled result.
func TestEngineCancellationDuringPostHookOmitsData(t *testing.T) {
	started := make(chan struct{})
	feature := &testFeature{
		desc: hookDescriptor("up"),
		run:  func(context.Context, core.Invocation) (any, error) { return "payload", nil },
	}
	h := newHarness(t, feature)
	hooks := &cancelingHooks{
		plan: hookPlan(core.HookEntry{
			Path: "scripts/post.sh", Stage: core.HookStagePost, SHA256: "bbb", Trusted: true,
		}),
		started: started,
	}
	h.engine.Hooks = hooks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.Result, 1)
	go func() { done <- h.engine.Run(ctx, core.Request{Feature: "up"}, nil) }()
	<-started
	cancel()
	res := <-done
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q, want canceled", res.Outcome)
	}
	if res.Data != nil {
		t.Fatalf("data = %#v on cancellation, want omitted", res.Data)
	}
	if st := stageByName(t, res, "hooks_post"); st.Status != core.StageCanceled {
		t.Fatalf("hooks_post = %q, want canceled", st.Status)
	}
}
