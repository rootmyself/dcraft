package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestEnginePassesTerminalIOAndJSONMode(t *testing.T) {
	stdin := strings.NewReader("input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	var got core.IO
	var gotJSON bool
	feature := &testFeature{desc: readOnlyDescriptor("probe"), run: func(ctx context.Context, in core.Invocation) (any, error) {
		got = in.IO
		gotJSON = in.JSON
		return "ok", nil
	}}
	h := newHarness(t, feature)
	res := h.engine.Run(context.Background(), core.Request{
		Feature: "probe",
		IO:      core.IO{Stdin: stdin, Stdout: stdout, Stderr: stderr, TTY: true},
		JSON:    true,
	}, nil)
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", res)
	}
	if got.Stdin != stdin || got.Stdout != stdout || got.Stderr != stderr || !got.TTY {
		t.Fatalf("invocation IO = %+v", got)
	}
	if !gotJSON {
		t.Fatal("feature did not receive JSON mode")
	}
}

type inputFeature struct {
	*testFeature
	fields      []core.InputField
	inputErr    error
	withInputs  func(req core.Request, values map[string]string) (core.Request, error)
	inputsCalls int
	gotTarget   core.Target
}

func (f *inputFeature) Inputs(ctx context.Context, in core.Invocation) ([]core.InputField, error) {
	f.inputsCalls++
	f.gotTarget = in.Target
	return f.fields, f.inputErr
}

func (f *inputFeature) WithInputs(req core.Request, values map[string]string) (core.Request, error) {
	if f.withInputs != nil {
		return f.withInputs(req, values)
	}
	return req, nil
}

func TestEngineInputsResolvesWithoutRunningOrWritingHistory(t *testing.T) {
	feature := &inputFeature{
		testFeature: &testFeature{desc: core.Descriptor{
			Name:     "attach",
			Section:  core.SectionInspect,
			Title:    "접속",
			Summary:  "서비스에 접속합니다.",
			Usage:    "dcraft attach",
			ReadOnly: true,
		}},
		fields: []core.InputField{{
			Key:      "service",
			Label:    "서비스",
			Kind:     core.InputChoice,
			Required: true,
			Options:  []core.InputOption{{Value: "web", Label: "web"}},
		}},
	}
	h := newHarness(t, feature)
	report := h.engine.Inputs(context.Background(), core.Request{Feature: "attach"}, nil)
	if report.Failed() {
		t.Fatalf("report failed: %+v", report.Diagnostics)
	}
	if len(report.Fields) != 1 || report.Fields[0].Key != "service" {
		t.Fatalf("fields = %+v", report.Fields)
	}
	if feature.inputsCalls != 1 {
		t.Fatalf("Inputs calls = %d", feature.inputsCalls)
	}
	if report.Target.ProjectDir != "/project" {
		t.Fatalf("target = %+v", report.Target)
	}
	if !h.resolver.called || !h.preflight.called {
		t.Fatal("Inputs skipped target resolution or preflight")
	}
	if len(h.state.records) != 0 {
		t.Fatalf("Inputs wrote history: %+v", h.state.records)
	}
}

type fakeIdentity struct {
	name string
	err  error
	got  core.Target
}

func (f *fakeIdentity) ReadProjectName(ctx context.Context, target core.Target) (string, error) {
	f.got = target
	return f.name, f.err
}

func TestEngineInputsUsesTheReadOnlyProjectIdentity(t *testing.T) {
	feature := &inputFeature{testFeature: &testFeature{desc: core.Descriptor{
		Name:     "attach",
		Section:  core.SectionInspect,
		Title:    "접속",
		Summary:  "서비스에 접속합니다.",
		Usage:    "dcraft attach",
		ReadOnly: true,
	}}}
	h := newHarness(t, feature)
	identity := &fakeIdentity{name: "hello-world-1"}
	h.engine.Identity = identity
	report := h.engine.Inputs(context.Background(), core.Request{Feature: "attach"}, nil)
	if report.Failed() {
		t.Fatalf("report failed: %+v", report.Diagnostics)
	}
	if identity.got.ProjectDir != "/project" {
		t.Fatalf("identity target = %+v", identity.got)
	}
	if report.Target.ProjectName != "hello-world-1" {
		t.Fatalf("report project = %q", report.Target.ProjectName)
	}
	if feature.gotTarget.ProjectName != "hello-world-1" {
		t.Fatalf("feature target project = %q", feature.gotTarget.ProjectName)
	}
}

func TestEngineInputsReportsResolutionFailureWithoutCallingFeature(t *testing.T) {
	feature := &inputFeature{testFeature: &testFeature{desc: readOnlyDescriptor("attach")}}
	h := newHarness(t, feature)
	h.resolver.err = core.Fail(core.Diagnostic{
		Category: core.CategoryNoConfiguration,
		Severity: core.SeverityError,
		Message:  "Compose 설정을 찾지 못했습니다.",
	})
	report := h.engine.Inputs(context.Background(), core.Request{Feature: "attach"}, nil)
	if !report.Failed() {
		t.Fatal("Inputs succeeded despite a target failure")
	}
	if feature.inputsCalls != 0 {
		t.Fatalf("feature Inputs called after target failure")
	}
	if len(report.Diagnostics) == 0 || report.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
	if report.Result().ExitCode() != 1 {
		t.Fatalf("exit = %d", report.Result().ExitCode())
	}
}

func TestEngineInputsRequiresTheCapability(t *testing.T) {
	feature := &testFeature{desc: readOnlyDescriptor("plain")}
	h := newHarness(t, feature)
	report := h.engine.Inputs(context.Background(), core.Request{Feature: "plain"}, nil)
	if !report.Failed() || len(report.Diagnostics) == 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Diagnostics[0].Category != core.CategoryInternal {
		t.Fatalf("category = %q", report.Diagnostics[0].Category)
	}
}

type cancelFeature struct {
	*testFeature
}

func (f *cancelFeature) CanceledDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryLogsFailed, // normalized by the engine
		Severity:    core.SeverityError,
		Message:     "로그 스트림을 분리했습니다. 서비스는 계속 실행 중입니다.",
		NextActions: []string{"dcraft status로 상태를 확인하세요."},
	}
}

func TestEngineUsesAFeaturesCanceledEffectWhileKeepingExit130(t *testing.T) {
	feature := &cancelFeature{testFeature: &testFeature{
		desc: readOnlyDescriptor("logs"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			return nil, context.Canceled
		},
	}}
	h := newHarness(t, feature)
	res := h.engine.Run(context.Background(), core.Request{Feature: "logs"}, nil)
	if res.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if res.ExitCode() != 130 {
		t.Fatalf("exit = %d", res.ExitCode())
	}
	var canceled core.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Category == core.CategoryCanceled {
			canceled = d
		}
	}
	if canceled.Message != "로그 스트림을 분리했습니다. 서비스는 계속 실행 중입니다." {
		t.Fatalf("canceled diagnostic = %+v", res.Diagnostics)
	}
	if canceled.Severity != core.SeverityWarning {
		t.Fatalf("severity = %q", canceled.Severity)
	}
}

func TestDiagnosticExitCodeIsMachineReadableWhenPresent(t *testing.T) {
	code := 7
	diag := core.Diagnostic{
		Category: ctxCategoryForTest(),
		Severity: core.SeverityError,
		Message:  "원격 명령이 종료 코드 7로 실패했습니다.",
		ExitCode: &code,
	}
	data, err := json.Marshal(diag)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"exit_code":7`) {
		t.Fatalf("JSON = %s", data)
	}
	without := core.Diagnostic{Category: ctxCategoryForTest(), Severity: core.SeverityError, Message: "x"}
	data, err = json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "exit_code") {
		t.Fatalf("empty exit code was serialized: %s", data)
	}
}

func ctxCategoryForTest() core.Category { return core.CategoryExecFailed }
