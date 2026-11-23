package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeResolver struct {
	target core.Target
	warns  []core.Diagnostic
	err    error
	called bool
	got    core.TargetRequest
}

func (f *fakeResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	f.called = true
	f.got = req
	return f.target, f.warns, f.err
}

type fakePreflight struct {
	report   core.PreflightReport
	called   bool
	requires core.Requirements
}

func (f *fakePreflight) Check(ctx context.Context, requires core.Requirements) core.PreflightReport {
	f.called = true
	f.requires = requires
	return f.report
}

type fakeState struct {
	dir     string
	records []core.OperationRecord
	err     error
}

func (f *fakeState) StackDir(target core.Target) (string, error) {
	return f.dir, nil
}

func (f *fakeState) AppendOperation(target core.Target, rec core.OperationRecord) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, rec)
	return nil
}

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time {
	f.now = f.now.Add(time.Second)
	return f.now
}

type testFeature struct {
	desc core.Descriptor
	run  func(ctx context.Context, in core.Invocation) (any, error)
}

func (f *testFeature) Descriptor() core.Descriptor { return f.desc }
func (f *testFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.run == nil {
		return "ok", nil
	}
	return f.run(ctx, in)
}

type verifyingFeature struct {
	*testFeature
	verify func(ctx context.Context, in core.Invocation, payload any) error
}

func (f *verifyingFeature) Verify(ctx context.Context, in core.Invocation, payload any) error {
	return f.verify(ctx, in, payload)
}

func readOnlyDescriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name:     name,
		Section:  core.SectionInspect,
		Title:    "스택 상태",
		Summary:  "현재 스택 상태 확인.",
		Usage:    "dcraft " + name,
		ReadOnly: true,
	}
}

type engineHarness struct {
	engine     *core.Engine
	resolver   *fakeResolver
	preflight  *fakePreflight
	state      *fakeState
	feature    core.Feature
	sinkEvents []core.Stage
	registry   *core.Registry
	clock      *fakeClock
}

func newHarness(t *testing.T, feature core.Feature) *engineHarness {
	t.Helper()
	reg := core.NewRegistry()
	if feature != nil {
		if err := reg.Register(feature); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	resolver := &fakeResolver{target: core.Target{
		ProjectDir: "/project",
		Files:      []string{"/project/compose.yaml"},
		EnvFiles:   []string{},
		Profiles:   []string{},
	}}
	preflight := &fakePreflight{report: core.PreflightReport{
		Tools:       core.ToolInfo{DockerVersion: "29.10.0", ComposeVersion: "5.7.0", EndpointKind: "unix"},
		Diagnostics: []core.Diagnostic{},
	}}
	state := &fakeState{dir: "/state"}
	clock := &fakeClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	h := &engineHarness{resolver: resolver, preflight: preflight, state: state, feature: feature, registry: reg, clock: clock}
	h.engine = core.NewEngine(reg, resolver, preflight, state, clock)
	h.engine.CWD = func() (string, error) { return "/project", nil }
	h.engine.NewOperationID = func() string { return "op_test" }
	return h
}

func (h *engineHarness) run(t *testing.T, req core.Request) core.Result {
	t.Helper()
	sink := core.SinkFunc(func(s core.Stage) { h.sinkEvents = append(h.sinkEvents, s) })
	return h.engine.Run(context.Background(), req, sink)
}

func stageNames(res core.Result) []string {
	names := make([]string, 0, len(res.Stages))
	for _, s := range res.Stages {
		names = append(names, s.Name)
	}
	return names
}

func stageByName(t *testing.T, res core.Result, name string) core.Stage {
	t.Helper()
	for _, s := range res.Stages {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("stage %q not found in %v", name, stageNames(res))
	return core.Stage{}
}

func hasCategory(res core.Result, cat core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func TestEngineSuccessSharesResolvePreflightFeatureStages(t *testing.T) {
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			var out string
			err := core.RunStage(in.Reporter, "inspect_stack", "스택 조회", func() error {
				out = "snapshot"
				return nil
			})
			return out, err
		},
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "status", Target: core.TargetRequest{Path: "/project"}})

	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if res.SchemaVersion != core.SchemaVersion {
		t.Fatalf("schema version = %d", res.SchemaVersion)
	}
	if res.OperationID != "op_test" || res.Action != "status" {
		t.Fatalf("id/action = %q/%q", res.OperationID, res.Action)
	}
	if res.Target.ProjectDir != "/project" {
		t.Fatalf("target = %+v", res.Target)
	}
	if res.Data != "snapshot" {
		t.Fatalf("data = %v", res.Data)
	}
	if got, want := stageNames(res), []string{"resolve_target", "preflight", "inspect_stack"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	for _, name := range []string{"resolve_target", "preflight", "inspect_stack"} {
		if st := stageByName(t, res, name); st.Status != core.StageSucceeded {
			t.Fatalf("stage %s status = %q", name, st.Status)
		}
	}
	if res.Tools.DockerVersion != "29.10.0" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	if res.StartedAt.IsZero() || res.FinishedAt.IsZero() || res.FinishedAt.Before(res.StartedAt) {
		t.Fatalf("timestamps = %v / %v", res.StartedAt, res.FinishedAt)
	}
	if len(h.sinkEvents) == 0 {
		t.Fatal("progress sink received no events")
	}
	if !h.resolver.called || !h.preflight.called {
		t.Fatalf("resolver/preflight called = %v/%v", h.resolver.called, h.preflight.called)
	}
}

func TestEnginePassesSelectionAndFeatureOptions(t *testing.T) {
	var got core.Invocation
	feature := &testFeature{
		desc: core.Descriptor{
			Name: "logs", Title: "로그", Summary: "로그를 봅니다.", Usage: "dcraft logs",
			Section:  core.SectionInspect,
			ReadOnly: true, SupportsServices: true, AcceptsArgs: true,
		},
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			got = in
			return nil, nil
		},
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{
		Feature:     "logs",
		Services:    []string{"web", "db"},
		Args:        []string{"sh", "-c", "true"},
		Options:     map[string]string{"follow": "true"},
		Interactive: true,
	})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if strings.Join(got.Services, ",") != "web,db" {
		t.Fatalf("services = %v", got.Services)
	}
	if strings.Join(got.Args, " ") != "sh -c true" {
		t.Fatalf("args = %v", got.Args)
	}
	if got.Option("follow") != "true" || !got.BoolOption("follow") {
		t.Fatalf("options = %v", got.Options)
	}
	if !got.Interactive {
		t.Fatal("interactive flag not passed")
	}
	if got.Reporter == nil || got.Clock == nil {
		t.Fatal("invocation missing reporter/clock")
	}
}

func TestEngineTargetFailureStopsBeforePreflight(t *testing.T) {
	feature := &testFeature{desc: readOnlyDescriptor("status")}
	h := newHarness(t, feature)
	h.resolver.err = core.Fail(core.Diagnostic{
		Category: core.CategoryNoConfiguration, Severity: core.SeverityError,
		Message: "대상 폴더에서 Compose 설정 찾기 실패.",
	})
	h.resolver.target = core.Target{ProjectDir: "/project"}

	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryNoConfiguration) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if h.preflight.called {
		t.Fatal("preflight ran after target resolution failure")
	}
	if res.Target.ProjectDir != "/project" {
		t.Fatalf("partial target lost: %+v", res.Target)
	}
	if st := stageByName(t, res, "resolve_target"); st.Status != core.StageFailed {
		t.Fatalf("resolve stage = %q", st.Status)
	}
}

func TestEngineKeepsTargetWarningsOnSuccess(t *testing.T) {
	h := newHarness(t, &testFeature{desc: readOnlyDescriptor("status")})
	h.resolver.warns = []core.Diagnostic{{
		Category: core.CategoryMultipleConfigFiles, Severity: core.SeverityWarning,
		Message: "여러 파일", Detail: "using compose.yaml",
	}}
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryMultipleConfigFiles) {
		t.Fatalf("warning lost: %+v", res.Diagnostics)
	}
}

func TestEnginePreflightFailureStopsFeature(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return nil, nil },
	}
	h := newHarness(t, feature)
	h.preflight.report = core.PreflightReport{
		Tools: core.ToolInfo{ComposeVersion: "5.7.0"},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결 불가.", NextActions: []string{"시작"},
		}},
	}
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if ran {
		t.Fatal("feature ran despite fatal preflight")
	}
	if !hasCategory(res, core.CategoryEngineUnreachable) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics duplicated: %+v", res.Diagnostics)
	}
	if res.Tools.ComposeVersion != "5.7.0" {
		t.Fatalf("tools lost on failure: %+v", res.Tools)
	}
	if st := stageByName(t, res, "preflight"); st.Status != core.StageFailed {
		t.Fatalf("preflight stage = %q", st.Status)
	}
}

func TestEngineRequiresExplicitApprovalEvenWhenInteractive(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: core.Descriptor{
			Name: "install", Title: "설치", Summary: "Compose를 설치.", Usage: "dcraft install",
			Section:  core.SectionConfig,
			Approval: core.ApprovalInstall,
		},
		run: func(context.Context, core.Invocation) (any, error) { ran = true; return nil, nil },
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "install", Interactive: true})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if ran {
		t.Fatal("feature ran without approval")
	}
	if res.RequiredApproval != core.ApprovalInstall {
		t.Fatalf("required approval = %q", res.RequiredApproval)
	}
	if !hasCategory(res, core.CategoryApprovalRequired) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if h.preflight.called {
		t.Fatal("preflight ran before approval was settled")
	}

	h2 := newHarness(t, feature)
	res2 := h2.run(t, core.Request{Feature: "install", Approvals: []core.ApprovalKind{core.ApprovalInstall}})
	if res2.Outcome != core.OutcomeSuccess {
		t.Fatalf("approved outcome = %q (%+v)", res2.Outcome, res2.Diagnostics)
	}
	if !ran {
		t.Fatal("approved feature did not run")
	}
	if res2.RequiredApproval != "" {
		t.Fatalf("required approval leaked on success: %q", res2.RequiredApproval)
	}
}

func TestEngineFeatureErrorPreservesDiagnosticCategory(t *testing.T) {
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, core.RunStage(in.Reporter, "inspect_stack", "스택 조회", func() error {
				return core.Fail(core.Diagnostic{
					Category: core.CategoryInvalidConfiguration, Severity: core.SeverityError,
					Message: "Compose 설정을 읽을 수 없음.",
				})
			})
		},
	}
	res := newHarness(t, feature).run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryInvalidConfiguration) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "inspect_stack"); st.Status != core.StageFailed {
		t.Fatalf("stage = %q", st.Status)
	}
}

func TestEngineRawFeatureErrorBecomesInternalDiagnostic(t *testing.T) {
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run:  func(context.Context, core.Invocation) (any, error) { return nil, errors.New("boom") },
	}
	res := newHarness(t, feature).run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryInternal) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "execute"); st.Status != core.StageFailed {
		t.Fatalf("feature without stages should get a failed execute stage: %v", stageNames(res))
	}
}

func TestEngineVerifierRunsBeforeSuccessAndHistory(t *testing.T) {
	feature := &verifyingFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
			Section: core.SectionOperate,
		}},
		verify: func(ctx context.Context, in core.Invocation, payload any) error { return nil },
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "update"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if st := stageByName(t, res, "verify"); st.Status != core.StageSucceeded {
		t.Fatalf("verify stage = %q", st.Status)
	}
	if len(h.state.records) != 1 {
		t.Fatalf("history records = %+v", h.state.records)
	}
	rec := h.state.records[0]
	if rec.Action != "update" || rec.Outcome != core.OutcomeSuccess {
		t.Fatalf("record = %+v", rec)
	}
}

func TestEngineVerificationFailureMarksFailureAndSkipsHistory(t *testing.T) {
	feature := &verifyingFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
			Section: core.SectionOperate,
		}},
		verify: func(ctx context.Context, in core.Invocation, payload any) error {
			return errors.New("healthcheck timed out")
		},
	}
	h := newHarness(t, feature)
	res := h.run(t, core.Request{Feature: "update"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryVerificationFailed) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if len(h.state.records) != 0 {
		t.Fatalf("failed verification recorded a good version: %+v", h.state.records)
	}
}

func TestEngineSkipsHistoryForReadOnlyFeatures(t *testing.T) {
	h := newHarness(t, &testFeature{desc: readOnlyDescriptor("status")})
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if len(h.state.records) != 0 {
		t.Fatalf("read-only feature wrote history: %+v", h.state.records)
	}
}

func TestEngineHistoryFailureIsWarningNotFalseFailure(t *testing.T) {
	feature := &testFeature{desc: core.Descriptor{
		Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", RecordsHistory: true,
		Section: core.SectionOperate,
	}}
	h := newHarness(t, feature)
	h.state.err = errors.New("disk full")
	res := h.run(t, core.Request{Feature: "update"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q, want success with warning", res.Outcome)
	}
	if !hasCategory(res, core.CategoryHistoryFailed) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if st := stageByName(t, res, "history"); st.Status != core.StageFailed {
		t.Fatalf("history stage = %q", st.Status)
	}
}

func TestEngineCancellationReportsCanceledStageAndEffects(t *testing.T) {
	started := make(chan struct{})
	feature := &testFeature{
		desc: core.Descriptor{Name: "update", Title: "갱신", Summary: "갱신.", Usage: "dcraft update", Section: core.SectionOperate},
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	h := newHarness(t, feature)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.Result, 1)
	go func() { done <- h.engine.Run(ctx, core.Request{Feature: "update"}, nil) }()
	<-started
	cancel()
	res := <-done

	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q, want canceled", res.Outcome)
	}
	if !hasCategory(res, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	diag := res.Diagnostics[0]
	if !strings.Contains(diag.Message, "중단") {
		t.Fatalf("cancel message = %q", diag.Message)
	}
	if len(diag.NextActions) == 0 {
		t.Fatal("cancel diagnostic has no next action")
	}
	if len(h.state.records) != 0 {
		t.Fatal("canceled operation was recorded as history")
	}
}

func TestEngineCancellationBeforeWorkDoesNotCallFeature(t *testing.T) {
	ran := false
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run:  func(context.Context, core.Invocation) (any, error) { ran = true; return nil, nil },
	}
	h := newHarness(t, feature)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := h.engine.Run(ctx, core.Request{Feature: "status"}, nil)
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if ran || h.resolver.called {
		t.Fatal("work ran after cancellation")
	}
	if !hasCategory(res, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestEngineReportsCanceledEvenIfFeatureIgnoresContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			close(started)
			<-release
			return "finished anyway", nil
		},
	}
	h := newHarness(t, feature)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.Result, 1)
	go func() { done <- h.engine.Run(ctx, core.Request{Feature: "status"}, nil) }()
	<-started
	cancel()
	close(release)
	res := <-done
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q, want canceled", res.Outcome)
	}
	if !hasCategory(res, core.CategoryCanceled) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestEngineUnknownFeatureIsInternalError(t *testing.T) {
	h := newHarness(t, nil)
	res := h.run(t, core.Request{Feature: "nope"})
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryInternal) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestEngineDoesNotAddAnExecuteStageWhenFeatureReportsOne(t *testing.T) {
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, core.RunStage(in.Reporter, "inspect_stack", "스택 조회", func() error { return nil })
		},
	}
	res := newHarness(t, feature).run(t, core.Request{Feature: "status"})
	for _, s := range res.Stages {
		if s.Name == "execute" {
			t.Fatalf("unexpected fallback execute stage: %v", stageNames(res))
		}
	}
}

func TestContractEnumValuesAreStable(t *testing.T) {
	pairs := []struct{ got, want string }{
		{string(core.OutcomeSuccess), "success"},
		{string(core.OutcomeFailure), "failure"},
		{string(core.OutcomeCanceled), "canceled"},
		{string(core.StageStarted), "started"},
		{string(core.StageSucceeded), "succeeded"},
		{string(core.StageFailed), "failed"},
		{string(core.StageCanceled), "canceled"},
		{string(core.StageSkipped), "skipped"},
		{string(core.ApprovalInstall), "install"},
		{string(core.ApprovalScriptTrust), "script_trust"},
		{string(core.ApprovalPreviousGood), "previous_version"},
		{string(core.ApprovalDataRestore), "data_restore"},
		{string(core.ApprovalFileOverwrite), "file_overwrite"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Fatalf("enum value changed: %q, want %q", p.got, p.want)
		}
	}
}

type fakeIdentityReader struct {
	name  string
	err   error
	calls int
	got   core.Target
}

func (f *fakeIdentityReader) ReadProjectName(ctx context.Context, target core.Target) (string, error) {
	f.calls++
	f.got = target
	return f.name, f.err
}

func TestReadOnlyFeaturesReceiveTheResolvedProjectIdentity(t *testing.T) {
	var got core.Invocation
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			got = in
			return "ok", nil
		},
	}
	h := newHarness(t, feature)
	reader := &fakeIdentityReader{name: "hello-world-1"}
	h.engine.Identity = reader
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q (%+v)", res.Outcome, res.Diagnostics)
	}
	if reader.calls != 1 {
		t.Fatalf("identity calls = %d, want 1", reader.calls)
	}
	if res.Target.ProjectName != "hello-world-1" || got.Target.ProjectName != "hello-world-1" {
		t.Fatalf("result target = %+v, invocation target = %+v", res.Target, got.Target)
	}
	if got, want := stageNames(res), []string{"resolve_target", "preflight", "execute"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stages = %v, want %v (identity read must not add a stage)", got, want)
	}
}

func TestMutatingFeaturesResolveIdentityThemselves(t *testing.T) {
	feature := &testFeature{desc: core.Descriptor{
		Name: "up", Title: "시작", Summary: "시작.", Usage: "dcraft up",
		Section: core.SectionOperate,
	}}
	h := newHarness(t, feature)
	reader := &fakeIdentityReader{name: "hello-world-1"}
	h.engine.Identity = reader
	res := h.run(t, core.Request{Feature: "up"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if reader.calls != 0 {
		t.Fatalf("engine resolved identity for a mutating feature: %d", reader.calls)
	}
	if res.Target.ProjectName != "" {
		t.Fatalf("mutating target was rewritten: %+v", res.Target)
	}
}

func TestExplicitProjectNameSkipsTheIdentityRead(t *testing.T) {
	feature := &testFeature{desc: readOnlyDescriptor("status")}
	h := newHarness(t, feature)
	h.resolver.target.ProjectName = "explicit-name"
	reader := &fakeIdentityReader{name: "allocated-name"}
	h.engine.Identity = reader
	res := h.run(t, core.Request{Feature: "status"})
	if reader.calls != 0 {
		t.Fatalf("identity read ran with an explicit -p: %d", reader.calls)
	}
	if res.Target.ProjectName != "explicit-name" {
		t.Fatalf("target = %+v", res.Target)
	}
}

func TestIdentityReadFailureIsAWarningAndFallsBack(t *testing.T) {
	var got core.Invocation
	feature := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			got = in
			return "ok", nil
		},
	}
	h := newHarness(t, feature)
	h.engine.Identity = &fakeIdentityReader{err: errors.New("names.json is unreadable")}
	res := h.run(t, core.Request{Feature: "status"})
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !hasCategory(res, core.CategoryIdentityUnreadable) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryIdentityUnreadable && d.Severity != core.SeverityWarning {
			t.Fatalf("identity diagnostic severity = %q", d.Severity)
		}
	}
	if res.Target.ProjectName != "" || got.Target.ProjectName != "" {
		t.Fatalf("fallback changed the target: %+v / %+v", res.Target, got.Target)
	}
}
