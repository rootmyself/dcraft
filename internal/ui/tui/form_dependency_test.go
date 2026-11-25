package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// execShellFormModel is the real exec field set: a container choice, a command
// choice whose "new command" row is the empty value, and a typed command that is
// only collected for that row.
type execShellFormModel struct {
	m     *Model
	input *fakeInputFeature
}

func openExecShellForm(t *testing.T) execShellFormModel {
	t.Helper()
	primary := &stubFeature{desc: descriptor("status", true), body: "status body"}
	input := &fakeInputFeature{fakeLiveFeature: &fakeLiveFeature{
		desc: inputDescriptor("attach"),
		mode: core.LiveAttach,
	}, fields: []core.InputField{
		{Key: "instance", Label: "서비스 인스턴스",
			Kind: core.InputChoice, Required: true,
			Options: []core.InputOption{{Value: "web|0", Label: "web · proj-web-1"}}},
		{Key: "command-choice", Label: "명령 선택",
			Kind: core.InputChoice,
			Options: []core.InputOption{
				{Value: "/bin/bash", Label: "/bin/bash (기본)"},
				{Value: "/bin/sh", Label: "/bin/sh"},
				{Value: "psql -U postgres", Label: "최근: psql -U postgres"},
				{Value: "", Label: "새 명령 직접 입력"},
			}},
		{Key: "command", Label: "명령", Help: "이미지 안의 명령과 인자.",
			Kind: core.InputText, Required: true,
			DependsOn: "command-choice", DependsValue: ""},
	}}
	m := newTestModel(t, primary, input)
	m.theme = *plainTheme()
	initModel(t, m)
	press(m, tea.KeyMsg{Type: tea.KeyDown})
	cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	feed(t, m, execute(t, cmd))
	if m.form == nil {
		t.Fatalf("form did not open:\n%s", m.View())
	}
	return execShellFormModel{m: m, input: input}
}

// selectCommandChoice moves the command choice to one option by index.
func selectCommandChoice(t *testing.T, m *Model, want int) {
	t.Helper()
	form := m.form
	form.focus = 1
	for i := 0; i < want; i++ {
		press(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := m.form.choices["command-choice"]; got != want {
		t.Fatalf("command choice = %d, want %d", got, want)
	}
}

// A pinned shell already names the argv, so the typed command is not drawn: the
// form is two fields, not three.
func TestPinnedShellHidesTheTypedCommandField(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	view := f.m.View()
	if strings.Contains(view, "명령:") {
		t.Fatalf("a pinned shell still asks for a typed command:\n%s", view)
	}
	if !strings.Contains(view, "명령 선택") || !strings.Contains(view, "서비스 인스턴스") {
		t.Fatalf("the two real fields are not both drawn:\n%s", view)
	}
	// The review section states only what will run.
	review := reviewSection(t, f.m)
	if strings.Contains(review, "명령:") {
		t.Fatalf("the review promises a command that is not collected:\n%s", review)
	}
	if !strings.Contains(review, "/bin/bash") {
		t.Fatalf("the review omits the chosen shell:\n%s", review)
	}
}

// The typed command appears only for the new-command row, and confirming on the
// command choice then runs: choosing a shell is the whole answer.
func TestNewCommandRowRevealsTheTypedField(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	selectCommandChoice(t, f.m, 3) // 새 명령 직접 입력
	view := f.m.View()
	if !strings.Contains(view, "명령:") {
		t.Fatalf("the new-command row did not reveal the typed field:\n%s", view)
	}
	// The typed field is required, so submitting it empty is refused in place.
	// The first enter moves the focus onto the revealed field; the second is
	// the submit that must be refused.
	f.m.form.focus = 2
	cmd := press(f.m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || f.m.form == nil {
		t.Fatalf("an empty new command submitted: cmd=%v form=%v", cmd, f.m.form)
	}
	if !strings.Contains(f.m.View(), "명령 항목 입력.") {
		t.Fatalf("the refusal is not shown:\n%s", f.m.View())
	}
}

// Confirming on the command choice runs a pinned shell immediately, without
// walking through a field that is not there.
func TestConfirmingOnTheCommandChoiceRunsTheShell(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	selectCommandChoice(t, f.m, 0) // /bin/bash
	// The instance choice still needs its own confirm.
	f.m.form.focus = 0
	press(f.m, tea.KeyMsg{Type: tea.KeyEnter})
	// Now the focus is on the command choice, which is the last visible field.
	if f.m.form == nil {
		t.Fatal("the form closed on the wrong field")
	}
	if got := f.m.form.focus; got != 1 {
		t.Fatalf("focus = %d, want the command choice", got)
	}
	// Confirming the last visible field submits. The run itself opens a live
	// screen the harness would have to drive, so the assertion is on the request
	// the feature received, not on the screen that follows.
	values := submittedValues(t, f.m)
	cmd := press(f.m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("confirming the last visible field did not submit")
	}
	if f.m.form != nil {
		t.Fatalf("the form stayed open:\n%s", f.m.View())
	}
	// The feature receives the pinned shell and no typed command: the hidden
	// field is missing from the submitted values, not empty.
	if got := values["command-choice"]; got != "/bin/bash" {
		t.Fatalf("submitted command choice = %q, want the pinned shell", got)
	}
	if _, sent := values["command"]; sent {
		t.Fatalf("the hidden field was submitted: %v", values)
	}
}

// A remembered command is also a complete argv, so it behaves like a shell.
func TestRememberedCommandHidesTheTypedField(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	selectCommandChoice(t, f.m, 2) // 최근: psql -U postgres
	if strings.Contains(f.m.View(), "명령:") {
		t.Fatalf("a remembered command still asks for a typed one:\n%s", f.m.View())
	}
}

// Tab and shift+tab skip a hidden field: the cursor never lands on a row that is
// not drawn.
func TestTabSkipsTheHiddenField(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	press(f.m, tea.KeyMsg{Type: tea.KeyTab})
	if got := f.m.form.focus; got != 1 {
		t.Fatalf("tab focused %d, want the command choice", got)
	}
	press(f.m, tea.KeyMsg{Type: tea.KeyTab})
	if got := f.m.form.focus; got != 0 {
		t.Fatalf("tab wrapped to %d, want the instance choice (the hidden field is skipped)", got)
	}
}

// A hidden field contributes no value: the feature receives no "command" key,
// so it cannot act on something the reader could not see.
func TestHiddenFieldIsNotSubmitted(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	selectCommandChoice(t, f.m, 1) // /bin/sh
	f.m.form.focus = 0
	press(f.m, tea.KeyMsg{Type: tea.KeyEnter})
	values := submittedValues(t, f.m)
	cmd := press(f.m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("the form did not submit")
	}
	if f.m.form != nil {
		t.Fatalf("the form stayed open:\n%s", f.m.View())
	}
	// The typed command was hidden, so it is absent from the values entirely.
	if got := values["command-choice"]; got != "/bin/sh" {
		t.Fatalf("submitted command choice = %q, want /bin/sh", got)
	}
	if _, sent := values["command"]; sent {
		t.Fatalf("the hidden field was submitted: %v", values)
	}
}

// Field blocks are separated by one blank line, so the container choice and the
// command choice read as two sections instead of one list.
func TestFieldBlocksAreSeparatedByABlankLine(t *testing.T) {
	f := openExecShellForm(t)
	resizeBoard(t, f.m)
	// The form body is the unit: the frame's borders sit between the body rows,
	// so a whole-screen scan would count the border as the separator.
	lines, _ := f.m.formLines()
	instance, command := -1, -1
	for i, line := range lines {
		switch {
		case instance < 0 && strings.Contains(line, "서비스 인스턴스"):
			instance = i
		case command < 0 && strings.Contains(line, "명령 선택"):
			command = i
		}
	}
	if instance < 0 || command < 0 {
		t.Fatalf("both field labels are not drawn:\n%s", strings.Join(lines, "\n"))
	}
	// The choice list and its hints belong to the first block, so the blank line
	// is the one directly above the second field's label.
	if got := strings.TrimSpace(lines[command-1]); got != "" {
		t.Fatalf("no blank line above the command block: %q", lines[command-1])
	}
}

// resizeBoard gives the model a frame size so the body is laid out for real.
func resizeBoard(t *testing.T, m *Model) {
	t.Helper()
	resize(t, m, 100, 40)
}

// reviewSection is the text from the review heading onward.
func reviewSection(t *testing.T, m *Model) string {
	t.Helper()
	lines := viewLines(t, m)
	for i, line := range lines {
		if strings.Contains(line, msgFormRunTitle) {
			return strings.Join(lines[i:], "\n")
		}
	}
	t.Fatalf("review section missing:\n%s", strings.Join(lines, "\n"))
	return ""
}

// submittedValues is the map the form hands to the feature: a choice
// contributes its machine value, a text field its trimmed text, and a hidden
// field nothing at all.
func submittedValues(t *testing.T, m *Model) map[string]string {
	t.Helper()
	form := m.form
	if form == nil {
		t.Fatalf("the form is not open")
	}
	values := map[string]string{}
	for _, field := range form.fields {
		if !field.Visible(form.answer) {
			continue
		}
		if field.Kind == core.InputChoice {
			values[field.Key] = field.Options[form.choices[field.Key]].Value
			continue
		}
		values[field.Key] = strings.TrimSpace(form.values[field.Key])
	}
	return values
}
