package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type approvalWizardStub struct {
	*wizardStub
	plans    []map[string]string
	planText string
}

func (f *approvalWizardStub) Plan(_ context.Context, in core.Invocation) (core.Plan, error) {
	answers := map[string]string{}
	for k, v := range in.Options {
		answers[k] = v
	}
	f.plans = append(f.plans, answers)
	return core.Plan{Text: f.planText}, nil
}

func TestApprovalWizardUsesTheGenericPlanAndDataRestoreApproval(t *testing.T) {
	stub := &wizardStub{
		desc: core.Descriptor{
			Name: "restore", Title: "데이터 복원", Summary: "요약", Usage: "dcraft restore",
			Section: core.SectionData,
			Wizard:  true, Approval: core.ApprovalDataRestore,
		},
		steps: core.WizardSpec{
			Title: "데이터 복원 설정",
			Steps: []core.WizardStep{{
				Name: "volume", Title: "복원 대상 볼륨 선택", Prompt: "볼륨",
				Options: []core.WizardOption{{Value: "data", Label: "data → proj_data"}},
			}},
		},
		preview: "복원 계획\n대상: data → proj_data",
	}
	wizard := &approvalWizardStub{wizardStub: stub, planText: "복원 계획\n대상: data → proj_data\n영향 서비스: app"}
	m := newWizardModel(t, wizard)
	openWizard(t, m)
	// One choice step, then the effect preview.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want wizard preview; view:\n%s", m.phase, m.View())
	}
	// The preview names the approval the feature will need, but it is not
	// itself an approval: enter plans, it does not apply.
	if view := m.View(); !strings.Contains(view, "승인:") || !strings.Contains(view, "data_restore") {
		t.Fatalf("preview does not show the declared approval:\n%s", view)
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if stub.applied {
		t.Fatal("wizard applied without the data_restore approval")
	}
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want generic confirmation; view:\n%s", m.phase, m.View())
	}
	if m.pendingApproval != core.ApprovalDataRestore {
		t.Fatalf("pending approval = %q", m.pendingApproval)
	}
	if len(wizard.plans) != 1 || wizard.plans[0]["volume"] != "data" {
		t.Fatalf("plan did not receive the wizard answers: %+v", wizard.plans)
	}
	if !strings.Contains(m.View(), "데이터 복원") || !strings.Contains(m.View(), "영향 서비스: app") {
		t.Fatalf("confirmation does not show the plan:\n%s", m.View())
	}
	// Accepting the independent approval runs the same feature path with
	// wizard=apply and the collected answers.
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if !stub.applied {
		t.Fatal("approved wizard did not apply")
	}
	last := stub.runs[len(stub.runs)-1]
	if last["wizard"] != "apply" || last["volume"] != "data" {
		t.Fatalf("apply options = %v", last)
	}
}
