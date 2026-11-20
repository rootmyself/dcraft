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
	m.wizardChecked = map[string]bool{}
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
	if first := m.firstVisibleStep(0); first >= 0 {
		m.wizardStep = first
	}
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
		m.wizardChecked = map[string]bool{}
		if step.Multi {
			for _, value := range strings.Split(m.wizardAnswers[step.Name], "\n") {
				if value = strings.TrimSpace(value); value != "" {
					m.wizardChecked[value] = true
				}
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
			case " ":
				if step.Multi {
					value := step.Options[m.wizardChoice].Value
					m.wizardChecked[value] = !m.wizardChecked[value]
				}
			case "enter":
				value := step.Options[m.wizardChoice].Value
				if step.Multi {
					var chosen []string
					for _, option := range step.Options {
						if m.wizardChecked[option.Value] {
							chosen = append(chosen, option.Value)
						}
					}
					if len(chosen) == 0 {
						// Enter on an untouched list selects the highlighted
						// option, preserving the simple one-choice flow.
						chosen = append(chosen, value)
					}
					value = strings.Join(chosen, "\n")
				}
				m.wizardAnswers[step.Name] = value
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
			if value == "" && !step.Optional {
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
			if prev := m.previousVisibleStep(len(m.wizardSpec.Steps) - 1); prev >= 0 {
				m.wizardStep = prev
				m.phase = phaseWizardStep
				m.initWizardStep()
			}
		}
	}
	return nil
}

// wizardStepVisible evaluates a step's single dependency against the answers
// collected so far. A hidden step never contributes an answer.
func wizardStepVisible(answers map[string]string, step core.WizardStep) bool {
	if step.DependsOn == "" {
		return true
	}
	return answers[step.DependsOn] == step.DependsValue
}

func (m *Model) firstVisibleStep(from int) int {
	for i := from; i < len(m.wizardSpec.Steps); i++ {
		if wizardStepVisible(m.wizardAnswers, m.wizardSpec.Steps[i]) {
			return i
		}
	}
	return -1
}

func (m *Model) previousVisibleStep(from int) int {
	for i := from; i >= 0 && i < len(m.wizardSpec.Steps); i-- {
		if wizardStepVisible(m.wizardAnswers, m.wizardSpec.Steps[i]) {
			return i
		}
	}
	return -1
}

func (m *Model) wizardProgress() (int, int) {
	ordinal, total := 0, 0
	for i, step := range m.wizardSpec.Steps {
		if !wizardStepVisible(m.wizardAnswers, step) {
			continue
		}
		total++
		if i <= m.wizardStep {
			ordinal = total
		}
	}
	if ordinal == 0 {
		ordinal = 1
	}
	if total == 0 {
		total = 1
	}
	return ordinal, total
}

func (m *Model) previousWizardStep() {
	if prev := m.previousVisibleStep(m.wizardStep - 1); prev >= 0 {
		m.wizardStep = prev
		m.phase = phaseWizardStep
		m.initWizardStep()
	}
}

func (m *Model) advanceWizard() {
	if next := m.firstVisibleStep(m.wizardStep + 1); next >= 0 {
		m.wizardStep = next
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
	if m.req.Options == nil {
		m.req.Options = map[string]string{}
	}
	for key, value := range options {
		m.req.Options[key] = value
	}
	feature := m.wizardFeature
	m.req.Feature = feature.Descriptor().Name
	m.req.Target = m.target
	// The wizard preview is not an approval. An approval-gated feature goes
	// through the same generic plan + confirmation screen as any other
	// approval operation; the collected answers stay in the request.
	if feature.Descriptor().Approval != core.ApprovalNone || len(m.wizardSpec.RequiredApprovals) > 0 {
		m.wizardApplying = false
		m.wizardAwaiting = false
		m.wizardFeature = nil
		m.wizardSpec = core.WizardSpec{}
		m.wizardAnswers = nil
		m.wizardPreview = ""
		m.wizardPreviewErr = nil
		m.wizardReturnRes = nil
		m.wizardReturnFeat = nil
		m.resetRunState()
		m.phase = phasePlanning
		return tea.Batch(m.planCmd(feature, m.events), waitStageCmd(m.events))
	}
	m.wizardApplying = true
	cmd := m.beginWithOptions(feature, options)
	m.phase = phaseLoading
	return cmd
}

func (m *Model) cancelWizard() {
	// Canceling the read-only steps run has no effect to report. Canceling the
	// apply run must let the engine's canceled result (with partial-effect
	// guidance) reach the screen instead of claiming nothing happened.
	if m.phase == phaseWizardLoading && m.wizardAwaiting {
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

// previewApprovals lists every independent approval the apply will need: a
// descriptor approval and any approvals the feature declared in the spec.
func (m *Model) previewApprovals() []core.ApprovalKind {
	var kinds []core.ApprovalKind
	if m.wizardFeature != nil {
		if kind := m.wizardFeature.Descriptor().Approval; kind != core.ApprovalNone {
			kinds = append(kinds, kind)
		}
	}
	kinds = append(kinds, m.wizardSpec.RequiredApprovals...)
	return uniqueApprovalKinds(kinds)
}

func (m *Model) wizardView() string {
	var b strings.Builder
	switch m.phase {
	case phaseWizardLoading:
		b.WriteString(m.title(msgWizardLoading))
		b.WriteString("\n")
		return b.String()
	case phaseWizardStep:
		if m.wizardSpec.Title != "" {
			b.WriteString(m.title(m.wizardSpec.Title))
			b.WriteString("\n\n")
		}
		if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
			return b.String()
		}
		step := m.wizardSpec.Steps[m.wizardStep]
		ordinal, total := m.wizardProgress()
		fmt.Fprintf(&b, "%s %d/%d\n", msgWizardProgress, ordinal, total)
		b.WriteString(m.title(step.Title))
		b.WriteString("\n\n")
		// A prompt is prose: it wraps at the content width instead of losing
		// its ending to the frame's truncation marker. A narrower pane (the
		// wide layout's sidebar) must not delete instructions.
		b.WriteString(m.wrapProse(step.Prompt))
		b.WriteString("\n\n")
		if len(step.Options) > 0 {
			for i, option := range step.Options {
				marker := "  "
				if i == m.wizardChoice {
					marker = "> "
				}
				if step.Multi {
					box := "[ ]"
					if m.wizardChecked[option.Value] {
						box = "[x]"
					}
					fmt.Fprintf(&b, "%s%s %s\n", marker, box, option.Label)
					continue
				}
				fmt.Fprintf(&b, "%s%s\n", marker, option.Label)
			}
		} else if step.AllowCustom {
			input := m.wizardInput
			if input == "" {
				input = " "
			}
			fmt.Fprintf(&b, "%s%s_\n", msgWizardInputLabel, input)
			hint := step.CustomHint
			if step.Optional {
				if hint != "" {
					hint += " · "
				}
				hint += msgWizardOptionalHint
			}
			if hint != "" {
				b.WriteString(m.hintText(hint))
				b.WriteString("\n")
			}
		}
	case phaseWizardPreview:
		if approvals := m.previewApprovals(); len(approvals) > 0 {
			for _, kind := range approvals {
				fmt.Fprintf(&b, "%s: %s (%s)\n", msgWizardRequiredApprovals, kind.KoreanLabel(), kind)
			}
			b.WriteString("enter 계획 확인 · left 이전 단계 · esc 취소\n\n")
		}
		// The preview is the feature's own prose; a narrower content pane must
		// not delete the part that explains the effect.
		b.WriteString(m.wrapProse(m.wizardPreview))
		b.WriteString("\n")
	}
	if m.notice != "" {
		b.WriteString("\n")
		b.WriteString(m.noticeText(m.notice))
		b.WriteString("\n")
	}
	return b.String()
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
		d := ce.Diagnostics[0]
		if strings.TrimSpace(d.Detail) != "" {
			return d.Message + "\n" + d.Detail
		}
		return d.Message
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
