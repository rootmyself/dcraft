package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/cli"
)

type captureResolver struct {
	target core.Target
	warns  []core.Diagnostic
	err    error
	got    core.TargetRequest
	cwd    string
}

func (r *captureResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	r.got = req
	r.cwd = cwd
	if r.target.ProjectDir == "" {
		r.target.ProjectDir = req.Path
	}
	return r.target, r.warns, r.err
}

type emptyPreflight struct{}

func (emptyPreflight) Check(ctx context.Context, requires core.Requirements) core.PreflightReport {
	return core.PreflightReport{Diagnostics: []core.Diagnostic{}}
}

type memState struct{}

func (memState) StackDir(target core.Target) (string, error) { return "/state", nil }
func (memState) AppendOperation(target core.Target, rec core.OperationRecord) error {
	return nil
}

type fakeClock struct{ now time.Time }

func (f fakeClock) Now() time.Time { return f.now }

type stubFeature struct {
	desc    core.Descriptor
	run     func(ctx context.Context, in core.Invocation) (any, error)
	present func(res core.Result, width int) string
	ran     bool
}

func (f *stubFeature) Descriptor() core.Descriptor { return f.desc }

func (f *stubFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.ran = true
	if f.run == nil {
		return map[string]string{"hello": "world"}, nil
	}
	return f.run(ctx, in)
}

func (f *stubFeature) Present(res core.Result, width int) string {
	if f.present == nil {
		return "PRESENTED " + res.Action
	}
	return f.present(res, width)
}

func descriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name: name, Title: "제목 " + name, Summary: "요약 " + name,
		Usage: "dcraft " + name, ReadOnly: true,
	}
}

type harness struct {
	root     *cobra.Command
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	resolver *captureResolver
	homeArgs *core.TargetRequest
	homeErr  error
	homeRan  bool
}

func newHarness(t *testing.T, features ...core.Feature) *harness {
	t.Helper()
	return newInteractiveHarness(t, false, features...)
}

func newInteractiveHarness(t *testing.T, interactive bool, features ...core.Feature) *harness {
	t.Helper()
	reg := core.NewRegistry()
	for _, f := range features {
		if err := reg.Register(f); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	resolver := &captureResolver{target: core.Target{ProjectDir: "/project"}}
	clock := fakeClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	engine := core.NewEngine(reg, resolver, emptyPreflight{}, memState{}, clock)
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_test" }
	h := &harness{resolver: resolver}
	h.root = cli.New(cli.Options{
		Engine:      engine,
		Registry:    reg,
		Interactive: interactive,
		Stdout:      &h.stdout,
		Stderr:      &h.stderr,
		Home: func(ctx context.Context, target core.TargetRequest) error {
			h.homeRan = true
			h.homeArgs = &target
			return h.homeErr
		},
	})
	return h
}

func (h *harness) run(args ...string) error {
	h.root.SetArgs(args)
	return h.root.ExecuteContext(context.Background())
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *cli.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func TestCommandsAreDerivedFromRegistry(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	logs := &stubFeature{desc: descriptor("logs")}
	h := newHarness(t, status, logs)

	if err := h.run("--help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := h.stdout.String()
	for _, want := range []string{"사용법:", "사용 가능한 명령:", "status", "logs", "요약 status", "요약 logs", "플래그:", "도움말:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
	names := map[string]bool{}
	for _, cmd := range h.root.Commands() {
		names[cmd.Name()] = true
	}
	if !names["status"] || !names["logs"] {
		t.Fatalf("derived commands missing: %v", names)
	}
	if names["up"] || names["backup"] {
		t.Fatalf("unregistered command appeared: %v", names)
	}
}

func TestStatusJSONSuccessShapeAndPurity(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	if err := h.run("status", "--json"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON mode wrote stderr: %q", h.stderr.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(h.stdout.Bytes(), &raw); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, h.stdout.String())
	}
	for _, key := range []string{"schema_version", "operation_id", "action", "outcome", "target", "tools", "started_at", "finished_at", "stages", "diagnostics", "data"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("JSON missing %q: %s", key, h.stdout.String())
		}
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Outcome != core.OutcomeSuccess || res.Action != "status" || res.OperationID != "op_test" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Stages) < 2 {
		t.Fatalf("stages = %+v", res.Stages)
	}
	if res.Target.Files == nil || res.Target.Profiles == nil {
		t.Fatalf("target arrays must be [] not null: %+v", res.Target)
	}
}

func TestHumanSuccessUsesFeaturePresenter(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	if err := h.run("status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(h.stdout.String(), "PRESENTED status") {
		t.Fatalf("presenter not used: %q", h.stdout.String())
	}
}

func TestTargetFlagsReachResolver(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	err := h.run("status", "--path", "/work/app", "-f", "compose.yaml", "-f", "extra.yml",
		"--env-file", "local.env", "--profile", "dev", "--profile", "tools", "-p", "hello-world",
		"--env", "staging")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	got := h.resolver.got
	if got.Path != "/work/app" {
		t.Fatalf("path = %q", got.Path)
	}
	if strings.Join(got.Files, ",") != "compose.yaml,extra.yml" {
		t.Fatalf("files = %v", got.Files)
	}
	if strings.Join(got.EnvFiles, ",") != "local.env" {
		t.Fatalf("env files = %v", got.EnvFiles)
	}
	if strings.Join(got.Profiles, ",") != "dev,tools" {
		t.Fatalf("profiles = %v", got.Profiles)
	}
	if got.ProjectName != "hello-world" {
		t.Fatalf("project name = %q", got.ProjectName)
	}
	if got.Environment != "staging" {
		t.Fatalf("environment = %q", got.Environment)
	}
	if h.resolver.cwd != "/cwd" {
		t.Fatalf("cwd = %q", h.resolver.cwd)
	}
}

func TestEnvironmentFlagIsDocumentedInKorean(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	if err := h.run("status", "--help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "--env") || !strings.Contains(out, "실행환경") {
		t.Fatalf("help missing --env guidance:\n%s", out)
	}
}

func TestFeatureFlagsArgsAndServicesReachFeature(t *testing.T) {
	var got core.Invocation
	logs := &stubFeature{desc: core.Descriptor{
		Name: "logs", Title: "로그", Summary: "로그를 봅니다.", Usage: "dcraft logs",
		ReadOnly: true, AcceptsArgs: true, SupportsServices: true,
		Flags: []core.Flag{
			{Name: "follow", Shorthand: "l", Kind: core.FlagBool, Usage: "계속 출력"},
			{Name: "tail", Kind: core.FlagString, Default: "all", Usage: "마지막 줄 수"},
		},
	}, run: func(ctx context.Context, in core.Invocation) (any, error) {
		got = in
		return nil, nil
	}}
	h := newHarness(t, logs)
	if err := h.run("logs", "--follow", "--tail", "50", "-s", "web", "-s", "db", "sh", "-c", "true"); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !got.BoolOption("follow") || got.Option("tail") != "50" {
		t.Fatalf("options = %v", got.Options)
	}
	if strings.Join(got.Services, ",") != "web,db" {
		t.Fatalf("services = %v", got.Services)
	}
	if strings.Join(got.Args, " ") != "sh -c true" {
		t.Fatalf("args = %v", got.Args)
	}
}

func TestApprovalIsEnforcedByEngineAndExitCode(t *testing.T) {
	install := &stubFeature{desc: core.Descriptor{
		Name: "install", Title: "설치", Summary: "설치합니다.", Usage: "dcraft install",
		Approval: core.ApprovalInstall,
	}}
	h := newHarness(t, install)
	err := h.run("install", "--json")
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit = %d, want 2 (%v)", code, err)
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.RequiredApproval != core.ApprovalInstall {
		t.Fatalf("required approval = %q", res.RequiredApproval)
	}
	if install.ran {
		t.Fatal("feature ran without approval")
	}

	install.ran = false
	h2 := newHarness(t, install)
	if err := h2.run("install", "--approve", "install"); err != nil {
		t.Fatalf("approved install: %v", err)
	}
	if !install.ran {
		t.Fatal("approved feature did not run")
	}
}

func TestUnknownApprovalIsUsageError(t *testing.T) {
	install := &stubFeature{desc: core.Descriptor{
		Name: "install", Title: "설치", Summary: "설치합니다.", Usage: "dcraft install",
		Approval: core.ApprovalInstall,
	}}
	h := newHarness(t, install)
	err := h.run("install", "--approve", "everything")
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2", exitCode(err))
	}
	if install.ran {
		t.Fatal("feature ran with invalid approval")
	}
	if !strings.Contains(h.stderr.String(), "승인") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestFailureJSONHasNoStderrAndFailureExit(t *testing.T) {
	status := &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
		return nil, core.Fail(core.Diagnostic{
			Category: core.CategoryNoConfiguration, Severity: core.SeverityError,
			Message:     "대상 폴더에서 Compose 설정을 찾지 못했습니다.",
			NextActions: []string{"compose.yaml을 추가하세요."},
		})
	}}
	h := newHarness(t, status)
	err := h.run("status", "--json")
	if exitCode(err) != 1 {
		t.Fatalf("exit = %d, want 1", exitCode(err))
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON failure wrote stderr: %q", h.stderr.String())
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, h.stdout.String())
	}
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

type panicVerifyFeature struct {
	*stubFeature
}

func (f *panicVerifyFeature) Verify(context.Context, core.Invocation, any) error {
	panic("verifier exploded")
}

// The CLI must report a panicking verifier as one internal_error result with
// exit 1 instead of crashing the process.
func TestVerifierPanicExitsOneWithAResult(t *testing.T) {
	feature := &panicVerifyFeature{stubFeature: &stubFeature{
		desc: core.Descriptor{
			Name: "update", Title: "갱신", Summary: "갱신합니다.", Usage: "dcraft update",
		},
	}}
	h := newHarness(t, feature)
	err := h.run("update", "--json")
	if exitCode(err) != 1 {
		t.Fatalf("exit = %d, want 1", exitCode(err))
	}
	if h.stderr.Len() != 0 {
		t.Fatalf("JSON failure wrote stderr: %q", h.stderr.String())
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, h.stdout.String())
	}
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryInternal {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want internal_error", res.Diagnostics)
	}
	if res.Data != nil {
		t.Fatalf("data = %#v on failure, want omitted", res.Data)
	}
}

func TestHumanFailureGoesToStderrWithKoreanGuidance(t *testing.T) {
	status := &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
		return nil, core.Fail(core.Diagnostic{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결할 수 없습니다.", Detail: "failed to connect",
			NextActions: []string{"Docker Desktop을 시작하세요."},
		})
	}}
	h := newHarness(t, status)
	err := h.run("status")
	if exitCode(err) != 1 {
		t.Fatalf("exit = %d, want 1", exitCode(err))
	}
	if h.stdout.Len() != 0 {
		t.Fatalf("failure wrote stdout: %q", h.stdout.String())
	}
	out := h.stderr.String()
	for _, want := range []string{"작업 실패", "Docker Engine", "다음 행동", "Docker Desktop", "단계:", "대상 확인", "환경 확인"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestCanceledExitCodeAndJSON(t *testing.T) {
	status := &stubFeature{desc: descriptor("status"), run: func(ctx context.Context, in core.Invocation) (any, error) {
		return nil, context.Canceled
	}}
	h := newHarness(t, status)
	err := h.run("status", "--json")
	if exitCode(err) != 130 {
		t.Fatalf("exit = %d, want 130", exitCode(err))
	}
	var res core.Result
	if err := json.Unmarshal(h.stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", res.Outcome)
	}
}

func TestNonInteractiveNoArgsDoesNotOpenHome(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	err := h.run()
	if exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2", exitCode(err))
	}
	if h.homeRan {
		t.Fatal("TUI opened in a non-interactive context")
	}
	out := h.stderr.String()
	for _, want := range []string{"대화형 터미널이 아니어서", "dcraft status", "--json", "도움말"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stderr missing %q:\n%s", want, out)
		}
	}
}

func TestInteractiveNoArgsOpensHomeWithTargetFlags(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newInteractiveHarness(t, true, status)
	if err := h.run("--path", "/work/app"); err != nil {
		t.Fatalf("home: %v", err)
	}
	if !h.homeRan {
		t.Fatal("home did not run")
	}
	if h.homeArgs == nil || h.homeArgs.Path != "/work/app" {
		t.Fatalf("home target = %+v", h.homeArgs)
	}
}

func TestJSONWithoutCommandIsUsageError(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newInteractiveHarness(t, true, status)
	if err := h.run("--json"); exitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2", exitCode(err))
	}
	if !strings.Contains(h.stderr.String(), "명령을 지정") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	if h.homeRan {
		t.Fatal("TUI opened for --json without a command")
	}
}

func TestWarningsDoNotContaminateStdout(t *testing.T) {
	status := &stubFeature{desc: descriptor("status")}
	h := newHarness(t, status)
	h.resolver.warns = []core.Diagnostic{{
		Category: core.CategoryMultipleConfigFiles, Severity: core.SeverityWarning,
		Message: "여러 Compose 파일이 있습니다.",
	}}
	if err := h.run("status"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(h.stdout.String(), "여러 Compose 파일") {
		t.Fatalf("warning mixed into stdout: %q", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "여러 Compose 파일") {
		t.Fatalf("warning missing from stderr: %q", h.stderr.String())
	}
}
