package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// formState collects the inputs a feature declared through core.InputProvider.
// It is generic: labels, options and meaning come from the feature, and the
// feature still validates every value with WithInputs before running.
type formState struct {
	feature core.Feature
	base    core.Request
	fields  []core.InputField
	values  map[string]string
	choices map[string]int
	focus   int
	notice  string
	// noticeField is the field the notice belongs to. The guidance is drawn
	// under that field, next to the value to correct, instead of at the
	// bottom of the screen where it is detached from both.
	noticeField int
}

func newFormState(feature core.Feature, fields []core.InputField, base core.Request) *formState {
	form := &formState{
		feature:     feature,
		base:        base,
		fields:      fields,
		values:      map[string]string{},
		choices:     map[string]int{},
		noticeField: -1,
	}
	for _, field := range fields {
		if field.Kind == core.InputChoice && len(field.Options) > 0 {
			form.choices[field.Key] = 0
		}
		// A text field with a dependency starts empty and hidden; the choice it
		// depends on decides whether it is collected.
		form.values[field.Key] = ""
	}
	// The focus starts on the first field that is actually drawn.
	if visible := form.visibleFields(); len(visible) > 0 {
		form.focus = visible[0]
	}
	return form
}

// formFocusIsText reports whether the focused field accepts typed text, which
// means "?" is a character there and not the help key.
func (m *Model) formFocusIsText() bool {
	if m.form == nil || m.form.focus < 0 || m.form.focus >= len(m.form.fields) {
		return false
	}
	return m.form.fields[m.form.focus].Kind == core.InputText
}

// answer is one field's current value: the selected option for a choice, the
// typed text for a text field. It is what a dependency compares against and what
// the field is drawn from, so both read the same answer.
func (f *formState) answer(key string) string {
	field, ok := core.InputDependsOn(f.fields, key)
	if !ok {
		return ""
	}
	if field.Kind == core.InputChoice {
		options := field.Options
		if len(options) == 0 {
			return ""
		}
		index := f.choices[key]
		if index < 0 || index >= len(options) {
			index = 0
		}
		return options[index].Value
	}
	return f.values[key]
}

// visibleFields is the drawn and collected fields in declaration order. A field
// whose dependency is unmet is not drawn, not focused and not submitted.
func (f *formState) visibleFields() []int {
	out := make([]int, 0, len(f.fields))
	for i, field := range f.fields {
		if field.Visible(f.answer) {
			out = append(out, i)
		}
	}
	return out
}

// nextVisible moves the focus by delta through the visible fields only, so tab
// never lands on a field that is not on screen.
func (f *formState) nextVisible(delta int) {
	visible := f.visibleFields()
	if len(visible) == 0 {
		return
	}
	pos := 0
	for i, index := range visible {
		if index == f.focus {
			pos = i
			break
		}
	}
	pos = (pos + delta + len(visible)) % len(visible)
	f.focus = visible[pos]
}

// fieldAfter is the next visible field after index, or false when index is the
// last one. Confirm on the last field submits; confirming anywhere else moves
// on, which is why the answer that just changed is re-read here.
func (f *formState) fieldAfter(index int) (int, bool) {
	for _, candidate := range f.visibleFields() {
		if candidate > index {
			return candidate, true
		}
	}
	return 0, false
}

func (m *Model) updateFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	form := m.form
	if form == nil {
		return m, nil
	}
	field := form.fields[form.focus]
	if msg.String() == "?" && m.helpAvailable() && field.Kind != core.InputText {
		// A text field accepts "?" as input; only choice fields let it open
		// the keymap.
		m.openHelp()
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.form = nil
		m.phase = phaseReady
		m.notice = msgFormCanceled
		return m, nil
	case "tab":
		form.nextVisible(1)
		form.notice = ""
		form.noticeField = -1
		return m, nil
	case "shift+tab":
		form.nextVisible(-1)
		form.notice = ""
		form.noticeField = -1
		return m, nil
	case "up":
		if field.Kind == core.InputChoice {
			form.choices[field.Key] = (form.choices[field.Key] - 1 + len(field.Options)) % len(field.Options)
		}
		return m, nil
	case "down":
		if field.Kind == core.InputChoice {
			form.choices[field.Key] = (form.choices[field.Key] + 1) % len(field.Options)
		}
		return m, nil
	case "left":
		if field.Kind == core.InputChoice {
			form.choices[field.Key] = (form.choices[field.Key] - 1 + len(field.Options)) % len(field.Options)
		}
		return m, nil
	case "right":
		if field.Kind == core.InputChoice {
			form.choices[field.Key] = (form.choices[field.Key] + 1) % len(field.Options)
		}
		return m, nil
	case "enter":
		// Changing a choice can hide the field the cursor is on (the typed
		// command) or reveal one, so the next focused field is recomputed from
		// the answers as they are now.
		if next, ok := form.fieldAfter(form.focus); ok {
			form.focus = next
			form.notice = ""
			form.noticeField = -1
			return m, nil
		}
		return m.submitForm()
	case "backspace":
		if field.Kind == core.InputText {
			runes := []rune(form.values[field.Key])
			if len(runes) > 0 {
				form.values[field.Key] = string(runes[:len(runes)-1])
			}
		}
		return m, nil
	}
	if field.Kind == core.InputText {
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			// Typing is the correction: the inline guidance has done its job.
			form.notice = ""
			form.noticeField = -1
		}
		if msg.Type == tea.KeyRunes {
			form.values[field.Key] += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			form.values[field.Key] += " "
		}
	}
	return m, nil
}

func (m *Model) submitForm() (tea.Model, tea.Cmd) {
	form := m.form
	if form == nil {
		return m, nil
	}
	values := map[string]string{}
	for i, field := range form.fields {
		// A hidden field is not part of the request: the feature would
		// otherwise receive a value the reader cannot see or correct.
		if !field.Visible(form.answer) {
			continue
		}
		value := strings.TrimSpace(form.values[field.Key])
		if field.Kind == core.InputChoice {
			if len(field.Options) == 0 {
				continue
			}
			value = field.Options[form.choices[field.Key]].Value
		}
		if field.Required && value == "" {
			form.focus = i
			form.notice = fmt.Sprintf(msgFormRequired, field.Label)
			form.noticeField = i
			return m, nil
		}
		values[field.Key] = value
	}
	provider, ok := form.feature.(core.InputProvider)
	if !ok {
		m.form = nil
		m.phase = phaseReady
		m.notice = msgFormUnsupported
		return m, nil
	}
	req, err := provider.WithInputs(form.base, values)
	if err != nil {
		form.notice = inputErrorText(err)
		return m, nil
	}
	feature := form.feature
	m.form = nil
	return m, m.beginRequest(feature, req)
}

// formLines shows every field with its label and help, the value being typed,
// and the exact values that will run. It returns the drawn rows and the field
// and choice targets together, so a click resolves against what is on screen.
// Nothing is sent to the engine until the last field is confirmed.
//
// Each field opens a block, and the field's own label is the block heading: a
// choice's label states the decision it collects, so repeating it as a section
// title would say the same words twice. The heading carries the accent and the
// help stays muted, so a colorless terminal still reads the structure from the
// blank line and the label alone.
func (m *Model) formLines() ([]string, []bodyHit) {
	form := m.form
	if form == nil {
		return nil, nil
	}
	var lines []string
	var hits []bodyHit
	add := func(s string) { lines = append(lines, s) }
	add(m.title(msgFormTitle))
	add("")
	if form.feature != nil {
		desc := form.feature.Descriptor()
		add(fmt.Sprintf("%s: %s (%s)", msgFormAction, desc.Title, desc.Name))
	}
	dir := m.target.Path
	if dir == "" {
		dir = msgConfirmTargetNow
	}
	add(fmt.Sprintf("%s: %s", msgConfirmTargetLabel, dir))
	if m.target.Environment != "" {
		add(fmt.Sprintf("%s: %s", msgConfirmEnvironment, m.target.Environment))
	}
	add("")
	for i, field := range form.fields {
		// A field whose dependency is unmet is not drawn at all: the rule is
		// the field's own, so this renderer never learns which field hides
		// which.
		if !field.Visible(form.answer) {
			continue
		}
		// One blank line between blocks keeps the instance choice and the
		// command choice readable as two sections instead of one list.
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			add("")
		}
		marker := "  "
		if i == form.focus {
			marker = "> "
		}
		switch field.Kind {
		case core.InputChoice:
			hits = append(hits, bodyHit{row: len(lines), kind: hitFormField, index: i})
			add(marker + m.sectionLabel(field.Label))
			lines = append(lines, m.formNoticeLines(form, i)...)
			lines = append(lines, m.helpLines(field.Help)...)
			if i == form.focus {
				// The open list already marks the selected row, so repeating it
				// above the list would say the same value twice.
				for j, option := range field.Options {
					pointer := "   "
					if j == form.choices[field.Key] {
						pointer = " ◉ "
					}
					hits = append(hits, bodyHit{row: len(lines), kind: hitFormChoice, index: i, aux: j})
					add("  " + pointer + m.valueText(j == form.choices[field.Key], option.Label))
				}
				break
			}
			selected := ""
			if len(field.Options) > 0 {
				selected = field.Options[form.choices[field.Key]].Label
			}
			add("  " + m.valueText(true, selected))
		default:
			cursor := ""
			if i == form.focus {
				cursor = cursorGlyph
			}
			hits = append(hits, bodyHit{row: len(lines), kind: hitFormField, index: i})
			add(marker + m.sectionLabel(field.Label))
			add("  " + m.valueText(true, form.values[field.Key]) + cursor)
			lines = append(lines, m.formNoticeLines(form, i)...)
			lines = append(lines, m.helpLines(field.Help)...)
		}
	}
	add("")
	add(m.title(msgFormRunTitle))
	for _, field := range form.fields {
		if !field.Visible(form.answer) {
			continue
		}
		value := form.reviewValue(field)
		if value == "" {
			value = msgFormEmpty
		}
		add(fmt.Sprintf("  %s: %s", field.Label, value))
	}
	if form.notice != "" && form.noticeField < 0 {
		add("")
		add(m.noticeText(form.notice))
	}
	return lines, hits
}

func (m *Model) formView() string {
	lines, _ := m.formLines()
	return strings.Join(lines, "\n")
}

// valueText paints a collected value in the accent token, so the answer a
// reader is about to submit stands apart from its label and help.
func (m *Model) valueText(selected bool, text string) string {
	if !selected || text == "" {
		return text
	}
	return m.theme.Paint(visual.TokenAccent, text)
}

// formNoticeLines draws the inline guidance for one field, wrapped to the body
// width. It is called only for the field the notice belongs to.
func (m *Model) formNoticeLines(form *formState, field int) []string {
	if form.notice == "" || form.noticeField != field {
		return nil
	}
	var out []string
	for _, line := range m.wrapTo(form.notice, m.bodyWidth()) {
		out = append(out, m.noticeText(line))
	}
	return out
}

// helpLines renders a field's help under its control, indented to the field's
// value column. Help is a sentence, so it wraps instead of being cut.
func (m *Model) helpLines(help string) []string {
	if help == "" {
		return nil
	}
	var out []string
	for _, line := range m.wrapTo(help, m.bodyWidth()-4) {
		out = append(out, "    "+m.hintText(line))
	}
	return out
}

// reviewValue is exactly what the feature will receive for a field: a choice
// contributes its machine value; text is trimmed when submitted and is shown
// trimmed here so the review matches the run. A choice whose label differs
// from its value states both, because the label alone would hide what runs. A
// choice with an empty machine value (a row that means "something else") is
// named by its label instead, so the review never reads as "nothing chosen"
// while a row is plainly selected.
func (f *formState) reviewValue(field core.InputField) string {
	if field.Kind == core.InputChoice && len(field.Options) > 0 {
		option := field.Options[f.choices[field.Key]]
		if option.Value == "" {
			return option.Label
		}
		if option.Label == option.Value || strings.Contains(option.Label, option.Value) {
			return option.Value
		}
		return option.Value + " (" + option.Label + ")"
	}
	return strings.TrimSpace(f.values[field.Key])
}

func inputErrorText(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		text := ce.Diagnostics[0].Message
		if len(ce.Diagnostics[0].NextActions) > 0 {
			text += " " + ce.Diagnostics[0].NextActions[0]
		}
		return text
	}
	return err.Error()
}
