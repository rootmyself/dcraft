package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type extendedWizard struct {
	desc       core.Descriptor
	spec       core.WizardSpec
	preview    string
	previewErr error

	needs          core.ApprovalKind
	applied        bool
	appliedAnswers map[string]string
}

func (f *extendedWizard) Descriptor() core.Descriptor { return f.desc }

func (f *extendedWizard) Run(ctx context.Context, in core.Invocation) (any, error) {
	switch in.Option("wizard") {
	case "steps":
		return f.spec, nil
	case "apply":
		f.applied = true
		f.appliedAnswers = map[string]string{}
		for k, v := range in.Options {
			f.appliedAnswers[k] = v
		}
		return "saved", nil
	}
	return nil, nil
}

func (f *extendedWizard) WizardPreview(answers map[string]string) (string, error) {
	if f.previewErr != nil {
		return "", f.previewErr
	}
	return f.preview, nil
}

func (f *extendedWizard) RequiredApproval(ctx context.Context, in core.Invocation) (core.ApprovalKind, error) {
	return f.needs, nil
}

func (f *extendedWizard) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	return core.Plan{Text: f.preview}, nil
}

func (f *extendedWizard) Present(res core.Result, width int) string { return "EXTENDED-SAVED" }

func newExtendedWizardModel(t *testing.T, wizard *extendedWizard) *Model {
	t.Helper()
	status := &stubFeature{desc: descriptor("status", true), body: "상태 본문"}
	reg := core.NewRegistry()
	if err := reg.Register(status); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(wizard); err != nil {
		t.Fatal(err)
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op_tui" }
	return NewModel(context.Background(), Options{Engine: engine, Registry: reg})
}

func openExtendedWizard(t *testing.T, m *Model) {
	t.Helper()
	initModel(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep {
		t.Fatalf("phase = %v, want wizard step; view:\n%s", m.phase, m.View())
	}
}

func TestWizardSkipsStepsWhoseConditionIsNotMet(t *testing.T) {
	spec := core.WizardSpec{
		Title: "구성 방식",
		Steps: []core.WizardStep{
			{
				Name: "from", Title: "방식", Prompt: "방식을 선택하세요.",
				Default: "dockerfile",
				Options: []core.WizardOption{
					{Value: "dockerfile", Label: "Dockerfile"},
					{Value: "image", Label: "이미지"},
				},
			},
			{
				Name: "context", Title: "빌드 컨텍스트", Prompt: "컨텍스트",
				AllowCustom: true, Default: ".", DependsOn: &core.WizardCondition{Name: "from", Values: []string{"dockerfile"}},
			},
			{
				Name: "image", Title: "이미지 참조", Prompt: "이미지",
				AllowCustom: true, DependsOn: &core.WizardCondition{Name: "from", Values: []string{"image"}},
			},
			{
				Name: "note", Title: "메모", Prompt: "메모",
				AllowCustom: true, Optional: true,
			},
		},
	}
	wizard := &extendedWizard{
		desc: core.Descriptor{Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true},
		spec: spec, preview: "미리보기",
	}
	m := newExtendedWizardModel(t, wizard)
	openExtendedWizard(t, m)

	// Accept the default dockerfile mode. The image step must be skipped and
	// the optional note step reached directly.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(m.View(), "빌드 컨텍스트") {
		t.Fatalf("expected Dockerfile-specific step, view:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(m.View(), "메모") {
		t.Fatalf("expected the optional step after the context step, view:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview; view:\n%s", m.phase, m.View())
	}
	if _, ok := m.wizardAnswers["image"]; ok {
		t.Fatalf("skipped image step received an answer: %v", m.wizardAnswers)
	}

	wizard2 := &extendedWizard{
		desc: wizard.desc, spec: spec, preview: "미리보기",
	}
	imageModel := newExtendedWizardModel(t, wizard2)
	openExtendedWizard(t, imageModel)
	// Choose the image mode, then the image step must be shown instead of the
	// Dockerfile context step.
	feed(t, imageModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, imageModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !strings.Contains(imageModel.View(), "이미지 참조") {
		t.Fatalf("expected image step, view:\n%s", imageModel.View())
	}
	if _, ok := imageModel.wizardAnswers["context"]; ok {
		t.Fatalf("skipped context step received an answer: %v", imageModel.wizardAnswers)
	}
}

func TestWizardOptionalStepAcceptsEmptyAndRequiredStepDoesNot(t *testing.T) {
	spec := core.WizardSpec{
		Title: "입력",
		Steps: []core.WizardStep{
			{Name: "required", Title: "필수", Prompt: "필수 값", AllowCustom: true},
			{Name: "optional", Title: "선택", Prompt: "선택 값", AllowCustom: true, Optional: true},
		},
	}
	wizard := &extendedWizard{
		desc: core.Descriptor{Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true},
		spec: spec, preview: "미리보기",
	}
	m := newExtendedWizardModel(t, wizard)
	openExtendedWizard(t, m)

	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep {
		t.Fatalf("required empty input advanced to %v", m.phase)
	}
	if !strings.Contains(m.View(), "값을 입력한 뒤 enter") {
		t.Fatalf("required empty input did not explain, view:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("value")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep || !strings.Contains(m.View(), "선택 값") {
		t.Fatalf("did not reach the optional step: %v\n%s", m.phase, m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("optional empty input did not reach preview: %v\n%s", m.phase, m.View())
	}
	if value, ok := m.wizardAnswers["optional"]; !ok || value != "" {
		t.Fatalf("optional answer = %q (present=%v), want an explicit empty answer", value, ok)
	}
}

func TestWizardPreviewShowsAndPassesIndependentApprovals(t *testing.T) {
	wizard := &extendedWizard{
		desc: core.Descriptor{Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true},
		spec: core.WizardSpec{
			Title: "설정 저장",
			Steps: []core.WizardStep{
				{Name: "service", Title: "서비스", Prompt: "이름", AllowCustom: true},
			},
			RequiredApprovals: []core.ApprovalKind{core.ApprovalFileOverwrite},
		},
		preview: "기존 파일을 덮어씁니다.",
		needs:   core.ApprovalFileOverwrite,
	}
	m := newExtendedWizardModel(t, wizard)
	openExtendedWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("app")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview", m.phase)
	}
	for _, want := range []string{"기존 파일을 덮어씁니다.", "파일 덮어쓰기", "file_overwrite"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("preview missing %q:\n%s", want, m.View())
		}
	}

	// The wizard preview is not the approval. The apply is routed through the
	// generic plan + confirmation screen, exactly like any approval operation.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want the approval confirmation; view:\n%s", m.phase, m.View())
	}
	for _, want := range []string{"승인 확인", "파일 덮어쓰기", "file_overwrite"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("confirmation missing %q:\n%s", want, m.View())
		}
	}
	if wizard.applied {
		t.Fatal("wizard applied before the independent approval confirmation")
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !wizard.applied {
		t.Fatal("wizard did not apply after the approval")
	}
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		t.Fatalf("result = %+v, want success with the collected approval", m.result)
	}
	if !strings.Contains(m.View(), "EXTENDED-SAVED") {
		t.Fatalf("saved screen missing:\n%s", m.View())
	}
}

func TestWizardPreviewErrorShowsTheDetailedKoreanGuidance(t *testing.T) {
	wizard := &extendedWizard{
		desc: core.Descriptor{Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true},
		spec: core.WizardSpec{
			Title: "설정",
			Steps: []core.WizardStep{
				{Name: "service", Title: "서비스", Prompt: "이름", AllowCustom: true},
			},
		},
		previewErr: core.Fail(core.Diagnostic{
			Category: core.CategoryInitInputInvalid,
			Severity: core.SeverityError,
			Message:  "생성 내용을 확인할 수 없습니다.",
			Detail:   "Dockerfile: /project/Dockerfile을 찾지 못했습니다.",
		}),
	}
	m := newExtendedWizardModel(t, wizard)
	openExtendedWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("app")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview", m.phase)
	}
	for _, want := range []string{"생성 내용을 확인할 수 없습니다.", "Dockerfile: /project/Dockerfile을 찾지 못했습니다."} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("preview error missing %q:\n%s", want, m.View())
		}
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if wizard.applied {
		t.Fatal("invalid preview applied the answers")
	}
}

func TestWizardCancelDuringApplyKeepsTheCanceledResultVisible(t *testing.T) {
	wizard := &extendedWizard{
		desc: core.Descriptor{Name: "init", Title: "최초 구성", Summary: "설정", Usage: "dcraft init", Wizard: true},
		spec: core.WizardSpec{
			Title: "설정",
			Steps: []core.WizardStep{
				{Name: "service", Title: "서비스", Prompt: "이름", AllowCustom: true},
			},
		},
		preview: "저장 미리보기",
	}
	m := newExtendedWizardModel(t, wizard)
	openExtendedWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("app")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview", m.phase)
	}
	// Start the apply but deliberately do not execute its command; then ask to
	// cancel the in-flight operation.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.phase != phaseLoading || !m.wizardApplying {
		t.Fatalf("apply did not start: phase=%v applying=%v", m.phase, m.wizardApplying)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.notice != msgCanceling {
		t.Fatalf("canceling notice = %q", m.notice)
	}
	if m.wizardCanceled {
		t.Fatal("apply cancellation was marked as an irrelevant steps cancellation")
	}
	canceled := core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        "init",
		Outcome:       core.OutcomeCanceled,
		Stages:        []core.Stage{},
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryCanceled, Severity: core.SeverityWarning,
			Message: "저장 단계가 시작된 뒤라면 파일이 이미 바뀌었을 수 있습니다.",
		}},
	}
	canceled.Target.Normalize()
	m.Update(resultMsg{res: canceled})
	if m.result == nil || m.result.Outcome != core.OutcomeCanceled {
		t.Fatalf("result = %+v, want the canceled result", m.result)
	}
	if !strings.Contains(m.View(), "저장 단계가 시작된 뒤라면") {
		t.Fatalf("canceled result was hidden:\n%s", m.View())
	}
}
