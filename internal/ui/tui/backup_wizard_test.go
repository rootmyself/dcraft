package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// stubWizard is a feature with the same generic capabilities backup uses: a
// guided wizard, a preview, and one engine Run path for both CLI and TUI.
type stubWizard struct {
	*stubFeature
	steps    core.WizardSpec
	preview  string
	applyRun int
	answers  map[string]string
}

func (f *stubWizard) Run(ctx context.Context, in core.Invocation) (any, error) {
	switch in.Option("wizard") {
	case "steps":
		return f.steps, nil
	case "apply":
		f.applyRun++
		f.answers = map[string]string{
			"volume": in.Option("volume"),
			"output": in.Option("output"),
		}
		return "백업 결과 본문", nil
	}
	return "flag 결과", nil
}

func (f *stubWizard) WizardPreview(answers map[string]string) (string, error) {
	return f.preview, nil
}

func TestBackupWizardSelectsAVolumeCollectsTheOutputPathAndConfirmsTheEffect(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태"}
	backup := &stubWizard{
		stubFeature: &stubFeature{desc: core.Descriptor{
			Name: "backup", Title: "데이터 백업", Summary: "백업.", Usage: "dcraft backup",
			Section: core.SectionData,
			Wizard:  true, RecordsHistory: true,
			Flags: []core.Flag{
				{Name: "output", Shorthand: "o", Kind: core.FlagString, Required: true, Usage: "출력 폴더"},
				{Name: "volume", Kind: core.FlagList, Required: true, Usage: "볼륨 키"},
			},
		}, body: "백업 결과 본문"},
		steps: core.WizardSpec{
			Title: "데이터 백업 설정",
			Steps: []core.WizardStep{
				{
					Name: "volume", Title: "볼륨 선택", Prompt: "볼륨을 고르",
					Options: []core.WizardOption{
						{Value: "data", Label: "data → proj_data · 서비스 app"},
						{Value: "logs", Label: "logs → proj_logs"},
					},
				},
				{
					Name: "output", Title: "저장 폴더", Prompt: "폴더 입력",
					AllowCustom: true, Default: "./dcraft-backups",
				},
			},
		},
		preview: "백업 계획\n볼륨: data → proj_data\n영향 서비스: app\n실행 중(중지 예정): app\n저장 폴더: ./dcraft-backups\n민감정보 안내",
	}
	m := newTestModel(t, status, backup)
	initModel(t, m)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if !strings.Contains(m.View(), "데이터 백업 설정") || !strings.Contains(m.View(), "data → proj_data") {
		t.Fatalf("wizard steps not shown:\n%s", m.View())
	}

	// Choose the first volume.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	if !strings.Contains(m.View(), "저장 폴더") || !strings.Contains(m.View(), "./dcraft-backups") {
		t.Fatalf("output path input not shown with its default:\n%s", m.View())
	}

	// Accept the shown default output path, then inspect the effect preview.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	for _, want := range []string{"영향 서비스: app", "실행 중", "./dcraft-backups", "민감정보"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("preview missing %q:\n%s", want, m.View())
		}
	}
	if backup.applyRun != 0 {
		t.Fatal("feature ran before the preview was confirmed")
	}

	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if backup.applyRun != 1 {
		t.Fatalf("apply runs = %d, want 1", backup.applyRun)
	}
	if backup.answers["volume"] != "data" || backup.answers["output"] != "./dcraft-backups" {
		t.Fatalf("answers = %v", backup.answers)
	}
	// A data operation holds its report until Enter.
	if !strings.Contains(m.View(), "완료 · enter 계속") {
		t.Fatalf("the finished screen does not state completion:\n%s", m.View())
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "백업 결과 본문") {
		t.Fatalf("result not shown:\n%s", m.View())
	}
}

func TestBackupWizardCancelRunsNothing(t *testing.T) {
	status := &stubFeature{desc: descriptor("status", true), body: "상태"}
	backup := &stubWizard{
		stubFeature: &stubFeature{desc: core.Descriptor{
			Name: "backup", Title: "데이터 백업", Summary: "백업.", Usage: "dcraft backup",
			Section: core.SectionData,
			Wizard:  true,
		}},
		steps:   core.WizardSpec{Title: "설정", Steps: []core.WizardStep{{Name: "volume", Title: "볼륨", Prompt: "고르", Options: []core.WizardOption{{Value: "data", Label: "data"}}}}},
		preview: "미리보기",
	}
	m := newTestModel(t, status, backup)
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(*Model)
	if backup.applyRun != 0 {
		t.Fatalf("canceled wizard ran the feature: %d", backup.applyRun)
	}
}
