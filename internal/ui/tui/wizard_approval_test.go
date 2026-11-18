package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// A wizard that saves a file needs an independent approval. The preview screen
// is the confirmation: it must show the approval kind and apply it through the
// same engine path. This is generic wizard behavior, not a feature branch.
func TestWizardWithApprovalShowsTheKindAndSavesAfterPreviewConfirmation(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{
			Name: "generate-git-hook", Title: "Git 스크립트 생성", Summary: "요약", Usage: "dcraft generate-git-hook",
			Wizard: true, Approval: core.ApprovalFileOverwrite,
		},
		steps: core.WizardSpec{
			Title: "스크립트 생성 설정",
			Steps: []core.WizardStep{{
				Name: "stage", Title: "실행 시점", Prompt: "고르세요",
				Options: []core.WizardOption{{Value: "pre", Label: "사전"}, {Value: "post", Label: "사후"}},
			}},
		},
		preview: "스크립트를 저장합니다: scripts/git-sync.sh",
	}
	m := newWizardModel(t, wizard)
	openWizard(t, m)

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview; view:\n%s", m.phase, m.View())
	}
	view := m.View()
	for _, want := range []string{"파일 덮어쓰기", "file_overwrite", "스크립트를 저장합니다"} {
		if !strings.Contains(view, want) {
			t.Fatalf("preview missing %q:\n%s", want, view)
		}
	}
	if wizard.applied {
		t.Fatal("wizard applied before confirmation")
	}

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !wizard.applied {
		t.Fatalf("wizard did not apply after confirmation; result=%+v", m.result)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v", m.result)
	}
}
