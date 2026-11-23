package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// secretWizard is the fixture most screen tests share: five steps with a
// defaulted choice, a multi-free text step, and a feature-provided preview.
func secretWizard() (*wizardStub, core.WizardSpec) {
	spec := wizardSteps()
	stub := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Section: core.SectionConfig, Wizard: true,
		},
		steps:   spec,
		preview: "저장할 비밀값 연결\n서비스: worker\n효과: dcraft up 시 파일로 전달.",
	}
	return stub, spec
}

// Every step states how far the flow has come, which question is being asked,
// and what the user must answer.
func TestWizardStepShowsProgressTitleAndPrompt(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	openWizard(t, m)
	resize(t, m, 120, 36)

	view := m.View()
	for _, want := range []string{"단계 1/5", "서비스 선택", "서비스 선택."} {
		if !strings.Contains(view, want) {
			t.Fatalf("step screen missing %q:\n%s", want, view)
		}
	}

	// The ordinal counts visible steps, so the second question reads 2/5.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	view = m.View()
	for _, want := range []string{"단계 2/5", "시점 선택", "시점 선택."} {
		if !strings.Contains(view, want) {
			t.Fatalf("second step screen missing %q:\n%s", want, view)
		}
	}
}

// A conditional step is skipped when its answer hides it, the denominator
// follows the visible steps, and going back to change the answer restores it
// with the earlier value.
func TestWizardConditionalStepHidesAndIsRestored(t *testing.T) {
	spec := core.WizardSpec{
		Title: "조건 단계",
		Steps: []core.WizardStep{
			{
				Name: "source", Title: "원본 종류", Prompt: "원본 선택.", Default: "env",
				Options: []core.WizardOption{{Value: "env", Label: "환경변수"}, {Value: "aws", Label: "AWS"}},
			},
			{
				Name: "field", Title: "AWS field", Prompt: "field를 입력.", AllowCustom: true,
				DependsOn: "source", DependsValue: "aws",
			},
			{Name: "ref", Title: "참조", Prompt: "참조 입력.", AllowCustom: true},
		},
	}
	desc := descriptor("secrets", false)
	desc.Wizard = true
	stub := &wizardStub{desc: desc, steps: spec, preview: "OK"}
	m := newWizardModel(t, stub)
	openWizard(t, m)
	resize(t, m, 80, 24)

	// Choose AWS: the field step appears and the denominator is 3.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(m.View(), "AWS field") || !strings.Contains(m.View(), "단계 2/3") {
		t.Fatalf("AWS step or denominator missing:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("token")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("prod/db")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview", m.phase)
	}

	// Back to the source step and choose env: the field step disappears and
	// the flow is two steps long. Going back over a typed step clears it first
	// (existing wizard navigation), so the arrows are not the only keys used.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyLeft}}) // preview -> ref
	for range "prod/db" {
		feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyBackspace}})
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyLeft}}) // ref -> field
	for range "token" {
		feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyBackspace}})
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyLeft}}) // field -> source
	if m.phase != phaseWizardStep || m.wizardSpec.Steps[m.wizardStep].Name != "source" {
		t.Fatalf("did not reach the source step: phase=%v step=%d", m.phase, m.wizardStep)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyUp}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep || m.wizardSpec.Steps[m.wizardStep].Name != "ref" {
		t.Fatalf("hidden step was not skipped: phase=%v step=%d", m.phase, m.wizardStep)
	}
	if !strings.Contains(m.View(), "단계 2/2") {
		t.Fatalf("visible-step denominator is wrong:\n%s", m.View())
	}

	// Back again and answer AWS: the field step returns with its earlier value.
	// `left` never abandons a typed value, so the ref answer is cleared first.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyLeft}})
	for range "prod/db" {
		feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyBackspace}})
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyLeft}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(m.View(), "AWS field") {
		t.Fatalf("conditional step was not restored:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "token_") {
		t.Fatalf("restored step lost its earlier answer:\n%s", m.View())
	}
}

// A single-choice step renders each option as a card and moves the cursor
// between cards without changing the answer.
func TestWizardChoiceStepRendersSelectableCards(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 80, 24)

	lines := viewLines(t, m)
	cards := 0
	for _, line := range lines {
		if strings.Contains(line, m.theme.Glyphs.TL) {
			cards++
		}
	}
	if cards != 2 {
		t.Fatalf("choice step drew %d cards, want 2:\n%s", cards, strings.Join(lines, "\n"))
	}
	if !strings.Contains(m.View(), "> web") {
		t.Fatalf("first card is not the cursor card:\n%s", m.View())
	}
	if strings.Contains(m.View(), "> worker") {
		t.Fatalf("two cards claim the cursor:\n%s", m.View())
	}

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	view := m.View()
	if !strings.Contains(view, "> worker") || strings.Contains(view, "> web") {
		t.Fatalf("cursor did not move to the second card:\n%s", view)
	}
}

// The multi step states each value's checked state explicitly and space
// toggles only the highlighted box.
func TestWizardMultiStepUsesExplicitCheckboxes(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{Name: "backup", Title: "백업", Summary: "백업.", Usage: "dcraft backup", Section: core.SectionData, Wizard: true},
		steps: core.WizardSpec{
			Title: "백업 설정",
			Steps: []core.WizardStep{
				{
					Name: "sources", Title: "데이터 선택", Prompt: "여러 항목을 고르.", Multi: true,
					Options: []core.WizardOption{
						{Value: "volume:data", Label: "볼륨 data"},
						{Value: "bind:/srv/app", Label: "폴더 /srv/app"},
					},
				},
				{Name: "ref", Title: "폴더", Prompt: "입력", AllowCustom: true, Default: "./out"},
			},
		},
		preview: "백업 계획",
	}
	m := newWizardModel(t, wizard)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 80, 24)

	view := m.View()
	for _, want := range []string{"> [ ] 볼륨 data", "[ ] 폴더 /srv/app"} {
		if !strings.Contains(view, want) {
			t.Fatalf("unchecked boxes missing %q:\n%s", want, view)
		}
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeySpace}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	view = m.View()
	for _, want := range []string{"[x] 볼륨 data", "> [ ] 폴더 /srv/app"} {
		if !strings.Contains(view, want) {
			t.Fatalf("checkbox state missing %q:\n%s", want, view)
		}
	}
	if m.phase != phaseWizardStep {
		t.Fatalf("space advanced the step: %v", m.phase)
	}
}

// An empty required answer is refused inline, next to the field, and typing
// clears the guidance without moving the flow.
func TestWizardRequiredInputBlocksEmptyWithInlineValidation(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 80, 24)
	// Four defaults reach the free-text ref step.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !m.wizardTextInput() {
		t.Fatalf("not on the free-text step: phase=%v", m.phase)
	}

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep {
		t.Fatalf("empty required answer advanced to %v", m.phase)
	}
	lines := viewLines(t, m)
	inputRow := -1
	for i, line := range lines {
		if strings.Contains(line, msgWizardInputLabel) {
			inputRow = i
			break
		}
	}
	if inputRow < 0 {
		t.Fatalf("input row not found:\n%s", strings.Join(lines, "\n"))
	}
	if inputRow+1 >= len(lines) || !strings.Contains(lines[inputRow+1], msgWizardNeedValue) {
		t.Fatalf("validation is not inline under the field:\n%s", strings.Join(lines, "\n"))
	}

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("token")}})
	if strings.Contains(m.View(), msgWizardNeedValue) {
		t.Fatalf("typing did not clear the validation:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("valid answer did not advance: phase=%v", m.phase)
	}
}

// The cursor stays visible at the end of the typed value, including after
// deleting a rune.
func TestWizardTypedInputShowsAVisibleCursor(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !m.wizardTextInput() {
		t.Fatalf("not on the free-text step: phase=%v", m.phase)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")}})
	if !strings.Contains(m.View(), "abc_") {
		t.Fatalf("visible cursor missing after typing:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyBackspace}})
	if !strings.Contains(m.View(), "ab_") {
		t.Fatalf("visible cursor missing after backspace:\n%s", m.View())
	}
}

// The preview is a styled panel that states the exact answers the apply will
// send, alongside the feature's own effect description.
func TestWizardPreviewPanelShowsTheExactAppliedValues(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 120, 36)
	answerWizard(t, m, true)

	lines := viewLines(t, m)
	panels := 0
	for _, line := range lines {
		if strings.Contains(line, m.theme.Glyphs.TL+m.theme.Glyphs.H+" "+msgWizardAppliedTitle) {
			panels++
		}
	}
	if panels != 1 {
		t.Fatalf("preview is not one styled panel (%d):\n%s", panels, strings.Join(lines, "\n"))
	}
	view := m.View()
	for _, want := range []string{
		"적용 내용", "저장할 비밀값 연결",
		"서비스 선택: worker", "시점 선택: 실행 시점 (runtime)",
		"전달 방식: 파일 (file)", "원본 종류: 로컬 파일 (file)", "원본 참조: secrets/token.txt",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("preview missing %q:\n%s", want, view)
		}
	}
}

// An approval-gated apply lands on the shared confirmation modal at 48x20:
// the same panel the rest of the TUI uses, not a second approval screen.
func TestWizardApprovalApplyUsesTheSharedModalAt48Columns(t *testing.T) {
	stub := &wizardStub{
		desc: core.Descriptor{
			Name: "restore", Title: "데이터 복원", Summary: "요약", Usage: "dcraft restore",
			Section: core.SectionData, Wizard: true, Approval: core.ApprovalDataRestore,
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
	wizard := &approvalWizardStub{wizardStub: stub, planText: "복원 계획\n영향 서비스: app"}
	m := newWizardModel(t, wizard)
	m.theme = *plainTheme()
	openWizard(t, m)
	resize(t, m, 48, 20)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))

	if m.phase != phaseConfirm || !m.confirmOpen() {
		t.Fatalf("phase = %v open=%v, want the shared confirmation", m.phase, m.confirmOpen())
	}
	if stub.applied {
		t.Fatal("wizard applied before the approval")
	}
	lines := viewLines(t, m)
	if top, bottom := modalBorders(m.theme, lines, 48); top != 1 || bottom != 1 {
		t.Fatalf("approval is not the shared modal:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(m.View(), msgApprovalConfirmTitle) {
		t.Fatalf("modal title missing:\n%s", m.View())
	}
	// The approval kinds are in the panel's scrolling band, under the approved
	// section; the panel scrolls, it never drops a field.
	if !strings.Contains(m.View(), msgApprovalsSection) {
		feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnd}})
		if !strings.Contains(m.View(), msgApprovalsSection) || !strings.Contains(m.View(), "data_restore") {
			t.Fatalf("approval kinds not reachable in the modal:\n%s", m.View())
		}
	}
}

// esc leaves the flow at any step without saving or running, and keeps the
// existing cancellation wording.
func TestWizardEscCancelRunsNothingAndKeepsTheWording(t *testing.T) {
	stub, _ := secretWizard()
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	openWizard(t, m)
	before := len(stub.runs)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}})
	if stub.applied || len(stub.runs) != before {
		t.Fatalf("canceling at a step ran the feature: applied=%v runs=%v", stub.applied, stub.runs)
	}
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready", m.phase)
	}
	if !strings.Contains(m.View(), msgWizardCanceled) {
		t.Fatalf("cancellation wording missing:\n%s", m.View())
	}

	// The same key on the preview saves nothing either.
	stub2, _ := secretWizard()
	m2 := newWizardModel(t, stub2)
	m2.theme = *plainTheme()
	openWizard(t, m2)
	answerWizard(t, m2, true)
	before = len(stub2.runs)
	feed(t, m2, []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}})
	if stub2.applied || len(stub2.runs) != before {
		t.Fatalf("canceling the preview ran the feature: applied=%v runs=%v", stub2.applied, stub2.runs)
	}
	if !strings.Contains(m2.View(), msgWizardCanceled) {
		t.Fatalf("preview cancellation wording missing:\n%s", m2.View())
	}
}

// At 48 columns the prompt and every option label wrap instead of being cut.
func TestWizardStepFits48ColumnsWithoutClipping(t *testing.T) {
	desc := descriptor("secrets", false)
	desc.Wizard = true
	stub := &wizardStub{
		desc: desc,
		steps: core.WizardSpec{Title: "좁은 화면 설정", Steps: []core.WizardStep{{
			Name: "service", Title: "서비스 선택", Prompt: "서비스 선택.",
			Options: []core.WizardOption{
				{Value: "web", Label: "첫 번째 선택지 아주 긴 라벨"},
				{Value: "worker", Label: "두 번째 선택지도 아주 긴 라벨"},
			},
		}}},
		preview: "OK",
	}
	m := newWizardModel(t, stub)
	m.theme = *plainTheme()
	resize(t, m, 48, 20)
	openWizard(t, m)
	view := m.View()
	if strings.Contains(view, m.theme.Glyphs.Ell) {
		t.Fatalf("48 columns clipped the step:\n%s", view)
	}
	for _, want := range []string{"서비스 선택.", "첫 번째 선택지", "긴 라벨", "두 번째 선택지도", "긴 라벨"} {
		if !strings.Contains(view, want) {
			t.Fatalf("48-column step missing %q:\n%s", want, view)
		}
	}
	for i, line := range viewLines(t, m) {
		if got := visual.Width(line); got > 48 {
			t.Fatalf("row %d is %d cells: %q", i, got, line)
		}
	}
}
