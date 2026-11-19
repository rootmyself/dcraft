package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

type wizardStub struct {
	desc    core.Descriptor
	steps   core.WizardSpec
	preview string
	err     error

	runs    []map[string]string
	applied bool
}

func (f *wizardStub) Descriptor() core.Descriptor { return f.desc }

func (f *wizardStub) Run(ctx context.Context, in core.Invocation) (any, error) {
	options := map[string]string{}
	for k, v := range in.Options {
		options[k] = v
	}
	f.runs = append(f.runs, options)
	switch in.Option("wizard") {
	case "steps":
		return f.steps, nil
	case "apply":
		f.applied = true
		return "saved", nil
	}
	return nil, nil
}

func (f *wizardStub) WizardPreview(answers map[string]string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.preview, nil
}

func (f *wizardStub) Present(res core.Result, width int) string { return "SAVED-SCREEN" }

func newWizardModel(t *testing.T, wizard core.Feature) *Model {
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

func wizardSteps() core.WizardSpec {
	return core.WizardSpec{
		Title: "비밀값 연결 설정",
		Steps: []core.WizardStep{
			{
				Name: "service", Title: "서비스 선택", Prompt: "서비스를 선택하세요.",
				Options: []core.WizardOption{{Value: "web", Label: "web"}, {Value: "worker", Label: "worker"}},
			},
			{
				Name: "phase", Title: "시점 선택", Prompt: "시점을 선택하세요.",
				Default: "runtime",
				Options: []core.WizardOption{{Value: "runtime", Label: "실행 시점 (runtime)"}},
			},
			{
				Name: "delivery", Title: "전달 방식", Prompt: "전달 방식을 선택하세요.",
				Default: "environment",
				Options: []core.WizardOption{{Value: "environment", Label: "환경변수"}, {Value: "file", Label: "파일"}},
			},
			{
				Name: "source", Title: "원본 종류", Prompt: "원본 종류를 선택하세요.",
				Default: "env",
				Options: []core.WizardOption{{Value: "env", Label: "환경변수"}, {Value: "file", Label: "로컬 파일"}},
			},
			{
				Name: "ref", Title: "원본 참조", Prompt: "원본 참조를 입력하세요.",
				AllowCustom: true, CustomHint: "환경변수 이름 또는 파일 경로",
			},
		},
	}
}

func openWizard(t *testing.T, m *Model) {
	t.Helper()
	initModel(t, m)
	if m.phase != phaseReady {
		t.Fatalf("initial phase = %v, want ready", m.phase)
	}
	// The menu starts on status; move to the wizard feature.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep {
		t.Fatalf("phase = %v, want wizard step; view:\n%s", m.phase, m.View())
	}
}

func answerWizard(t *testing.T, m *Model, toPreview bool) {
	t.Helper()
	// service: choose worker (down then enter)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	// phase: runtime default
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	// delivery: choose file (down then enter)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	// source: choose file (down then enter)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	// ref: type and enter
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("secrets/token.txt")}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if toPreview && m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview; view:\n%s", m.phase, m.View())
	}
}

func TestWizardSkipsConditionalStepsAndAllowsOptionalEmptyAnswer(t *testing.T) {
	newSpec := func() core.WizardSpec {
		return core.WizardSpec{
			Title: "조건 단계",
			Steps: []core.WizardStep{
				{
					Name: "source", Title: "원본 종류", Prompt: "원본을 선택하세요.", Default: "env",
					Options: []core.WizardOption{{Value: "env", Label: "환경변수"}, {Value: "aws", Label: "AWS"}},
				},
				{Name: "ref", Title: "참조", Prompt: "참조를 입력하세요.", AllowCustom: true},
				{
					Name: "field", Title: "field", Prompt: "field를 입력하세요.", AllowCustom: true,
					Optional: true, DependsOn: "source", DependsValue: "aws",
				},
			},
		}
	}

	secretDesc := descriptor("secrets", false)
	secretDesc.Wizard = true

	// The env answer hides the AWS-only step: ref goes straight to preview.
	envWizard := &wizardStub{desc: secretDesc, steps: newSpec(), preview: "OK"}
	envModel := newWizardModel(t, envWizard)
	openWizard(t, envModel)
	feed(t, envModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}}) // source: env
	feed(t, envModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("TOKEN")}})
	feed(t, envModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if envModel.phase != phaseWizardPreview {
		t.Fatalf("env flow phase = %v, want preview; view:\n%s", envModel.phase, envModel.View())
	}
	if _, answered := envModel.wizardAnswers["field"]; answered {
		t.Fatalf("hidden AWS step was answered: %+v", envModel.wizardAnswers)
	}

	// The AWS answer shows the optional step; an empty Enter is accepted.
	awsWizard := &wizardStub{desc: secretDesc, steps: newSpec(), preview: "OK"}
	awsModel := newWizardModel(t, awsWizard)
	openWizard(t, awsModel)
	feed(t, awsModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, awsModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}}) // source: aws
	feed(t, awsModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("prod/db")}})
	feed(t, awsModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if awsModel.phase != phaseWizardStep || !strings.Contains(awsModel.View(), "field") {
		t.Fatalf("AWS flow did not show the dependent step; phase=%v view:\n%s", awsModel.phase, awsModel.View())
	}
	feed(t, awsModel, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}}) // empty optional answer
	if awsModel.phase != phaseWizardPreview {
		t.Fatalf("AWS flow phase = %v, want preview; view:\n%s", awsModel.phase, awsModel.View())
	}
	if value, ok := awsModel.wizardAnswers["field"]; !ok || value != "" {
		t.Fatalf("optional empty answer = %q (present=%v)", value, ok)
	}
}

func TestWizardShowsEffectBeforeSaveAndDeclineSavesNothing(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Wizard: true,
		},
		steps:   wizardSteps(),
		preview: "저장할 비밀값 연결\n서비스: worker\n전달: 파일 /run/secrets/token\n효과: dcraft up 시 파일로 전달합니다.",
	}
	m := newWizardModel(t, wizard)
	openWizard(t, m)
	for _, want := range []string{"비밀값 연결 설정", "서비스 선택", "단계 1/5", "worker"} {
		if !stringsContains(m.View(), want) {
			t.Fatalf("wizard view missing %q:\n%s", want, m.View())
		}
	}
	answerWizard(t, m, true)
	view := m.View()
	for _, want := range []string{"저장할 비밀값 연결", "worker", "/run/secrets/token", "enter 저장"} {
		if !stringsContains(view, want) {
			t.Fatalf("preview missing %q:\n%s", want, view)
		}
	}
	if wizard.applied {
		t.Fatal("wizard applied before confirmation")
	}
	// esc declines the save and returns to the previous screen.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}})
	if wizard.applied {
		t.Fatal("declining the wizard saved a reference")
	}
	if m.phase != phaseReady {
		t.Fatalf("phase after decline = %v, want ready", m.phase)
	}
	if !stringsContains(m.View(), "저장된 내용은 없습니다") {
		t.Fatalf("decline notice missing:\n%s", m.View())
	}
}

func TestWizardSavesThroughTheSameFeaturePathAfterConfirmation(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Wizard: true,
		},
		steps:   wizardSteps(),
		preview: "저장할 비밀값 연결\n효과: 파일 /run/secrets/token",
	}
	m := newWizardModel(t, wizard)
	openWizard(t, m)
	answerWizard(t, m, true)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !wizard.applied {
		t.Fatal("wizard did not apply after confirmation")
	}
	last := wizard.runs[len(wizard.runs)-1]
	if last["wizard"] != "apply" || last["service"] != "worker" ||
		last["delivery"] != "file" || last["source"] != "file" || last["ref"] != "secrets/token.txt" {
		t.Fatalf("apply options = %v", last)
	}
	if !stringsContains(m.View(), "SAVED-SCREEN") {
		t.Fatalf("saved screen missing:\n%s", m.View())
	}
}

func TestWizardTypingQDoesNotQuitAndInvalidPreviewBlocksSave(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Wizard: true,
		},
		steps:   wizardSteps(),
		preview: "preview",
		err:     errors.New("invalid reference"),
	}
	m := newWizardModel(t, wizard)
	openWizard(t, m)
	// move to the ref step quickly
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep || !m.wizardTextInput() {
		t.Fatalf("not on the input step: %v", m.phase)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}})
	if m.phase != phaseWizardStep || m.wizardInput != "q" {
		t.Fatalf("typing q left phase=%v input=%q", m.phase, m.wizardInput)
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview", m.phase)
	}
	if m.wizardPreviewErr == nil {
		t.Fatal("preview error was not surfaced")
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if wizard.applied {
		t.Fatal("invalid preview still saved")
	}
}

func TestWizardSpecFailureShowsFailureInsteadOfSaving(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{
			Name: "secrets", Title: "비밀값 연결", Summary: "요약", Usage: "dcraft secrets",
			Wizard: true,
		},
	}
	// A nil spec cannot be produced by the stub; use a feature that fails.
	failing := &failingWizard{desc: wizard.desc}
	reg := core.NewRegistry()
	status := &stubFeature{desc: descriptor("status", true), body: "상태"}
	if err := reg.Register(status); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(failing); err != nil {
		t.Fatal(err)
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return "/cwd", nil }
	engine.NewOperationID = func() string { return "op" }
	m := NewModel(context.Background(), Options{Engine: engine, Registry: reg})
	initModel(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseReady {
		t.Fatalf("phase = %v, want ready failure screen", m.phase)
	}
	if !stringsContains(m.View(), "작업 실패") {
		t.Fatalf("failure screen missing:\n%s", m.View())
	}
}

type failingWizard struct{ desc core.Descriptor }

func (f *failingWizard) Descriptor() core.Descriptor { return f.desc }
func (f *failingWizard) Run(ctx context.Context, in core.Invocation) (any, error) {
	return nil, core.Fail(core.Diagnostic{
		Category:    core.CategoryServiceUnknown,
		Severity:    core.SeverityError,
		Message:     "서비스를 확인할 수 없습니다.",
		NextActions: []string{"Compose 설정을 확인하세요."},
	})
}
func (f *failingWizard) WizardPreview(answers map[string]string) (string, error) { return "", nil }

func stringsContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func TestWizardMultiStepCollectsAMixedSelection(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{Name: "backup", Title: "백업", Summary: "백업합니다.", Usage: "dcraft backup", Wizard: true},
		steps: core.WizardSpec{
			Title: "백업 설정",
			Steps: []core.WizardStep{
				{
					Name: "sources", Title: "데이터 선택", Prompt: "여러 항목을 고르세요.", Multi: true,
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
	openWizard(t, m)

	// Space toggles the highlighted value; down moves; enter confirms the mix.
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeySpace}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyDown}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeySpace}})
	if !strings.Contains(m.View(), "[x] 볼륨 data") || !strings.Contains(m.View(), "[x] 폴더 /srv/app") {
		t.Fatalf("checked values not rendered:\n%s", m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardPreview {
		t.Fatalf("phase = %v, want preview:\n%s", m.phase, m.View())
	}
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if !wizard.applied {
		t.Fatal("wizard did not apply")
	}
	if got := wizard.runs[len(wizard.runs)-1]["sources"]; got != "volume:data\nbind:/srv/app" {
		t.Fatalf("sources answer = %q", got)
	}
}

func TestWizardMultiEnterWithoutTogglesSelectsTheHighlightedValue(t *testing.T) {
	wizard := &wizardStub{
		desc: core.Descriptor{Name: "backup", Title: "백업", Summary: "백업합니다.", Usage: "dcraft backup", Wizard: true},
		steps: core.WizardSpec{Steps: []core.WizardStep{
			{
				Name: "sources", Title: "데이터 선택", Prompt: "고르세요.", Multi: true,
				Options: []core.WizardOption{{Value: "volume:data", Label: "볼륨 data"}},
			},
			{Name: "ref", Title: "출력", Prompt: "입력", AllowCustom: true, Default: "./out"},
		}},
		preview: "미리보기",
	}
	m := newWizardModel(t, wizard)
	openWizard(t, m)
	feed(t, m, []tea.Msg{tea.KeyMsg{Type: tea.KeyEnter}})
	if m.phase != phaseWizardStep || m.wizardStep != 1 {
		t.Fatalf("enter did not preserve the single-choice flow: phase=%v step=%d", m.phase, m.wizardStep)
	}
	if got := m.wizardAnswers["sources"]; got != "volume:data" {
		t.Fatalf("sources answer = %q", got)
	}
}
