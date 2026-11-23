package tui

import (
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
