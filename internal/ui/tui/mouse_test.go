package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

func mousePress(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func click(t *testing.T, m *Model, x, y int, at time.Time) tea.Cmd {
	t.Helper()
	m.mouseNow = func() time.Time { return at }
	nm, cmd := m.Update(mousePress(x, y))
	*m = *(nm.(*Model))
	return cmd
}

func wheel(t *testing.T, m *Model, x, y int, down bool) {
	t.Helper()
	button := tea.MouseButtonWheelUp
	if down {
		button = tea.MouseButtonWheelDown
	}
	nm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress})
	*m = *(nm.(*Model))
}

// findRow returns the first drawn row containing needle and its cell offset.
func findRow(t *testing.T, m *Model, needle string) (int, int) {
	t.Helper()
	for i, line := range strings.Split(m.View(), "\n") {
		if at := strings.Index(line, needle); at >= 0 {
			return i, visual.Width(line[:at])
		}
	}
	t.Fatalf("view has no row containing %q:\n%s", needle, m.View())
	return 0, 0
}

func clickRow(t *testing.T, m *Model, needle string, at time.Time) tea.Cmd {
	t.Helper()
	y, x := findRow(t, m, needle)
	return click(t, m, x+1, y, at)
}

func TestMouseSidebarSelectsAndRequiresDoubleClickForDangerousAction(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	y, _ := findRow(t, m, "데이터 복원")
	click(t, m, 4, y, now)
	if !strings.Contains(strings.Split(m.View(), "\n")[y], m.theme.Glyphs.Sel+" 데이터 복원") {
		t.Fatalf("click did not select restore:\n%s", m.View())
	}
	if m.confirmOpen() {
		t.Fatal("single click opened confirmation")
	}
	click(t, m, 4, y, now.Add(100*time.Millisecond))
	if !m.confirmOpen() {
		t.Fatalf("double click did not open confirmation:\n%s", m.View())
	}
}

func TestMouseSidebarWheelMovesSelection(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	wheel(t, m, 4, 4, true)
	if !strings.Contains(m.View(), m.theme.Glyphs.Sel+" 로그 보기") {
		t.Fatalf("wheel did not move to the next menu item:\n%s", m.View())
	}
}

func TestMouseMenuClickSelectsAndDoubleClickRuns(t *testing.T) {
	status := &stubFeature{desc: sectionDescriptor("status", "스택 상태", "상태 확인.", core.SectionInspect, true, false), body: "상태 본문"}
	logs := &stubFeature{desc: sectionDescriptor("logs", "로그 보기", "실시간 로그를 따라봅니다.", core.SectionInspect, false, false), body: "로그 본문"}
	m := newTestModel(t, status, logs)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 99, 30)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "(logs)", now)
	if m.features[m.selected].Descriptor().Name != "logs" {
		t.Fatalf("click did not select logs:\n%s", m.View())
	}
	if logs.runs != 0 {
		t.Fatal("single click ran the feature")
	}
	cmd := clickRow(t, m, "(logs)", now.Add(100*time.Millisecond))
	feed(t, m, execute(t, cmd))
	if logs.runs != 1 {
		t.Fatalf("double click runs = %d, want 1", logs.runs)
	}
}

func TestMouseEnvironmentClickSelectsAndDoubleClickRuns(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "본문"}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{}, status)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 30)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "dev", now)
	if m.envSelected != 0 {
		t.Fatalf("click did not select dev: %d", m.envSelected)
	}
	if status.runs != 0 {
		t.Fatal("single click ran the feature")
	}
	cmd := clickRow(t, m, "dev", now.Add(100*time.Millisecond))
	feed(t, m, execute(t, cmd))
	if status.runs != 1 {
		t.Fatalf("double click runs = %d, want 1", status.runs)
	}
	if resolver.lastTarget.Environment != "dev" {
		t.Fatalf("target environment = %q, want dev", resolver.lastTarget.Environment)
	}
}

// The environment administration screen is a selectable list too: a click
// moves the marker, a double click takes the action, and a switch never runs
// the primary feature.
func TestMouseEnvironmentAdministrationSelectsAndSwitchesWithoutRunning(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "old result"}
	adminDesc := descriptor("aliases", false)
	adminDesc.EnvironmentAdmin = true
	admin := &stubFeature{desc: adminDesc}
	resolver := envResolver()
	m := newEnvTestModel(t, resolver, core.TargetRequest{Environment: "dev"}, status, admin)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.phase != phaseEnvironmentActions {
		t.Fatalf("phase = %v, want the administration screen", m.phase)
	}
	// The primary feature runs once on startup, before the screen opened.
	baseline := status.runs
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	wheel(t, m, 10, 8, true)
	if m.envAction != 1 {
		t.Fatalf("wheel left the action at %d, want 1", m.envAction)
	}
	clickRow(t, m, msgEnvNewAlias, now)
	if m.envAction != 0 {
		t.Fatalf("click selected action %d, want 0", m.envAction)
	}
	if m.phase != phaseEnvironmentActions || status.runs != baseline {
		t.Fatalf("single click took the action: phase=%v runs=%d want %d", m.phase, status.runs, baseline)
	}
	clickRow(t, m, msgEnvSwitch, now.Add(time.Second))
	if m.envAction != 1 {
		t.Fatalf("click selected action %d, want 1", m.envAction)
	}
	cmd := clickRow(t, m, msgEnvSwitch, now.Add(time.Second+100*time.Millisecond))
	feed(t, m, execute(t, cmd))
	if m.phase != phaseSelectingEnvironment || !m.envSwitch {
		t.Fatalf("double click did not open the switch list: phase=%v switch=%v", m.phase, m.envSwitch)
	}
	clickRow(t, m, "prod", now.Add(2*time.Second))
	if m.envSelected != 1 || m.phase != phaseSelectingEnvironment {
		t.Fatalf("click did not select prod without switching: selected=%d phase=%v", m.envSelected, m.phase)
	}
	clickRow(t, m, "prod", now.Add(2*time.Second+100*time.Millisecond))
	if m.phase != phaseReady || m.envSwitch {
		t.Fatalf("double click did not finish the switch: phase=%v switch=%v", m.phase, m.envSwitch)
	}
	if m.target.Environment != "prod" || m.facts.Environment != "prod" {
		t.Fatalf("target=%q header=%q, want prod", m.target.Environment, m.facts.Environment)
	}
	if status.runs != baseline {
		t.Fatalf("the switch ran the primary feature: runs=%d, want %d", status.runs, baseline)
	}
	if view := m.View(); strings.Contains(view, "old result") {
		t.Fatalf("the switch kept the stale result:\n%s", view)
	}
}

func TestMouseServiceCheckboxTogglesOncePerDoubleClick(t *testing.T) {
	catalog := newCatalogStatus(defaultServiceCatalog())
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 본문"}
	m := newServiceModel(t, catalog, stop)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 120, 36)
	chooseFeature(t, m, "stop")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "api", now)
	if !m.serviceChecked[0] || m.serviceWhole {
		t.Fatalf("click did not check api: %v whole=%v", m.serviceChecked, m.serviceWhole)
	}
	clickRow(t, m, "api", now.Add(100*time.Millisecond))
	if !m.serviceChecked[0] {
		t.Fatal("second press of a double click toggled api back off")
	}
	clickRow(t, m, msgServiceWhole, now.Add(2*time.Second))
	if !m.serviceWhole || anyChecked(m.serviceChecked) {
		t.Fatalf("whole-stack row did not restore the scope: %v", m.serviceChecked)
	}
}

func TestMouseWizardCardClickSelectsAndDoubleClickAdvances(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	wiz := &wizardStub{desc: desc, steps: wizardSteps(), preview: "OK"}
	m := newWizardModel(t, wiz)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "worker", now)
	if m.wizardChoice != 1 {
		t.Fatalf("click chose %d, want 1", m.wizardChoice)
	}
	cmd := clickRow(t, m, "worker", now.Add(100*time.Millisecond))
	feed(t, m, execute(t, cmd))
	if m.wizardAnswers["service"] != "worker" {
		t.Fatalf("double click answer = %q, want worker", m.wizardAnswers["service"])
	}
}

func TestMouseWizardCheckboxTogglesOncePerDoubleClick(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	wiz := &wizardStub{desc: desc, preview: "OK", steps: core.WizardSpec{
		Title: "다중 선택",
		Steps: []core.WizardStep{{
			Name: "opts", Title: "옵션", Multi: true,
			Options: []core.WizardOption{{Value: "web", Label: "web"}, {Value: "worker", Label: "worker"}},
		}},
	}}
	m := newWizardModel(t, wiz)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "worker", now)
	if !m.wizardChecked["worker"] {
		t.Fatal("click did not check worker")
	}
	clickRow(t, m, "worker", now.Add(100*time.Millisecond))
	if !m.wizardChecked["worker"] {
		t.Fatal("second press of a double click unchecked worker")
	}
}

func TestMouseWizardListItemClickSelectsAndDoubleClickEdits(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	wiz := &wizardStub{desc: desc, preview: "OK", steps: core.WizardSpec{
		Title: "목록",
		Steps: []core.WizardStep{{
			Name: "items", Title: "항목", List: true, Default: "alpha",
		}},
	}}
	m := newWizardModel(t, wiz)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "* alpha", now)
	if m.wizardChoice != 0 {
		t.Fatalf("click chose %d, want 0", m.wizardChoice)
	}
	clickRow(t, m, "* alpha", now.Add(100*time.Millisecond))
	if !m.wizardEditing || m.wizardInput != "alpha" {
		t.Fatalf("double click did not enter edit: editing=%v input=%q", m.wizardEditing, m.wizardInput)
	}
}

func TestMouseFormFocusesFieldsAndChoices(t *testing.T) {
	form := openExecForm(t)
	resize(t, form.m, 120, 36)
	m := form.m
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "명령:", now)
	if m.form.focus != 1 {
		t.Fatalf("click focused %d, want 1", m.form.focus)
	}
	clickRow(t, m, "서비스 인스턴스:", now.Add(time.Second))
	if m.form.focus != 0 {
		t.Fatalf("click focused %d, want 0", m.form.focus)
	}
	clickRow(t, m, "◉ web · proj-web-2", now.Add(2*time.Second))
	if m.form.choices["instance"] != 0 {
		t.Fatalf("choice = %d, want 0", m.form.choices["instance"])
	}
	clickRow(t, m, "◉ web · proj-web-2", now.Add(2*time.Second+100*time.Millisecond))
	if m.form.focus != 1 {
		t.Fatalf("double click did not act as enter: focus=%d", m.form.focus)
	}
}

func TestMouseResultRowDoubleClickOpensDetail(t *testing.T) {
	longImage := "registry.example.com/team/a-very-long-image-name-that-does-not-fit:2026-11-20"
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: &core.Table{
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "이미지", Weight: 45, Min: 16, Max: 34},
		},
		Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
			{Text: "api"},
			{Text: longImage, Detail: longImage},
		}}},
	}}}}
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: doc}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 60, 24)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	clickRow(t, m, "api", now)
	if m.detailOpen {
		t.Fatal("single click opened the detail view")
	}
	clickRow(t, m, "api", now.Add(100*time.Millisecond))
	if !m.detailOpen {
		t.Fatalf("double click did not open the detail view:\n%s", m.View())
	}
}

func TestMouseWheelScrollsResultContent(t *testing.T) {
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockTable, Table: &core.Table{
		Columns: []core.Column{
			{Header: "서비스", Weight: 30, Min: 12, Max: 24},
			{Header: "상태", Fixed: 11},
		},
		Rows: func() []core.Row {
			rows := make([]core.Row, 0, 30)
			for i := 0; i < 30; i++ {
				rows = append(rows, core.Row{Selectable: true, Cells: []core.Cell{
					{Text: "svc" + string(rune('a'+i%26))}, {Text: "실행 중"},
				}})
			}
			return rows
		}(),
	}}}}
	f := &documentOnlyFeature{desc: descriptor("status", true), doc: doc}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	initModel(t, m)
	resize(t, m, 80, 18)
	if !m.bodyScrolls() {
		t.Fatal("the result body does not scroll; the test proves nothing")
	}
	wheel(t, m, 10, 8, true)
	if m.body.Top == 0 {
		t.Fatalf("wheel did not scroll the result body:\n%s", m.View())
	}
}

func TestMouseConfirmModalButtonsDoubleClick(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Cancel: a double click closes the panel and runs nothing.
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	resize(t, m, 120, 36)
	view := m.View()
	for _, want := range []string{"[ " + msgModalApprove + " ]", "[ " + msgModalCancel + " ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation buttons missing %q:\n%s", want, view)
		}
	}
	y, x := findRow(t, m, "[ "+msgModalCancel+" ]")
	click(t, m, x+2, y, now)
	if m.phase != phaseConfirm {
		t.Fatal("single click dismissed the confirmation")
	}
	click(t, m, x+2, y, now.Add(100*time.Millisecond))
	if m.phase != phaseReady || up.runs != 0 {
		t.Fatalf("cancel button: phase=%v runs=%d", m.phase, up.runs)
	}

	// Approve: a double click runs the feature through the same gate.
	m = newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	resize(t, m, 120, 36)
	y, x = findRow(t, m, "[ "+msgModalApprove+" ]")
	click(t, m, x+2, y, now)
	cmd := click(t, m, x+2, y, now.Add(100*time.Millisecond))
	feed(t, m, execute(t, cmd))
	if up.runs != 1 {
		t.Fatalf("approve button runs = %d, want 1", up.runs)
	}
}

// A wheel over the confirmation panel scrolls its band and never approves.
func TestMouseWheelOverModalScrollsWithoutApproving(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	up := &stubFeature{desc: mutatingDescriptor("up"), body: "시작 본문"}
	m := newTestModel(t, status, up)
	m.theme = *plainTheme()
	openConfirm(t, m)
	resize(t, m, 40, 18)
	up.runs = 0

	wheel(t, m, 20, 9, true)
	if m.confirmScroll == 0 {
		t.Fatalf("wheel did not scroll the modal band:\n%s", m.View())
	}
	if up.runs != 0 || m.phase != phaseConfirm {
		t.Fatalf("wheel approved the confirmation: runs=%d phase=%v", up.runs, m.phase)
	}
}

func TestMouseHelpHasNoButtonsAndFooterIsNotClickable(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	before := m.selected
	lines := strings.Split(m.View(), "\n")
	click(t, m, 10, len(lines)-1, now)
	if m.selected != before {
		t.Fatalf("footer click moved the selection:\n%s", m.View())
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.helpOpen {
		t.Fatal("help did not open")
	}
	if view := m.View(); strings.Contains(view, "[ "+msgModalApprove+" ]") {
		t.Fatalf("help panel drew confirmation buttons:\n%s", view)
	}
	wheel(t, m, 10, 8, true)
	if !m.helpOpen {
		t.Fatal("wheel closed the help panel")
	}
}

// A list step is a selectable list: its items have no options and allow a
// custom value, so it looks like a text field to the wizard, but the wheel
// must move the cursor over the items plus the new-item row instead of
// scrolling the body. A plain text field keeps the wheel for the body.
func TestMouseWheelMovesTheListEditorCursor(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	wiz := &wizardStub{desc: desc, preview: "OK", steps: core.WizardSpec{
		Title: "목록",
		Steps: []core.WizardStep{{
			Name: "items", Title: "항목", List: true, AllowCustom: true, Optional: true,
			Default: "alpha\nbeta\ngamma",
		}},
	}}
	m := newWizardModel(t, wiz)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	if m.phase != phaseWizardStep || len(m.wizardItems) != 3 || !m.wizardSpec.Steps[0].List {
		t.Fatalf("fixture is not a 3-item list step: phase=%v items=%v", m.phase, m.wizardItems)
	}
	// The list starts on the new-item row, below the last item.
	if m.wizardChoice != len(m.wizardItems) {
		t.Fatalf("cursor starts at %d, want the new-item row %d", m.wizardChoice, len(m.wizardItems))
	}

	// Up moves one item at a time, the same direction the keyboard takes.
	wheel(t, m, 10, 8, false)
	if m.wizardChoice != 2 {
		t.Fatalf("wheel up left the cursor at %d, want 2", m.wizardChoice)
	}
	wheel(t, m, 10, 8, true)
	if m.wizardChoice != 3 {
		t.Fatalf("wheel down left the cursor at %d, want the new-item row 3", m.wizardChoice)
	}

	// Both ends clamp: the wheel never wraps past the first item or the
	// new-item row, exactly like Up/Down on the same list.
	for i := 0; i < 5; i++ {
		wheel(t, m, 10, 8, false)
	}
	if m.wizardChoice != 0 {
		t.Fatalf("wheel up wrapped past the first item to %d, want 0", m.wizardChoice)
	}
	for i := 0; i < 6; i++ {
		wheel(t, m, 10, 8, true)
	}
	if m.wizardChoice != len(m.wizardItems) {
		t.Fatalf("wheel down wrapped past the new-item row to %d, want %d", m.wizardChoice, len(m.wizardItems))
	}

	// A plain text field keeps the wheel for body scrolling: the cursor is not
	// a list position there.
	text := &wizardStub{desc: desc, preview: "OK", steps: core.WizardSpec{
		Title: "참조",
		Steps: []core.WizardStep{{
			Name: "ref", Title: "원본 참조", Prompt: "원본 참조 입력.", AllowCustom: true,
		}},
	}}
	m2 := newWizardModel(t, text)
	m2.theme = *plainTheme()
	openWizard(t, m2)
	resize(t, m2, 120, 36)
	if !m2.wizardTextInput() || m2.wizardSpec.Steps[0].List {
		t.Fatalf("fixture is not a plain text field: %+v", m2.wizardSpec.Steps[0])
	}
	choice := m2.wizardChoice
	wheel(t, m2, 10, 8, true)
	if m2.wizardChoice != choice {
		t.Fatalf("wheel moved the cursor of a text field: %d -> %d", choice, m2.wizardChoice)
	}
}

// While a list item is being edited the keyboard blocks Up/Down, so a card
// click must not move the selection either: Enter would otherwise write the
// buffer into a different item than the one the user was editing. The click is
// ignored, exactly like the keys.
func TestMouseCardClickDoesNotMoveTheSelectionWhileEditing(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	wiz := &wizardStub{desc: desc, preview: "OK", steps: core.WizardSpec{
		Title: "목록",
		Steps: []core.WizardStep{{
			Name: "items", Title: "항목", List: true, AllowCustom: true, Optional: true,
			Default: "alpha\nbeta\ngamma",
		}},
	}}
	m := newWizardModel(t, wiz)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Enter edit on item 0: select it, then double click to open the field.
	clickRow(t, m, "* alpha", now)
	clickRow(t, m, "* alpha", now.Add(100*time.Millisecond))
	if !m.wizardEditing || m.wizardChoice != 0 {
		t.Fatalf("fixture did not enter edit on item 0: editing=%v choice=%d", m.wizardEditing, m.wizardChoice)
	}
	typeKeys(t, m, "-edited")

	// A single click on item 2 must leave the selection on item 0.
	clickRow(t, m, "* gamma", now.Add(time.Second))
	if m.wizardChoice != 0 {
		t.Fatalf("card click moved the edit selection to %d, want 0", m.wizardChoice)
	}
	if !m.wizardEditing {
		t.Fatal("card click cancelled the edit instead of ignoring it")
	}

	// The second press of a double click is ignored for the same reason, so the
	// edit stays on item 0 until Enter.
	clickRow(t, m, "* gamma", now.Add(time.Second+100*time.Millisecond))
	if !m.wizardEditing || m.wizardChoice != 0 {
		t.Fatalf("double click escaped the edit on item 0: editing=%v choice=%d", m.wizardEditing, m.wizardChoice)
	}
	if got := m.wizardItems[2]; got != "gamma" {
		t.Fatalf("item 2 = %q, want it unchanged", got)
	}

	// The new-item row is the same list cursor: clicking it while editing would
	// point the cursor past the items, and Enter would index out of range.
	clickRow(t, m, cursorGlyph, now.Add(2*time.Second))
	if m.wizardChoice != 0 {
		t.Fatalf("new-item click moved the edit cursor to %d, want 0", m.wizardChoice)
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.wizardItems[0]; got != "alpha-edited" {
		t.Fatalf("item 0 = %q, want the edited buffer", got)
	}
	if got := m.wizardItems[2]; got != "gamma" {
		t.Fatalf("item 2 = %q, want it unchanged", got)
	}

	// Outside an edit the new-item row is still clickable: the cursor moves
	// there and a typed value adds an item.
	clickRow(t, m, cursorGlyph, now.Add(2*time.Second))
	if m.wizardChoice != len(m.wizardItems) {
		t.Fatalf("new-item click left the cursor at %d, want %d", m.wizardChoice, len(m.wizardItems))
	}
}
