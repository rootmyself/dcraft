package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rootmyself/dcraft/internal/core"
)

func TestWizardListEditingAndReview(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "volumes", Title: "Volumes", List: true, AllowCustom: true},
		{Name: "next", Title: "Next", AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	key := func(k tea.KeyType) { feed(t, m, []tea.Msg{tea.KeyMsg{Type: k}}) }
	text := func(s string) { feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}}) }

	key(tea.KeyEnter)
	if !strings.Contains(m.View(), msgWizardNeedValue) {
		t.Fatalf("required list lacks guidance:\n%s", m.View())
	}
	text("data:/data:ro,Z")
	key(tea.KeyEnter)
	text("cache:/cache")
	key(tea.KeyEnter)
	key(tea.KeyUp)
	key(tea.KeyEnter)
	text("-changed")
	key(tea.KeyEsc)
	if !strings.Contains(m.View(), "* cache:/cache") || strings.Contains(m.View(), "cache:/cache-changed") {
		t.Fatalf("escape did not cancel edit:\n%s", m.View())
	}
	key(tea.KeyEnter)
	text("-new")
	key(tea.KeyEnter)
	key(tea.KeyUp)
	key(tea.KeyBackspace)
	key(tea.KeyDown)
	key(tea.KeyEnter)
	key(tea.KeyLeft)
	if !strings.Contains(m.View(), "* cache:/cache-new") || strings.Contains(m.View(), "data:/data") {
		t.Fatalf("return did not restore edited list:\n%s", m.View())
	}
	key(tea.KeyEnter)
	key(tea.KeyEnter)
	view := m.View()
	if !strings.Contains(view, "* cache:/cache-new") {
		t.Fatalf("review missing list bullet:\n%s", view)
	}
}

func TestWizardListScrollKeepsSelectionAndInputVisible(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	resize(t, m, 40, 24)
	for i := 0; i < 20; i++ {
		feed(t, m, []tea.Msg{
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fmt.Sprintf("item-%02d", i))},
			tea.KeyMsg{Type: tea.KeyEnter},
		})
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyUp}})
	if view := m.View(); !strings.Contains(view, "> * item-19") || !strings.Contains(view, "_") {
		t.Fatalf("last selected item or input is hidden:\n%s", view)
	}
	for i := 0; i < 19; i++ {
		feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyUp}})
	}
	if view := m.View(); !strings.Contains(view, "> * item-00") || !strings.Contains(view, "_") {
		t.Fatalf("first selected item or input is hidden:\n%s", view)
	}
}

func TestWizardLongListKeepsSelectionVisible(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	resize(t, m, 40, 24)
	key := func(k tea.KeyType) { feed(t, m, []tea.Msg{tea.KeyMsg{Type: k}}) }
	text := func(s string) { feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}}) }
	text("LONGSTART" + strings.Repeat("x", 800))
	key(tea.KeyEnter)
	for i := 0; i < 12; i++ {
		text(fmt.Sprintf("item-%02d", i))
		key(tea.KeyEnter)
	}
	key(tea.KeyUp)
	if !strings.Contains(m.View(), "> * item-11") || !strings.Contains(m.View(), "_") {
		t.Fatalf("last item or input missing:\n%s", m.View())
	}
	for i := 0; i < 12; i++ {
		key(tea.KeyUp)
	}
	if !strings.Contains(m.View(), "> * LONGSTART") || !strings.Contains(m.View(), "_") {
		t.Fatalf("long selected item or input missing:\n%s", m.View())
	}
	key(tea.KeyDown)
	if !strings.Contains(m.View(), "> * item-00") {
		t.Fatalf("cannot move past long item:\n%s", m.View())
	}
}

func TestWizardOptionalListCanBeEmpty(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(m.View(), msgWizardAnswerEmpty) {
		t.Fatalf("empty optional list did not reach review:\n%s", m.View())
	}
}

func TestWizardListOversizedItemRemainsSelectable(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	resize(t, m, 40, 24)
	feed(t, m, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("longvalue", 90))},
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.KeyMsg{Type: tea.KeyUp},
	})
	if view := m.View(); !strings.Contains(view, "> * longvalue") {
		t.Fatalf("selected oversized item is not visible:\n%s", view)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if view := m.View(); !strings.Contains(view, "수정 취소") {
		t.Fatalf("oversized item cannot be edited:\n%s", view)
	}
}

func TestWizardListLongEditShowsInputEnd(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	resize(t, m, 40, 24)
	feed(t, m, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("a", 800) + "ENDMARK")},
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.KeyMsg{Type: tea.KeyUp},
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("EDITMARK")},
	})
	if view := m.View(); !strings.Contains(view, "ENDMARKEDITMARK_") {
		t.Fatalf("editing input end is hidden:\n%s", view)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyBackspace}})
	if view := m.View(); !strings.Contains(view, "ENDMARKEDITMAR_") {
		t.Fatalf("edited input end after backspace is hidden:\n%s", view)
	}
}

func TestWizardListOptionalEmpty(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true, Optional: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if view := m.View(); !strings.Contains(view, "Items:") {
		t.Fatalf("empty optional list did not advance to review:\n%s", view)
	}
}

// `d` deletes on every list screen, the same key the backup source checkboxes
// use. While a value is being typed it is a character instead.
func TestWizardListDeletesWithD(t *testing.T) {
	f := &wizardStub{desc: descriptor("wizard", false), steps: core.WizardSpec{Steps: []core.WizardStep{
		{Name: "items", Title: "Items", List: true, AllowCustom: true},
	}}}
	f.desc.Wizard = true
	m := newWizardModel(t, f)
	openWizard(t, m)
	feed(t, m, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")},
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")},
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.KeyMsg{Type: tea.KeyUp},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}},
	})
	if view := m.View(); strings.Contains(view, "second") || !strings.Contains(view, "first") {
		t.Fatalf("d did not delete the selected item:\n%s", view)
	}
	// The cursor sits on the add row, so `d` is a character there. Up wraps
	// from the add row to the last item and down wraps back, so one Up lands
	// on the item and Enter edits it.
	feed(t, m, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyUp},
		tea.KeyMsg{Type: tea.KeyEnter},
	})
	if view := m.View(); !strings.Contains(view, msgListEditing) {
		t.Fatalf("enter did not start editing:\n%s", view)
	}
	// While editing, `d` is typed and does not delete.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}})
	if view := m.View(); !strings.Contains(view, "firstd") {
		t.Fatalf("d did not type while editing:\n%s", view)
	}
}
