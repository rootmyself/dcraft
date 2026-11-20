package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// catalogStatus is the primary status screen plus the read-only service
// catalog capability the TUI asks for before a SupportsServices operation.
type catalogStatus struct {
	*stubFeature
	services []core.ServiceStatus
	err      error
	calls    int
}

func (c *catalogStatus) Services(ctx context.Context, in core.Invocation) ([]core.ServiceStatus, error) {
	c.calls++
	return c.services, c.err
}

func defaultServiceCatalog() []core.ServiceStatus {
	return []core.ServiceStatus{
		{Name: "api", State: "running", Health: "healthy", Instances: 1, Configured: true},
		{Name: "web", State: "exited", Health: "none", Instances: 2, Configured: true},
	}
}

type serviceCaptureFeature struct {
	desc core.Descriptor
	runs int
	got  core.Invocation
	body string
}

func (f *serviceCaptureFeature) Descriptor() core.Descriptor { return f.desc }
func (f *serviceCaptureFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.runs++
	f.got = in
	return "payload", nil
}
func (f *serviceCaptureFeature) Present(res core.Result, width int) string { return f.body }

type servicePlannerFeature struct {
	*serviceCaptureFeature
	planText string
	plans    int
	planGot  core.Invocation
}

func (f *servicePlannerFeature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	f.plans++
	f.planGot = in
	return core.Plan{Text: f.planText}, nil
}

func serviceDescriptor(name string) core.Descriptor {
	d := descriptor(name, false)
	d.ReadOnly = false
	d.RecordsHistory = true
	d.SupportsServices = true
	return d
}

func newServiceModel(t *testing.T, catalog *catalogStatus, features ...core.Feature) *Model {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(catalog); err != nil {
		t.Fatalf("Register catalog: %v", err)
	}
	for _, f := range features {
		if err := reg.Register(f); err != nil {
			t.Fatalf("Register %s: %v", f.Descriptor().Name, err)
		}
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui_services" }
	return NewModel(context.Background(), Options{Engine: engine, Registry: reg})
}

func newCatalogStatus(services []core.ServiceStatus) *catalogStatus {
	return &catalogStatus{
		stubFeature: &stubFeature{desc: descriptor("status", true), body: "상태 본문"},
		services:    services,
	}
}

func chooseFeature(t *testing.T, m *Model, name string) {
	t.Helper()
	found := false
	for i, f := range m.features {
		if f.Descriptor().Name == name {
			m.selected = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("feature %q is not in the menu", name)
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = *(nm.(*Model))
	feed(t, m, execute(t, cmd))
}

func press(m *Model, msg tea.Msg) tea.Cmd {
	nm, cmd := m.Update(msg)
	*m = *(nm.(*Model))
	return cmd
}

func pressRune(m *Model, r string) tea.Cmd {
	return press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)})
}

func pressSpace(m *Model) tea.Cmd {
	return press(m, tea.KeyMsg{Type: tea.KeySpace})
}

func feedCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	feed(t, m, execute(t, cmd))
}

func TestServiceFeatureShowsTheCurrentStackBeforeRunning(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)

	chooseFeature(t, m, "stop")
	if m.phase != phaseSelectingServices {
		t.Fatalf("phase = %v, want service selection; view:\n%s", m.phase, m.View())
	}
	if catalog.calls != 1 {
		t.Fatalf("catalog calls = %d, want 1", catalog.calls)
	}
	if stop.runs != 0 {
		t.Fatal("stop ran before the scope was confirmed")
	}
	view := m.View()
	for _, want := range []string{
		"서비스 선택", "전체 (스택 전체)", "[x] 전체",
		"api", "실행 중", "healthy", "인스턴스 1개",
		"web", "중지됨", "인스턴스 2개",
		"space", "[enter] 실행", "[esc] 취소", "[q] 종료",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("service screen missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[x] api") || strings.Contains(view, "[x] web") {
		t.Fatalf("individual services were preselected:\n%s", view)
	}
}

func TestServiceSelectionTogglesAndRunsOnlySelectedServices(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)
	chooseFeature(t, m, "stop")

	press(m, tea.KeyMsg{Type: tea.KeyDown}) // whole -> api
	if view := m.View(); !strings.Contains(view, "> [ ] api") {
		t.Fatalf("cursor is not on api:\n%s", view)
	}
	pressSpace(m)
	if view := m.View(); !strings.Contains(view, "[x] api") || !strings.Contains(view, "[ ] 전체") {
		t.Fatalf("api was not selected or whole stayed selected:\n%s", view)
	}
	press(m, tea.KeyMsg{Type: tea.KeyDown}) // api -> web
	pressSpace(m)
	if view := m.View(); !strings.Contains(view, "[x] web") {
		t.Fatalf("web was not selected:\n%s", view)
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))

	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirmation; view:\n%s", m.phase, m.View())
	}
	view := m.View()
	// The confirmation is now a panel; the scope row and the title are its
	// fields, so the same facts are asserted on the panel.
	for _, want := range []string{"범위:", "api, web", msgConfirmTitle} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
	if stop.runs != 0 {
		t.Fatal("stop ran before the confirmation")
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if stop.runs != 1 {
		t.Fatalf("runs = %d, want 1", stop.runs)
	}
	if got := strings.Join(stop.got.Services, ","); got != "api,web" {
		t.Fatalf("services = %q, want api,web", got)
	}
}

func TestServiceSelectionWholeStackKeepsPreviousBehavior(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)
	chooseFeature(t, m, "stop")

	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if !strings.Contains(m.View(), "스택 전체") || !strings.Contains(m.View(), "범위:") {
		t.Fatalf("whole-stack scope not shown:\n%s", m.View())
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if stop.runs != 1 {
		t.Fatalf("runs = %d, want 1", stop.runs)
	}
	if len(stop.got.Services) != 0 {
		t.Fatalf("whole-stack run carried a selection: %v", stop.got.Services)
	}
}

func TestServiceSelectionCancelRunsNothing(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)
	chooseFeature(t, m, "stop")

	press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready", m.phase)
	}
	if stop.runs != 0 {
		t.Fatal("esc ran the feature")
	}
	if !strings.Contains(m.View(), "아무것도 실행하지 않았습니다") {
		t.Fatalf("cancel notice missing:\n%s", m.View())
	}

	chooseFeature(t, m, "stop")
	cmd := pressRune(m, "q")
	if !isQuit(t, cmd) {
		t.Fatal("q did not quit from the service screen")
	}
	if stop.runs != 0 {
		t.Fatal("q ran the feature")
	}
}

func TestServiceListFailureBlocksTheOperation(t *testing.T) {
	catalog := newCatalogStatus(nil)
	catalog.err = core.Fail(core.Diagnostic{
		Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
		Message: "스택을 읽지 못했습니다.",
	})
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)

	chooseFeature(t, m, "stop")
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready failure", m.phase)
	}
	if stop.runs != 0 {
		t.Fatal("stop ran with an unreadable service list")
	}
	view := m.View()
	for _, want := range []string{"작업 실패", "스택을 읽지 못했습니다"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure screen missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "다음 작업을 실행할까요?") {
		t.Fatalf("failure still offered a confirmation:\n%s", view)
	}
}

func TestNonServiceFeatureSkipsTheServiceScreen(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	plain := &serviceCaptureFeature{desc: mutatingDescriptor("plain"), body: "본문"}
	m := newServiceModel(t, catalog, plain)
	initModel(t, m)

	chooseFeature(t, m, "plain")
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want direct confirmation", m.phase)
	}
	if catalog.calls != 0 {
		t.Fatalf("catalog was queried for a feature without services: %d", catalog.calls)
	}
}

func TestPrimaryServiceFeatureShowsTheScreenAtStartup(t *testing.T) {
	d := descriptor("status", true)
	d.ReadOnly = true
	d.SupportsServices = true
	catalog := &catalogStatus{
		stubFeature: &stubFeature{desc: d, body: "상태 본문"},
		services:    defaultServiceCatalog(),
	}
	m := newServiceModel(t, catalog)
	initModel(t, m)

	if m.phase != phaseSelectingServices {
		t.Fatalf("phase = %v, want service selection; view:\n%s", m.phase, m.View())
	}
	if catalog.runs != 0 {
		t.Fatal("the primary feature ran before the scope was chosen")
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if catalog.runs != 1 {
		t.Fatalf("runs = %d, want 1", catalog.runs)
	}
}

func TestServiceSelectionFeedsTheUpdatePlan(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	update := &servicePlannerFeature{
		serviceCaptureFeature: &serviceCaptureFeature{
			desc: serviceDescriptor("update"),
			body: "갱신 본문",
		},
		planText: "계획: api 이미지 취득 후 적용",
	}
	m := newServiceModel(t, catalog, update)
	initModel(t, m)

	chooseFeature(t, m, "update")
	press(m, tea.KeyMsg{Type: tea.KeyDown}) // whole -> api
	pressSpace(m)
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))

	if update.plans != 1 {
		t.Fatalf("plans = %d, want 1", update.plans)
	}
	if got := strings.Join(update.planGot.Services, ","); got != "api" {
		t.Fatalf("plan services = %q, want api", got)
	}
	view := m.View()
	for _, want := range []string{"계획: api 이미지 취득 후 적용", "범위:", "api"} {
		if !strings.Contains(view, want) {
			t.Fatalf("plan confirmation missing %q:\n%s", want, view)
		}
	}
	if update.runs != 0 {
		t.Fatal("update ran before the plan was confirmed")
	}
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if update.runs != 1 || strings.Join(update.got.Services, ",") != "api" {
		t.Fatalf("run = %d services=%v", update.runs, update.got.Services)
	}
}

func TestUnconfiguredServiceFactsCannotBeSelected(t *testing.T) {
	services := append(defaultServiceCatalog(), core.ServiceStatus{
		Name: "old", State: "exited", Health: "none", Instances: 1, Configured: false,
	})
	catalog := newCatalogStatus(services)
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	initModel(t, m)
	chooseFeature(t, m, "stop")

	// whole -> api -> web -> old
	press(m, tea.KeyMsg{Type: tea.KeyDown})
	press(m, tea.KeyMsg{Type: tea.KeyDown})
	press(m, tea.KeyMsg{Type: tea.KeyDown})
	pressSpace(m)
	view := m.View()
	if strings.Contains(view, "[x] old") {
		t.Fatalf("an unconfigured service was selectable:\n%s", view)
	}
	if !strings.Contains(view, "현재 설정에 없음") {
		t.Fatalf("unconfigured service is not labelled:\n%s", view)
	}
}

func TestExecFormKeepsItsOwnSelectionAndSkipsTheGenericScreen(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{
		desc: inputDescriptor("attach"),
		mode: core.LiveAttach,
	}, fields: []core.InputField{
		{Key: "instance", Label: "서비스 인스턴스", Kind: core.InputChoice, Required: true,
			Options: []core.InputOption{{Value: "web|0", Label: "web · proj-web-1"}}},
		{Key: "command", Label: "명령", Kind: core.InputText, Required: true},
	}}
	m := newServiceModel(t, catalog, input)
	initModel(t, m)

	chooseFeature(t, m, "attach")
	if m.form == nil {
		t.Fatalf("exec form did not open; view:\n%s", m.View())
	}
	if catalog.calls != 0 {
		t.Fatalf("generic service screen queried for exec: %d", catalog.calls)
	}
	if strings.Contains(m.View(), "서비스 선택") {
		t.Fatalf("generic service screen replaced the exec form:\n%s", m.View())
	}
}
