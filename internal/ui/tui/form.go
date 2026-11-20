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
}

func newFormState(feature core.Feature, fields []core.InputField, base core.Request) *formState {
	form := &formState{
		feature: feature,
		base:    base,
		fields:  fields,
		values:  map[string]string{},
		choices: map[string]int{},
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
		return m, nil
	case "shift+tab":
		form.focus = (form.focus - 1 + len(form.fields)) % len(form.fields)
		form.notice = ""
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

// formView shows every field and the exact values that will run. Nothing is
// sent to the engine until the last field is confirmed.
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
				cursor = "▌"
			}
			fmt.Fprintf(&b, "%s%s: %s%s\n", marker, field.Label, form.values[field.Key], cursor)
			if i == form.focus && field.Help != "" {
				fmt.Fprintf(&b, "    %s\n", m.hintText(field.Help))
			}
		}
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", m.title(msgFormRunTitle))
	for _, field := range form.fields {
		value := form.values[field.Key]
		if field.Kind == core.InputChoice && len(field.Options) > 0 {
			value = field.Options[form.choices[field.Key]].Label
		}
		if value == "" {
			value = msgFormEmpty
		}
		fmt.Fprintf(&b, "  %s: %s\n", field.Label, value)
	}
	if form.notice != "" {
		b.WriteString("\n")
		b.WriteString(m.noticeText(form.notice))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
