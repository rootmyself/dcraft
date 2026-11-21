package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

func visualWidth(s string) int { return visual.Width(s) }

func contextBackground() context.Context { return context.Background() }

func scopeServices() []core.ServiceStatus {
	return []core.ServiceStatus{
		{Name: "api", State: "running", Health: "healthy", Instances: 1, Configured: true,
			Image: "ghcr.io/acme/api:2.14.0", Ports: "8080→8080"},
		{Name: "worker", State: "running", Health: "unhealthy", Instances: 1, Configured: true,
			Image: "ghcr.io/acme/worker:2.14.0"},
		{Name: "legacy", State: "exited", Health: "none", Instances: 0, Configured: false,
			Image: "ghcr.io/acme/legacy:1.0"},
	}
}

// manyServices is a stack large enough that no approved terminal shows every
// row at once.
func manyServices(n int) []core.ServiceStatus {
	var out []core.ServiceStatus
	for i := 0; i < n; i++ {
		out = append(out, core.ServiceStatus{
			Name: fmt.Sprintf("svc-%02d", i), State: "running", Health: "healthy",
			Instances: 1, Configured: true,
			Image: fmt.Sprintf("ghcr.io/acme/svc-%02d:1.0", i),
			Ports: fmt.Sprintf("%d→80", 8000+i),
		})
	}
	return out
}

// scopeModel builds a model already on the scope screen for the given services.
func scopeModel(t *testing.T, services []core.ServiceStatus) *Model {
	t.Helper()
	catalog := newCatalogStatus(services)
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	m.theme = *plainTheme()
	initModel(t, m)
	chooseFeature(t, m, "stop")
	if m.phase != phaseSelectingServices {
		t.Fatalf("phase = %v, want service selection; view:\n%s", m.phase, m.View())
	}
	return m
}

func pressDown(m *Model) { press(m, tea.KeyMsg{Type: tea.KeyDown}) }
func pressUp(m *Model)   { press(m, tea.KeyMsg{Type: tea.KeyUp}) }

func viewOf(m *Model) string { return m.View() }

// tableHeaderLine returns the rendered table header: the only line that names
// the selection, service and state columns together.
func tableHeaderLine(t *testing.T, view string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, msgServiceColumnSelect) &&
			strings.Contains(line, msgServiceColumnService) &&
			strings.Contains(line, msgServiceColumnState) {
			return line
		}
	}
	t.Fatalf("no table header:\n%s", view)
	return ""
}

// rowWith returns the first rendered line that names the given row.
func rowWith(t *testing.T, view, name string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no row for %q:\n%s", name, view)
	return ""
}

// assertFits fails when any rendered line is wider than the terminal or the
// frame is not exactly the terminal height.
func assertFits(t *testing.T, m *Model, width, height int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != height {
		t.Fatalf("%dx%d: %d rows:\n%s", width, height, len(lines), m.View())
	}
	for i, line := range lines {
		if got := visualWidth(line); got > width {
			t.Fatalf("%dx%d: row %d is %d cells: %q", width, height, i, got, line)
		}
	}
}

// The scope screen is a table, not a checklist: state, health, instances, image
// and port are readable per row, and the approved column order is
// 선택 · 서비스 · 상태 · 인스턴스 · health · 이미지 · 포트.
func TestScopeTableRendersStateHealthInstancesImageAndPort(t *testing.T) {
	m := scopeModel(t, scopeServices())
	resize(t, m, 120, 36)
	view := viewOf(m)

	header := tableHeaderLine(t, view)
	last := -1
	for _, col := range []string{
		msgServiceColumnSelect, msgServiceColumnService, msgServiceColumnState,
		msgServiceColumnInstances, msgServiceColumnHealth, msgServiceColumnImage, msgServiceColumnPort,
	} {
		idx := strings.Index(header, col)
		if idx < 0 {
			t.Fatalf("header %q missing %q", header, col)
		}
		if idx < last {
			t.Fatalf("column %q is out of the approved order in %q", col, header)
		}
		last = idx
	}

	for _, want := range []string{
		"api", "✓ 실행 중", "정상", "ghcr.io/acme/api:2.14.0", "8080→8080",
		"worker", "▲ 비정상",
		"legacy", "■ 중지됨",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("scope table missing %q:\n%s", want, view)
		}
	}
	// The instance count is a number, right aligned in its own column.
	apiRow := rowWith(t, view, "api")
	if !strings.Contains(apiRow, " 1 ") {
		t.Fatalf("api row has no instance count: %q", apiRow)
	}
	// The cursor is a gutter glyph plus bold (bold only on a colored theme).
	// The glyph alone is the channel a plain frame keeps, so it is what the
	// plain-theme assertion checks.
	pressDown(m)
	if apiRow := rowWith(t, viewOf(m), "api"); !strings.Contains(apiRow, "▸[ ] api") {
		t.Fatalf("the cursor is not on api: %q", apiRow)
	}
	pressDown(m)
	if row := rowWith(t, viewOf(m), "worker"); !strings.Contains(row, "▸[ ] worker") {
		t.Fatalf("the cursor did not move to worker: %q", row)
	}
	assertFits(t, m, 120, 36)
}

// The whole-stack scope is the default selection and stays pinned above the
// table so narrowing a scope is always reversible. space on it clears every
// individual choice — the previous toggle behavior, now in the table.
func TestScopeTableWholeStackIsTheDefaultAndSpaceRestoresIt(t *testing.T) {
	m := scopeModel(t, scopeServices())
	resize(t, m, 120, 36)
	if !m.serviceWhole {
		t.Fatal("whole-stack scope is not the default")
	}
	if row := rowWith(t, viewOf(m), msgServiceWhole); !strings.Contains(row, "[x]") {
		t.Fatalf("whole-stack row is not checked:\n%s", row)
	}

	pressDown(m)
	pressSpace(m)
	if m.serviceWhole {
		t.Fatal("space on a service left the whole-stack scope active")
	}
	if row := rowWith(t, viewOf(m), msgServiceWhole); strings.Contains(row, "[x]") {
		t.Fatalf("whole-stack row stayed checked:\n%s", row)
	}

	pressUp(m)
	pressSpace(m)
	if !m.serviceWhole {
		t.Fatal("space on the whole-stack row did not restore the whole scope")
	}
	if row := rowWith(t, viewOf(m), msgServiceWhole); !strings.Contains(row, "[x]") {
		t.Fatalf("whole-stack row is not checked again:\n%s", row)
	}
}

// An unconfigured service is shown with its facts, marked in text, and cannot
// be selected. The marker is a Korean label, not only a muted color.
func TestScopeTableShowsUnconfiguredServiceAsNotSelectable(t *testing.T) {
	m := scopeModel(t, scopeServices())
	resize(t, m, 120, 36)
	// whole -> api -> worker -> legacy
	pressDown(m)
	pressDown(m)
	pressDown(m)
	pressSpace(m)

	if m.serviceChecked[2] {
		t.Fatal("an unconfigured service was selected")
	}
	view := viewOf(m)
	if row := rowWith(t, view, "legacy"); strings.Contains(row, "[x]") {
		t.Fatalf("unconfigured row shows a selection:\n%s", row)
	}
	if !strings.Contains(view, msgServiceNotConfigured) {
		t.Fatalf("unconfigured service is not labelled:\n%s", view)
	}
	if row := rowWith(t, view, msgServiceNotConfigured); !strings.Contains(row, "legacy") {
		t.Fatalf("the label does not name the row:\n%s", row)
	}
	if !m.serviceWhole {
		t.Fatal("an unselectable row changed the scope")
	}
}

// The confirmed scope is echoed unchanged: a subset never widens to the whole
// stack, and the run carries exactly the names that were checked.
func TestScopeTableConfirmedSubsetIsEchoedUnchanged(t *testing.T) {
	m := scopeModel(t, scopeServices())
	resize(t, m, 120, 36)
	pressDown(m) // api
	pressSpace(m)
	feedCmd(t, m, press(m, tea.KeyMsg{Type: tea.KeyEnter}))

	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want confirmation; view:\n%s", m.phase, m.View())
	}
	confirm := viewOf(m)
	row, ok := modalRow(confirm, "범위:")
	if !ok || !strings.Contains(row, "api") {
		t.Fatalf("confirmation does not echo the chosen scope:\n%s", confirm)
	}
	if strings.Contains(row, "스택 전체") {
		t.Fatalf("a subset silently widened to the whole stack:\n%s", confirm)
	}
}

// Twenty-five services: the header never scrolls, the selection follows the
// cursor, the counter states the drawn range, and every row can be reached.
func TestScopeTableScrollsWithAFixedHeaderAndReachesEveryRow(t *testing.T) {
	services := manyServices(25)
	m := scopeModel(t, services)
	resize(t, m, 80, 24)

	header := tableHeaderLine(t, viewOf(m))
	seen := map[string]bool{}
	for i := 0; i <= len(services); i++ {
		view := viewOf(m)
		if got := tableHeaderLine(t, view); got != header {
			t.Fatalf("row %d: header moved:\n%q\n%q", i, header, got)
		}
		for _, svc := range services {
			if strings.Contains(view, svc.Name) {
				seen[svc.Name] = true
			}
		}
		assertFits(t, m, 80, 24)
		if i < len(services) {
			pressDown(m)
		}
	}
	for _, svc := range services {
		if !seen[svc.Name] {
			t.Fatalf("row %q was never visible while walking the table", svc.Name)
		}
	}

	// The counter is the approved `first-last/total` form over the service rows.
	counter := ""
	for _, line := range strings.Split(viewOf(m), "\n") {
		if idx := strings.Index(line, "/25"); idx >= 0 {
			start := strings.LastIndex(line[:idx], " ")
			counter = strings.TrimSpace(line[start+1 : idx+3])
			break
		}
	}
	if counter == "" {
		t.Fatalf("no first-last/total counter:\n%s", viewOf(m))
	}
	if !strings.HasSuffix(counter, "/25") {
		t.Fatalf("counter %q does not count the 25 service rows", counter)
	}

	// At the end the last row is visible and the first one has scrolled away.
	view := viewOf(m)
	if !strings.Contains(view, "svc-24") {
		t.Fatalf("the last service is not visible at the end:\n%s", view)
	}
	if strings.Contains(view, "svc-00") {
		t.Fatalf("the window did not scroll on a 25-row stack:\n%s", view)
	}

	// Moving back to the top returns to the first row without losing the header.
	press(m, tea.KeyMsg{Type: tea.KeyHome})
	if row := rowWith(t, viewOf(m), msgServiceWhole); !strings.Contains(row, "▸") {
		t.Fatalf("home did not put the cursor on the whole-stack row:\n%s", row)
	}
	if view := viewOf(m); !strings.Contains(view, "svc-00") {
		t.Fatalf("home did not return to the first service row:\n%s", view)
	}
	if got := tableHeaderLine(t, viewOf(m)); got != header {
		t.Fatalf("header moved after scrolling back:\n%q\n%q", header, got)
	}
}

// Compact keeps 선택 · 서비스 · 상태, and folds health into the state cell with
// its icon and Korean label so nothing depends on color.
func TestScopeTableCompactKeepsThreeColumnsAndFoldsHealthIntoState(t *testing.T) {
	m := scopeModel(t, scopeServices())
	m.theme = *plainTheme()
	resize(t, m, 48, 20)
	view := viewOf(m)

	header := tableHeaderLine(t, view)
	for _, want := range []string{msgServiceColumnSelect, msgServiceColumnService, msgServiceColumnState} {
		if !strings.Contains(header, want) {
			t.Fatalf("compact header %q missing %q", header, want)
		}
	}
	for _, gone := range []string{
		msgServiceColumnInstances, msgServiceColumnHealth,
		msgServiceColumnImage, msgServiceColumnPort,
	} {
		if strings.Contains(header, gone) {
			t.Fatalf("compact header %q still has %q", header, gone)
		}
	}
	// The unhealthy worker states its health instead of its container state.
	worker := rowWith(t, view, "worker")
	if !strings.Contains(worker, "▲ 비정상") {
		t.Fatalf("compact did not fold unhealthy health into the state cell: %q", worker)
	}
	// A normal service keeps its container state.
	if api := rowWith(t, view, "api"); !strings.Contains(api, "실행 중") {
		t.Fatalf("compact lost the running state: %q", api)
	}
	assertFits(t, m, 48, 20)
}

// "확인 중" is health, not a container state: it also folds into the state cell
// in compact.
func TestScopeTableCompactShowsStartingHealth(t *testing.T) {
	services := []core.ServiceStatus{
		{Name: "api", State: "running", Health: "starting", Instances: 1, Configured: true},
	}
	m := scopeModel(t, services)
	resize(t, m, 48, 20)
	if row := rowWith(t, viewOf(m), "api"); !strings.Contains(row, "◐ 확인 중") {
		t.Fatalf("compact did not fold starting health into the state cell: %q", row)
	}
}

// Every approved size: exactly the terminal height, no line wider than the
// terminal, and the essential scope facts survive 48 columns.
func TestScopeTableFitsEveryApprovedSize(t *testing.T) {
	for _, size := range [][2]int{{48, 20}, {80, 24}, {120, 36}} {
		m := scopeModel(t, manyServices(25))
		resize(t, m, size[0], size[1])
		assertFits(t, m, size[0], size[1])
		view := viewOf(m)
		for _, want := range []string{
			msgServiceSelectTitle, msgServiceWhole, msgServiceColumnService, msgServiceColumnState,
		} {
			if !strings.Contains(view, want) {
				t.Fatalf("%dx%d: missing %q:\n%s", size[0], size[1], want, view)
			}
		}
	}
}

// A wizard collects its own answers, including any service choice, so it never
// detours through the generic scope table.
func TestWizardFeatureSkipsTheScopeTable(t *testing.T) {
	catalog := newCatalogStatus(scopeServices())
	d := serviceDescriptor("guided")
	d.Wizard = true
	guided := &wizardStub{desc: d, steps: core.WizardSpec{
		Title: "안내 설정",
		Steps: []core.WizardStep{{Name: "service", Title: "서비스", Prompt: "서비스를 선택하세요."}},
	}}
	reg := core.NewRegistry()
	if err := reg.Register(catalog); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(guided); err != nil {
		t.Fatal(err)
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui_services" }
	m := NewModel(contextBackground(), Options{Engine: engine, Registry: reg})
	m.theme = *plainTheme()
	initModel(t, m)

	chooseFeature(t, m, "guided")
	if catalog.calls != 0 {
		t.Fatalf("generic scope catalog queried for a wizard feature: %d", catalog.calls)
	}
	if m.phase != phaseWizardStep {
		t.Fatalf("phase = %v, want a wizard step; view:\n%s", m.phase, m.View())
	}
	if strings.Contains(m.View(), msgServiceSelectTitle) {
		t.Fatalf("the scope table replaced the wizard:\n%s", m.View())
	}
}
