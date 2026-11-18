package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type stubResolver struct{}

func (stubResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	return core.Target{ProjectDir: "/project", Files: []string{"/project/compose.yaml"}, EnvFiles: []string{}, Profiles: []string{}}, nil, nil
}

type stubPreflight struct{}

func (stubPreflight) Check(ctx context.Context, requires core.Requirements) core.PreflightReport {
	return core.PreflightReport{Tools: core.ToolInfo{DockerVersion: "29.10.0"}, Diagnostics: []core.Diagnostic{}}
}

type stubState struct{}

func (stubState) StackDir(target core.Target) (string, error) { return "/state", nil }
func (stubState) AppendOperation(target core.Target, rec core.OperationRecord) error {
	return nil
}

type stubClock struct{}

func (stubClock) Now() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }

type stubFeature struct {
	desc    core.Descriptor
	body    string
	runs    int
	outcome core.Outcome
}

func (f *stubFeature) Descriptor() core.Descriptor { return f.desc }

func (f *stubFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.runs++
	if f.outcome == core.OutcomeFailure {
		return nil, core.Fail(core.Diagnostic{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결할 수 없습니다.",
		})
	}
	return "payload", nil
}

func (f *stubFeature) Present(res core.Result, width int) string { return f.body }

func descriptor(name string, primary bool) core.Descriptor {
	return core.Descriptor{
		Name: name, Title: "제목 " + name, Summary: "요약 " + name,
		Usage: "dcraft " + name, ReadOnly: true, Primary: primary,
	}
}

func newTestModel(t *testing.T, features ...core.Feature) *Model {
	t.Helper()
	reg := core.NewRegistry()
	for _, f := range features {
		if err := reg.Register(f); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui" }
	return NewModel(context.Background(), Options{Engine: engine, Registry: reg})
}

func execute(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, execute(t, sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func feed(t *testing.T, m *Model, msgs []tea.Msg) {
	t.Helper()
	for _, msg := range msgs {
		nm, cmd := m.Update(msg)
		m = nm.(*Model)
		for _, next := range execute(t, cmd) {
			if _, ok := next.(tea.QuitMsg); ok {
				continue
			}
			nm, _ := m.Update(next)
			m = nm.(*Model)
		}
	}
}

func initModel(t *testing.T, m *Model) {
	t.Helper()
	feed(t, m, execute(t, m.Init()))
}

func isQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	for _, msg := range execute(t, cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

func TestPrimaryFeatureRendersKoreanStatusScreen(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "서비스 상태 본문"}
	m := newTestModel(t, status)
	initModel(t, m)
	view := m.View()
	for _, want := range []string{"dcraft", "제목 status", "서비스 상태 본문", "r 새로고침", "q 종료"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if status.runs != 1 {
		t.Fatalf("runs = %d, want 1", status.runs)
	}
}

func TestQuitKeysQuitWithoutRunningAnotherOperation(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	for _, key := range []string{"q", "ctrl+c"} {
		m := newTestModel(t, status)
		status.runs = 0
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if key == "ctrl+c" {
			_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		}
		if !isQuit(t, cmd) {
			t.Fatalf("key %q did not quit", key)
		}
		if status.runs != 0 {
			t.Fatalf("key %q ran a feature: %d", key, status.runs)
		}
	}
}

func TestMenuAndExecutionAreDerivedFromRegistry(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	logs := &stubFeature{desc: descriptor("logs", false), body: "로그 본문"}
	m := newTestModel(t, status, logs)
	initModel(t, m)
	view := m.View()
	for _, want := range []string{"작업", "제목 status", "제목 logs", "enter 실행"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu missing %q:\n%s", want, view)
		}
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if logs.runs != 1 {
		t.Fatalf("selected feature runs = %d, want 1", logs.runs)
	}
	if !strings.Contains(m.View(), "로그 본문") {
		t.Fatalf("selected result not shown:\n%s", m.View())
	}
}

func TestRefreshRerunsCurrentFeature(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newTestModel(t, status)
	initModel(t, m)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if status.runs != 2 {
		t.Fatalf("runs = %d, want 2", status.runs)
	}
}

func TestNoPrimaryScreenExplainsAndOnlyQuits(t *testing.T) {
	logs := &stubFeature{desc: descriptor("logs", false), body: "logs"}
	m := newTestModel(t, logs)
	if cmd := m.Init(); cmd != nil {
		t.Fatal("model without a primary feature started work")
	}
	view := m.View()
	if !strings.Contains(view, "기본 화면이 없습니다") {
		t.Fatalf("view = %s", view)
	}
	if !strings.Contains(view, "q 종료") {
		t.Fatalf("view missing quit hint: %s", view)
	}
}

func TestStageProgressIsRenderedWhileLoading(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newTestModel(t, status)
	nm, _ := m.Update(stageMsg{stage: core.Stage{
		Name: "preflight", Title: "환경 확인", Status: core.StageSucceeded,
	}})
	m = nm.(*Model)
	view := m.View()
	for _, want := range []string{"작업을 실행하는 중", "환경 확인", "완료"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestFailureRendersDiagnosticsAndStages(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), outcome: core.OutcomeFailure}
	m := newTestModel(t, status)
	initModel(t, m)
	view := m.View()
	for _, want := range []string{"작업 실패", "Docker Engine에 연결할 수 없습니다", "대상 확인", "환경 확인"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestViewDoesNotAdvertiseUnimplementedCommands(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newTestModel(t, status)
	initModel(t, m)
	view := m.View()
	for _, forbidden := range []string{"dcraft up", "dcraft update", "dcraft down", "dcraft logs", "dcraft backup"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("view advertises unimplemented %q:\n%s", forbidden, view)
		}
	}
}

func TestSuccessWarningsAreVisibleInTheTUI(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newTestModel(t, status)
	initModel(t, m)
	m.result.Diagnostics = []core.Diagnostic{{
		Category: core.CategoryMultipleConfigFiles, Severity: core.SeverityWarning,
		Message: "대상 폴더에 여러 개의 Compose 파일이 있습니다.",
	}}
	if view := m.View(); !strings.Contains(view, "여러 개의 Compose 파일") {
		t.Fatalf("warning not rendered:\n%s", view)
	}
}

func TestEnterWhileLoadingDoesNotStartSecondOperation(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	logs := &stubFeature{desc: descriptor("logs", false), body: "logs"}
	m := newTestModel(t, status, logs)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter while loading returned a command")
	}
	if logs.runs != 0 {
		t.Fatalf("logs ran while loading")
	}
}

// --- named environment selection -------------------------------------------

type stubEnvResolver struct {
	stubResolver
	envs       []core.Environment
	envErr     error
	envCalls   int
	lastTarget core.TargetRequest
}

func (r *stubEnvResolver) Environments(ctx context.Context, req core.TargetRequest, cwd string) ([]core.Environment, []core.Diagnostic, error) {
	r.envCalls++
	if r.envErr != nil {
		return nil, nil, r.envErr
	}
	return r.envs, nil, nil
}

func (r *stubEnvResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	r.lastTarget = req
	return core.Target{
		ProjectDir:  "/project",
		Files:       []string{"/project/compose.yaml"},
		EnvFiles:    []string{},
		Profiles:    []string{},
		Environment: req.Environment,
	}, nil, nil
}

func newEnvTestModel(t *testing.T, resolver *stubEnvResolver, target core.TargetRequest, features ...*stubFeature) *Model {
	t.Helper()
	reg := core.NewRegistry()
	for _, f := range features {
		if err := reg.Register(f); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	engine := core.NewEngine(reg, resolver, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui_env" }
	return NewModel(context.Background(), Options{Engine: engine, Registry: reg, Target: target})
}

func envResolver() *stubEnvResolver {
	return &stubEnvResolver{envs: []core.Environment{
		{Name: "dev", Files: []string{"/project/compose.yaml"}, Profiles: []string{"dev"}},
		{Name: "prod", Files: []string{"/project/compose.yaml"}, Profiles: []string{"prod"}},
	}}
}

func TestEnvironmentSelectionIsRequiredWhenAliasesExist(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{}, status)
	initModel(t, m)
	view := m.View()
	for _, want := range []string{"실행환경 선택", "dev", "prod", "직접 선택", "q 종료"} {
		if !strings.Contains(view, want) {
			t.Fatalf("selection view missing %q:\n%s", want, view)
		}
	}
	if status.runs != 0 {
		t.Fatalf("feature ran before an explicit environment selection: %d", status.runs)
	}
	if strings.Contains(view, "> dev") || strings.Contains(view, "> prod") {
		t.Fatalf("a default environment was preselected:\n%s", view)
	}
}

func TestEnvironmentSelectionRequiresAKeyBeforeRunning(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newEnvTestModel(t, envResolver(), core.TargetRequest{}, status)
	initModel(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("enter with no selection returned a command")
	}
	if status.runs != 0 {
		t.Fatalf("enter with no selection ran the feature")
	}
}

func TestEnvironmentSelectionKeyboardChoosesAndRuns(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "본문"}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{}, status)
	initModel(t, m)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))

	if status.runs != 1 {
		t.Fatalf("runs = %d, want 1", status.runs)
	}
	if resolver.lastTarget.Environment != "dev" {
		t.Fatalf("selected environment = %q, want dev", resolver.lastTarget.Environment)
	}
	if !strings.Contains(m.View(), "본문") {
		t.Fatalf("result not shown:\n%s", m.View())
	}
}

func TestEnvironmentSelectionCanChooseLaterEntry(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "본문"}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{}, status)
	initModel(t, m)
	for i := 0; i < 2; i++ {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(*Model)
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if resolver.lastTarget.Environment != "prod" {
		t.Fatalf("selected environment = %q, want prod", resolver.lastTarget.Environment)
	}
}

func TestQuitFromEnvironmentSelectionRunsNothing(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "body"}
	m := newEnvTestModel(t, envResolver(), core.TargetRequest{}, status)
	initModel(t, m)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = nm.(*Model)
	if !isQuit(t, cmd) {
		t.Fatal("q did not quit from the selection screen")
	}
	if status.runs != 0 {
		t.Fatalf("quitting from selection ran the feature: %d", status.runs)
	}
}

func TestExplicitEnvironmentSkipsTheSelectionScreen(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "본문"}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{Environment: "prod"}, status)
	initModel(t, m)
	if strings.Contains(m.View(), "실행환경 선택") {
		t.Fatalf("explicit --env still prompted:\n%s", m.View())
	}
	if status.runs != 1 {
		t.Fatalf("runs = %d, want 1", status.runs)
	}
	if resolver.lastTarget.Environment != "prod" {
		t.Fatalf("environment = %q", resolver.lastTarget.Environment)
	}
}

func TestNoAliasesRunsThePrimaryFeatureWithoutSelection(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "본문"}
	resolver := &stubEnvResolver{}
	m := newEnvTestModel(t, resolver, core.TargetRequest{}, status)
	initModel(t, m)
	if strings.Contains(m.View(), "실행환경 선택") {
		t.Fatalf("selection screen shown without aliases:\n%s", m.View())
	}
	if status.runs != 1 {
		t.Fatalf("runs = %d, want 1", status.runs)
	}
	if resolver.lastTarget.Environment != "" {
		t.Fatalf("environment changed without aliases: %q", resolver.lastTarget.Environment)
	}
}
