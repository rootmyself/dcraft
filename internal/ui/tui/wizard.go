package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// beginWizard starts the guided flow for a feature that declares the Wizard
// capability. The feature still runs through the common engine for both the
// step listing and the final save.
func (m *Model) beginWizard(f core.Feature) tea.Cmd {
	if f == nil || !f.Descriptor().Wizard {
		return nil
	}
	if _, ok := f.(core.Wizard); !ok {
		m.notice = msgWizardUnavailable
		return nil
	}
	m.wizardReturnRes = m.result
	m.wizardReturnFeat = m.active
	m.wizardFeature = f
	m.wizardSpec = core.WizardSpec{}
	m.wizardStep = 0
	m.wizardChoice = 0
	m.wizardInput = ""
	m.wizardAnswers = map[string]string{}
	m.wizardPreview = ""
	m.wizardPreviewErr = nil
	m.wizardAwaiting = true
	m.wizardApplying = false
	cmd := m.beginWithOptions(f, map[string]string{"wizard": "steps"})
	m.phase = phaseWizardLoading
	return cmd
}

func (m *Model) beginWizardSteps(res core.Result) {
	if res.Outcome != core.OutcomeSuccess {
		m.wizardFeature = nil
		m.wizardReturnRes = nil
		m.wizardReturnFeat = nil
		m.result = &res
		m.phase = phaseReady
		return
	}
	spec, ok := wizardSpecFrom(res.Data)
	if !ok {
		failure := res
		failure.Outcome = core.OutcomeFailure
		failure.Data = nil
		failure.Diagnostics = append(failure.Diagnostics, core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgWizardPayload,
			NextActions: []string{msgWizardPayloadAction},
		})
		m.wizardFeature = nil
		m.wizardReturnRes = nil
		m.wizardReturnFeat = nil
		m.result = &failure
		m.phase = phaseReady
		return
	}
	m.wizardSpec = spec
	m.wizardStep = 0
	m.phase = phaseWizardStep
	m.initWizardStep()
}

func (m *Model) isWizardPhase() bool {
	switch m.phase {
	case phaseWizardLoading, phaseWizardStep, phaseWizardPreview:
		return true
	default:
		return false
	}
}

func (m *Model) wizardTextInput() bool {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return false
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	return len(step.Options) == 0 && step.AllowCustom
}

func (m *Model) initWizardStep() {
	if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	m.notice = ""
	if len(step.Options) > 0 {
		wanted := step.Default
		if previous, ok := m.wizardAnswers[step.Name]; ok && previous != "" {
			wanted = previous
		}
		m.wizardChoice = 0
		for i, option := range step.Options {
			if option.Value == wanted {
				m.wizardChoice = i
				break
			}
		}
		return
	}
	m.wizardInput = m.wizardAnswers[step.Name]
	if m.wizardInput == "" {
		m.wizardInput = step.Default
	}
}

func (m *Model) updateWizard(msg tea.KeyMsg) tea.Cmd {
	switch m.phase {
	case phaseWizardStep:
		if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
			return nil
		}
		step := m.wizardSpec.Steps[m.wizardStep]
		if len(step.Options) > 0 {
			switch msg.String() {
			case "up", "k":
				m.wizardChoice = (m.wizardChoice - 1 + len(step.Options)) % len(step.Options)
			case "down", "j":
				m.wizardChoice = (m.wizardChoice + 1) % len(step.Options)
			case "enter":
				m.wizardAnswers[step.Name] = step.Options[m.wizardChoice].Value
				m.advanceWizard()
			case "left":
				m.previousWizardStep()
			}
			return nil
		}
		if !step.AllowCustom {
			if msg.String() == "enter" {
				m.notice = msgWizardNeedChoice
			}
			return nil
		}
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(m.wizardInput)
			if value == "" {
				m.notice = msgWizardNeedValue
				return nil
			}
			m.wizardAnswers[step.Name] = value
			m.advanceWizard()
		case "left":
			if m.wizardInput == "" {
				m.previousWizardStep()
			}
		case "backspace":
			runes := []rune(m.wizardInput)
			if len(runes) > 0 {
				m.wizardInput = string(runes[:len(runes)-1])
			} else {
				m.previousWizardStep()
			}
		default:
			if len(msg.Runes) > 0 {
				m.wizardInput += string(msg.Runes)
			}
		}
	case phaseWizardPreview:
		switch msg.String() {
		case "enter":
			if m.wizardPreviewErr == nil {
				return m.applyWizard()
			}
		case "left":
			if len(m.wizardSpec.Steps) > 0 {
				m.wizardStep = len(m.wizardSpec.Steps) - 1
				m.phase = phaseWizardStep
				m.initWizardStep()
			}
		}
	}
	return nil
}

func (m *Model) previousWizardStep() {
	if m.wizardStep > 0 {
		m.wizardStep--
		m.phase = phaseWizardStep
		m.initWizardStep()
	}
}

func (m *Model) advanceWizard() {
	if m.wizardStep+1 < len(m.wizardSpec.Steps) {
		m.wizardStep++
		m.initWizardStep()
		return
	}
	wizard, ok := m.wizardFeature.(core.Wizard)
	if !ok {
		m.notice = msgWizardUnavailable
		return
	}
	text, err := wizard.WizardPreview(m.wizardAnswers)
	m.phase = phaseWizardPreview
	m.wizardPreview = text
	m.wizardPreviewErr = err
	m.notice = ""
	if err != nil {
		m.wizardPreview = msgWizardPreviewFailed + "\n" + wizardErrorText(err)
	}
}

func (m *Model) applyWizard() tea.Cmd {
	if m.wizardFeature == nil {
		return nil
	}
	options := map[string]string{"wizard": "apply"}
	for key, value := range m.wizardAnswers {
		options[key] = value
	}
	m.wizardApplying = true
	cmd := m.beginWithOptions(m.wizardFeature, options)
	m.phase = phaseLoading
	return cmd
}

func (m *Model) cancelWizard() {
	if m.phase == phaseWizardLoading {
		m.wizardCanceled = true
	}
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
		m.runCtx = nil
	}
	m.wizardAwaiting = false
	m.wizardApplying = false
	m.wizardFeature = nil
	m.wizardSpec = core.WizardSpec{}
	m.wizardAnswers = nil
	m.wizardPreview = ""
	m.wizardPreviewErr = nil
	if m.wizardReturnFeat != nil {
		m.active = m.wizardReturnFeat
	}
	if m.wizardReturnRes != nil {
		m.result = m.wizardReturnRes
	}
	m.wizardReturnRes = nil
	m.wizardReturnFeat = nil
	m.currentStage = nil
	m.phase = phaseReady
	m.notice = msgWizardCanceled
}

func (m *Model) wizardView() string {
	var b strings.Builder
	switch m.phase {
	case phaseWizardLoading:
		b.WriteString(titleStyle.Render(msgWizardLoading))
		b.WriteString("\n")
		return b.String()
	case phaseWizardStep:
		if m.wizardSpec.Title != "" {
			b.WriteString(titleStyle.Render(m.wizardSpec.Title))
			b.WriteString("\n\n")
		}
		if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
			return b.String()
		}
		step := m.wizardSpec.Steps[m.wizardStep]
		fmt.Fprintf(&b, "%s %d/%d\n", msgWizardProgress, m.wizardStep+1, len(m.wizardSpec.Steps))
		b.WriteString(titleStyle.Render(step.Title))
		b.WriteString("\n\n")
		b.WriteString(step.Prompt)
		b.WriteString("\n\n")
		if len(step.Options) > 0 {
			for i, option := range step.Options {
				marker := "  "
				if i == m.wizardChoice {
					marker = "> "
				}
				fmt.Fprintf(&b, "%s%s\n", marker, option.Label)
			}
		} else if step.AllowCustom {
			input := m.wizardInput
			if input == "" {
				input = " "
			}
			fmt.Fprintf(&b, "%s%s_\n", msgWizardInputLabel, input)
			if step.CustomHint != "" {
				b.WriteString(hintStyle.Render(step.CustomHint))
				b.WriteString("\n")
			}
		}
	case phaseWizardPreview:
		b.WriteString(m.wizardPreview)
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString("\n")
		b.WriteString(noticeStyle.Render(m.notice))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) wizardHints() string {
	switch m.phase {
	case phaseWizardStep:
		if m.wizardTextInput() {
			return msgHintWizardInput
		}
		return msgHintWizardChoice
	case phaseWizardPreview:
		if m.wizardPreviewErr != nil {
			return msgHintWizardInvalid
		}
		return msgHintWizardPreview
	default:
		return msgHintWizardLoading
	}
}

func wizardSpecFrom(data any) (core.WizardSpec, bool) {
	switch v := data.(type) {
	case core.WizardSpec:
		return v, true
	case *core.WizardSpec:
		if v != nil {
			return *v, true
		}
	}
	return core.WizardSpec{}, false
}

func wizardErrorText(err error) string {
	var ce *core.Error
	if errorsAs(err, &ce) && len(ce.Diagnostics) > 0 {
		return ce.Diagnostics[0].Message
	}
	return err.Error()
}

func errorsAs(err error, target **core.Error) bool {
	for err != nil {
		if ce, ok := err.(*core.Error); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
