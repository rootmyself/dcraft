package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// A guided flow states the feature's own warning before its first question, so a
// reader learns the folder is not a blank slate before answering anything.
func TestWizardShowsTheFeatureNotice(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태"}
	w := &stubWizard{
		stubFeature: &stubFeature{desc: core.Descriptor{
			Name: "init", Title: "최초 구성", Summary: "구성.", Usage: "dcraft init",
			Section: core.SectionConfig, Wizard: true,
		}},
		steps: core.WizardSpec{
			Title:  "최초 Compose 설정",
			Notice: "이미 Compose 설정이 있음: compose.yaml. 최초 구성은 기존 파일을 덮어쓰지 않음.",
			Steps: []core.WizardStep{
				{Name: "from", Title: "구성 방식", Prompt: "고르", Options: []core.WizardOption{{Value: "image", Label: "이미지"}}},
			},
		},
	}
	m := newTestModel(t, status, w)
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if !strings.Contains(m.View(), "이미 Compose 설정이 있음") {
		t.Fatalf("the wizard does not state its notice:\n%s", m.View())
	}
}
