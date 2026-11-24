package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// The sidebar is drawn in every phase, so a click on it must resolve in every
// phase. This walks the screens the shell can show and asserts that a drawn nav
// row is a target.

func sidebarClickModel(t *testing.T, wizard bool) *Model {
	t.Helper()
	var m *Model
	if wizard {
		f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
			{Name: "service", Title: "서비스 선택", Prompt: "서비스 선택.",
				Options: []core.WizardOption{{Value: "web", Label: "web"}, {Value: "worker", Label: "worker"}}},
		}}}
		f.desc.Wizard = true
		m = newWizardModel(t, f)
	} else {
		m, _ = navModel(t)
	}
	resize(t, m, 120, 34)
	return m
}

// navTargetFor reports the click target that covers a drawn feature row. It
// matches by registration index, which is stable, instead of by title, which a
// fixture may leave empty.
func navTargetFor(t *testing.T, m *Model, index int) (hitTarget, bool) {
	t.Helper()
	for _, hit := range m.mouseTargets() {
		if hit.kind == hitNavItem && hit.index == index {
			return hit, true
		}
	}
	return hitTarget{}, false
}

// Every screen the sidebar is drawn on accepts a click on its rows. The screen
// decides what the click means, but the target always exists.
func TestSidebarRowsAreClickableInEveryDrawnPhase(t *testing.T) {
	ready := sidebarClickModel(t, false)
	if !ready.sidebarVisible() {
		t.Fatal("sidebar is not visible at 120 columns")
	}
	for i := range ready.features {
		if _, ok := navTargetFor(t, ready, i); !ok {
			t.Fatalf("ready phase has no target for feature %d", i)
		}
	}

	// A wizard step draws the sidebar and the answers below it.
	wizard := sidebarClickModel(t, true)
	openWizard(t, wizard)
	if wizard.phase != phaseWizardStep {
		t.Fatalf("phase = %v, want a wizard step", wizard.phase)
	}
	if _, ok := navTargetFor(t, wizard, wizard.featureIndex("status")); !ok {
		t.Fatal("wizard step has no sidebar target")
	}

	// The service selection screen draws it too.
	services := sidebarClickModel(t, false)
	services.phase = phaseSelectingServices
	if _, ok := navTargetFor(t, services, 0); !ok {
		t.Fatal("service selection has no sidebar target")
	}

	// A running mutating operation and the live log both keep the pane.
	busy := sidebarClickModel(t, false)
	busy.phase = phaseLoading
	busy.active = &stubFeature{desc: mutatingDescriptor("up")}
	if _, ok := navTargetFor(t, busy, 0); !ok {
		t.Fatal("running operation has no sidebar target")
	}
}

// doubleClickSidebar presses the drawn row of a feature twice. Both presses land
// on the layout the screen actually drew.
func doubleClickSidebar(t *testing.T, m *Model, index int) {
	t.Helper()
	hit, ok := navTargetFor(t, m, index)
	if !ok {
		t.Fatalf("no sidebar target for feature %d", index)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		m.mouseNow = func() time.Time { return now.Add(time.Duration(i) * 50 * time.Millisecond) }
		nm, _ := m.Update(mousePress(hit.x+2, hit.y))
		*m = *(nm.(*Model))
	}
}

// A screen with typed answers asks before discarding them, and declining the
// confirmation keeps every answer.
func TestSidebarClickOnAWizardAsksBeforeDiscardingInput(t *testing.T) {
	m := sidebarClickModel(t, true)
	openWizard(t, m)
	// Type one answer so the screen counts as holding input.
	m.wizardAnswers["service"] = "web"
	statusIndex := m.featureIndex("status")
	doubleClickSidebar(t, m, statusIndex)
	if !m.discardOpen {
		t.Fatalf("second click did not ask before discarding:\n%s", m.View())
	}
	if m.phase != phaseWizardStep || m.wizardAnswers["service"] != "web" {
		t.Fatal("asking already discarded the answer")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !m.overlaySpecMissing() || m.wizardAnswers["service"] != "web" {
		t.Fatalf("esc did not keep the answer: %+v", m.wizardAnswers)
	}
	if m.phase != phaseWizardStep {
		t.Fatalf("esc left the wizard: phase = %v", m.phase)
	}
}

// overlaySpecMissing reports that no panel is drawn. The confirmation is the
// only thing standing between the wizard and the reader.
func (m *Model) overlaySpecMissing() bool {
	_, ok := m.overlaySpec()
	return !ok
}

// Accepting the confirmation discards the answers and lands on the chosen
// feature. It is reached by a click, so the same path the reader takes is the
// one under test.
func TestSidebarClickAcceptDiscardsInputAndNavigates(t *testing.T) {
	m := sidebarClickModel(t, true)
	openWizard(t, m)
	m.wizardAnswers["service"] = "web"
	statusIndex := m.featureIndex("status")
	doubleClickSidebar(t, m, statusIndex)
	if !m.discardOpen {
		t.Fatal("the click did not ask")
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = *(nm.(*Model))
	feed(t, m, execute(t, cmd))
	if m.discardOpen {
		t.Fatal("enter did not close the confirmation")
	}
	if m.wizardAnswers != nil {
		t.Fatalf("answers survived the accept: %+v", m.wizardAnswers)
	}
	if m.selected != statusIndex {
		t.Fatalf("selected = %d, want %d", m.selected, statusIndex)
	}
}

// A running mutating operation reports instead of navigating, because dcraft
// cannot resume progress the screen would lose.
func TestSidebarClickDuringARunReportsInsteadOfNavigating(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	m.phase = phaseLoading
	m.active = &stubFeature{desc: mutatingDescriptor("up")}
	m.selected = m.featureIndex("status")
	before := m.selected
	m.activateNav(m.featureIndex("restore"), true)
	if m.selected != before {
		t.Fatalf("a blocked click moved the cursor: %d", m.selected)
	}
	if !strings.Contains(m.View(), msgNavBlocked) {
		t.Fatalf("a blocked click said nothing:\n%s", m.View())
	}
}

// A result screen navigates on the click: there is no input to lose.
func TestSidebarClickOnAResultNavigatesImmediately(t *testing.T) {
	m, _ := navModel(t)
	resize(t, m, 120, 34)
	m.phase = phaseReady
	m.result = &core.Result{Outcome: core.OutcomeSuccess, Action: "status"}
	m.activateNav(m.featureIndex("restore"), true)
	if m.selected != m.featureIndex("restore") {
		t.Fatalf("selected = %d", m.selected)
	}
	if m.discardOpen {
		t.Fatal("a read-only result asked before navigating")
	}
}
