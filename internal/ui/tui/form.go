package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
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
		form.focus = (form.focus + 1) % len(form.fields)
		form.notice = ""
		form.noticeField = -1
		return m, nil
	case "shift+tab":
		form.focus = (form.focus - 1 + len(form.fields)) % len(form.fields)
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
		if form.focus < len(form.fields)-1 {
			form.focus++
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

// formView shows every field with its label and help, the value being typed,
// and the exact values that will run. Nothing is sent to the engine until the
// last field is confirmed. Guidance about a missing answer is drawn inline
// under the field it belongs to, so the value to correct is on the same row.
func (m *Model) formView() string {
	form := m.form
	var b strings.Builder
	b.WriteString(m.title(msgFormTitle))
	b.WriteString("\n\n")
	if form.feature != nil {
		desc := form.feature.Descriptor()
		fmt.Fprintf(&b, "%s: %s (%s)\n", msgFormAction, desc.Title, desc.Name)
	}
	dir := m.target.Path
	if dir == "" {
		dir = msgConfirmTargetNow
	}
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmTargetLabel, dir)
	if m.target.Environment != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmEnvironment, m.target.Environment)
	}
	b.WriteString("\n")
	for i, field := range form.fields {
		marker := "  "
		if i == form.focus {
			marker = "> "
		}
		switch field.Kind {
		case core.InputChoice:
			selected := ""
			if len(field.Options) > 0 {
				selected = field.Options[form.choices[field.Key]].Label
			}
			fmt.Fprintf(&b, "%s%s: %s\n", marker, field.Label, selected)
			m.writeFormNotice(&b, form, i)
			if field.Help != "" {
				b.WriteString(m.helpLine(field.Help))
			}
			if i == form.focus {
				for j, option := range field.Options {
					pointer := "   "
					if j == form.choices[field.Key] {
						pointer = " ◉ "
					}
					fmt.Fprintf(&b, "  %s%s\n", pointer, option.Label)
				}
			}
		default:
			cursor := ""
			if i == form.focus {
				cursor = cursorGlyph
			}
			fmt.Fprintf(&b, "%s%s: %s%s\n", marker, field.Label, form.values[field.Key], cursor)
			m.writeFormNotice(&b, form, i)
			if field.Help != "" {
				b.WriteString(m.helpLine(field.Help))
			}
		}
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", m.title(msgFormRunTitle))
	for _, field := range form.fields {
		value := form.reviewValue(field)
		if value == "" {
			value = msgFormEmpty
		}
		fmt.Fprintf(&b, "  %s: %s\n", field.Label, value)
	}
	if form.notice != "" && form.noticeField < 0 {
		b.WriteString("\n")
		b.WriteString(m.noticeText(form.notice))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeFormNotice draws the inline guidance for one field, wrapped to the body
// width. It is called only for the field the notice belongs to.
func (m *Model) writeFormNotice(b *strings.Builder, form *formState, field int) {
	if form.notice == "" || form.noticeField != field {
		return
	}
	for _, line := range m.wrapTo(form.notice, m.bodyWidth()) {
		b.WriteString(m.noticeText(line))
		b.WriteString("\n")
	}
}

// helpLine renders a field's help under its control, indented to the field's
// value column. Help is a sentence, so it wraps instead of being cut.
func (m *Model) helpLine(help string) string {
	var b strings.Builder
	for _, line := range m.wrapTo(help, m.bodyWidth()-4) {
		b.WriteString("    ")
		b.WriteString(m.hintText(line))
		b.WriteString("\n")
	}
	return b.String()
}

// reviewValue is exactly what the feature will receive for a field: a choice
// contributes its machine value; text is trimmed when submitted and is shown
// trimmed here so the review matches the run. A choice whose label differs
// from its value states both, because the label alone would hide what runs.
func (f *formState) reviewValue(field core.InputField) string {
	if field.Kind == core.InputChoice && len(field.Options) > 0 {
		option := field.Options[f.choices[field.Key]]
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
