package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// execFormModel opens the exec input form with one choice field and one text
// field, exactly as the feature declares them.
type execFormModel struct {
	m     *Model
	input *fakeInputFeature
}

func openExecForm(t *testing.T) execFormModel {
	t.Helper()
	primary := &stubFeature{desc: descriptor("status", true), body: "status body"}
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{
		desc: inputDescriptor("attach"),
		mode: core.LiveAttach,
	}, fields: []core.InputField{
		{Key: "instance", Label: "서비스 인스턴스", Help: "실행 중인 인스턴스를 고르세요.",
			Kind: core.InputChoice, Required: true,
			Options: []core.InputOption{{Value: "web|1", Label: "web · proj-web-2"}}},
		{Key: "command", Label: "명령", Help: "이미지 안에서 실행할 명령입니다.",
			Kind: core.InputText, Required: true},
	}}
	m := newTestModel(t, primary, input)
	m.theme = *plainTheme()
	initModel(t, m)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(*Model)
	feed(t, m, execute(t, cmd))
	if m.form == nil {
		t.Fatalf("form did not open:\n%s", m.View())
	}
	return execFormModel{m: m, input: input}
}

// The form states every field's label and its help, not only the focused
// field's: the review must be readable without walking the fields again.
func TestFormShowsEveryLabelAndItsHelp(t *testing.T) {
	form := openExecForm(t)
	view := form.m.View()
	for _, want := range []string{
		"실행 전 확인", "서비스 인스턴스", "실행 중인 인스턴스를 고르세요.",
		"명령", "이미지 안에서 실행할 명령입니다.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("form missing %q:\n%s", want, view)
		}
	}
}

// The review section states the exact values the feature will receive, not a
// friendly re-rendering of them.
func TestFormReviewShowsTheExactSubmittedValues(t *testing.T) {
	form := openExecForm(t)
	m := form.m
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // move to the command field
	m = nm.(*Model)
	for _, r := range "echo hi" {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = nm.(*Model)
	}
	lines := viewLines(t, m)
	review := -1
	for i, line := range lines {
		if strings.Contains(line, msgFormRunTitle) {
			review = i
			break
		}
	}
	if review < 0 {
		t.Fatalf("review section missing:\n%s", strings.Join(lines, "\n"))
	}
	section := strings.Join(lines[review:], "\n")
	for _, want := range []string{"서비스 인스턴스: web|1", "명령: echo hi"} {
		if !strings.Contains(section, want) {
			t.Fatalf("review missing %q:\n%s", want, section)
		}
	}
}

// An empty required field is refused in place: the guidance sits under the
// field, the form does not submit, and typing clears it.
func TestFormRequiredTextBlocksSubmissionInline(t *testing.T) {
	form := openExecForm(t)
	m := form.m
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // move to the command field
	m = nm.(*Model)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // submit with no command
	m = nm.(*Model)
	if cmd != nil {
		t.Fatalf("empty required field produced a command: %v", cmd)
	}
	if m.form == nil {
		t.Fatal("empty required field closed the form")
	}
	if form.input.runs != 0 {
		t.Fatal("empty required field ran the feature")
	}
	lines := viewLines(t, m)
	fieldRow, noticeRow := -1, -1
	for i, line := range lines {
		if fieldRow < 0 && strings.Contains(line, "> 명령:") {
			fieldRow = i
			continue
		}
		if fieldRow >= 0 && strings.Contains(line, "명령 항목을 입력하세요.") {
			noticeRow = i
			break
		}
	}
	if fieldRow < 0 || noticeRow != fieldRow+1 {
		t.Fatalf("required guidance is not under its field (field=%d notice=%d):\n%s",
			fieldRow, noticeRow, strings.Join(lines, "\n"))
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("date")})
	m = nm.(*Model)
	if strings.Contains(m.View(), "명령 항목을 입력하세요.") {
		t.Fatalf("typing did not clear the guidance:\n%s", m.View())
	}
}
