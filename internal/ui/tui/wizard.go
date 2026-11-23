package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
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

// wizardStepIsMulti reports whether the current question is a checkbox list.
func (m *Model) wizardStepIsMulti() bool {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return false
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	return len(step.Options) > 0 && step.Multi
}

// wizardStepIsList reports whether the current question edits a selectable
// list: its items, plus the row that adds one. The keyboard and the wheel both
// ask this, so a list is one kind of answer in exactly one place. It is not
// wizardTextInput: a list accepts custom text too, but its cursor moves over
// rows.
func (m *Model) wizardStepIsList() bool {
	return m.wizardTextInput() && m.wizardSpec.Steps[m.wizardStep].List
}

// wizardStepIsOptional reports whether the current question may be answered
// with an empty value.
func (m *Model) wizardStepIsOptional() bool {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return false
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	return len(step.Options) == 0 && step.AllowCustom && step.Optional
}

func (m *Model) initWizardStep() {
	if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	m.notice = ""
	m.wizardNotice = ""
	m.wizardEditing = false
	// Each question starts at the top of the screen: a scroll position kept
	// from the previous step would hide the question being asked.
	m.body = visual.Viewport{}
	if step.List {
		m.wizardItems = nil
		value, answered := m.wizardAnswers[step.Name]
		if !answered {
			value = step.Default
		}
		for _, item := range strings.Split(value, "\n") {
			if item = strings.TrimSpace(item); item != "" {
				m.wizardItems = append(m.wizardItems, item)
			}
		}
		m.wizardChoice = len(m.wizardItems)
		m.wizardInput = ""
		return
	}
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
		if m.phase == phaseWizardStep {
			// Coming back to a choice step must show the restored card, not
			// the top of a list the reader already walked past.
			m.followWizardCursor()
		}
		return
	}
	m.wizardChoice = 0
	m.wizardChecked = map[string]bool{}
	m.wizardInput = m.wizardAnswers[step.Name]
	if m.wizardInput == "" {
		m.wizardInput = step.Default
	}
}

func (m *Model) updateWizard(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "?" && m.helpAvailable() && !m.wizardTextInput() {
		// "?" is a typed character in a free-text answer; only a step with a
		// fixed choice list treats it as the help key.
		m.openHelp()
		return nil
	}
	switch m.phase {
	case phaseWizardStep:
		if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
			return nil
		}
		step := m.wizardSpec.Steps[m.wizardStep]
		if step.List {
			m.updateWizardList(msg, step)
			return nil
		}
		if len(step.Options) > 0 {
			switch msg.String() {
			case "up", "k":
				m.wizardChoice = (m.wizardChoice - 1 + len(step.Options)) % len(step.Options)
				m.followWizardCursor()
			case "down", "j":
				m.wizardChoice = (m.wizardChoice + 1) % len(step.Options)
				m.followWizardCursor()
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
				// The guidance belongs to this field: the reader must see
				// which answer is missing, not a detached notice.
				m.wizardNotice = msgWizardNeedValue
				m.notice = ""
				return nil
			}
			m.wizardAnswers[step.Name] = value
			m.advanceWizard()
		case "left":
			if m.wizardInput == "" {
				m.previousWizardStep()
			}
		case "backspace":
			m.wizardNotice = ""
			runes := []rune(m.wizardInput)
			if len(runes) > 0 {
				m.wizardInput = string(runes[:len(runes)-1])
			} else {
				m.previousWizardStep()
			}
		default:
			if len(msg.Runes) > 0 {
				m.wizardNotice = ""
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

func (m *Model) updateWizardList(msg tea.KeyMsg, step core.WizardStep) {
	switch msg.String() {
	case "up":
		if !m.wizardEditing && m.wizardChoice > 0 {
			m.wizardChoice--
			m.followWizardCursor()
		}
	case "down":
		if !m.wizardEditing && m.wizardChoice < len(m.wizardItems) {
			m.wizardChoice++
			m.followWizardCursor()
		}
	case "left":
		if !m.wizardEditing {
			m.wizardAnswers[step.Name] = strings.Join(m.wizardItems, "\n")
			m.previousWizardStep()
		}
	case "enter":
		value := strings.TrimSpace(m.wizardInput)
		if m.wizardEditing {
			if value == "" {
				m.wizardNotice = msgWizardNeedValue
				return
			}
			m.wizardItems[m.wizardChoice] = value
			m.wizardEditing = false
			m.wizardInput = ""
		} else if m.wizardChoice < len(m.wizardItems) {
			m.wizardEditing = true
			m.wizardInput = m.wizardItems[m.wizardChoice]
		} else if value != "" {
			m.wizardItems = append(m.wizardItems, value)
			m.wizardChoice = len(m.wizardItems)
			m.wizardInput = ""
		} else if len(m.wizardItems) == 0 && !step.Optional {
			m.wizardNotice = msgWizardNeedValue
			return
		} else {
			m.wizardAnswers[step.Name] = strings.Join(m.wizardItems, "\n")
			m.advanceWizard()
		}
		m.wizardNotice = ""
		m.followWizardCursor()
	case "backspace", "delete":
		m.wizardNotice = ""
		if m.wizardEditing || (m.wizardChoice == len(m.wizardItems) && m.wizardInput != "") {
			runes := []rune(m.wizardInput)
			if len(runes) > 0 {
				m.wizardInput = string(runes[:len(runes)-1])
			}
		} else if len(m.wizardItems) > 0 {
			i := m.wizardChoice
			if i == len(m.wizardItems) {
				i--
			}
			m.wizardItems = append(m.wizardItems[:i], m.wizardItems[i+1:]...)
			if m.wizardChoice > len(m.wizardItems) {
				m.wizardChoice = len(m.wizardItems)
			}
			m.followWizardCursor()
		}
	default:
		if len(msg.Runes) > 0 && (m.wizardEditing || m.wizardChoice == len(m.wizardItems)) {
			m.wizardInput += string(msg.Runes)
			m.wizardNotice = ""
		}
	}
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
	// The preview starts at its top: a scroll position from the option list
	// would hide the first applied value.
	m.body = visual.Viewport{}
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
	m.stages = nil
	m.planStages = nil
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
		if m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
			return b.String()
		}
		screen := m.wizardStepScreen(m.wizardSpec.Steps[m.wizardStep])
		b.WriteString(strings.Join(screen.lines(), "\n"))
		b.WriteString("\n")
	case phaseWizardPreview:
		// The preview is the applied-values panel. Approvals are listed inside
		// it and the keys live in the footer, so nothing is stated twice.
		b.WriteString(strings.Join(m.wizardPreviewPanel(), "\n"))
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
