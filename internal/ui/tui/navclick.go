package tui

import tea "github.com/charmbracelet/bubbletea"

// A sidebar click navigates in every screen, not only in the menu. What the
// click costs depends on the screen, and that decision is stated once here: a
// screen holding input asks before discarding it, a running mutating operation
// keeps the reader where the progress is, and every other screen navigates on
// the spot.

// navClickOutcome is what a sidebar click does on the current screen.
type navClickOutcome int

const (
	// navClickNavigate moves to the chosen feature immediately.
	navClickNavigate navClickOutcome = iota
	// navClickConfirm asks before discarding collected input.
	navClickConfirm
	// navClickBlocked reports that the current operation cannot be left.
	navClickBlocked
)

// navClickDecision reads the phase. The screens that hold answers are the
// wizard and the form; an in-flight mutating run holds progress that dcraft has
// no daemon to resume, so it is the one blocked case.
func (m *Model) navClickDecision() navClickOutcome {
	switch m.phase {
	case phaseWizardStep, phaseWizardPreview, phaseForm:
		if m.screenHasInput() {
			return navClickConfirm
		}
		return navClickNavigate
	case phaseLoading, phasePlanning:
		if m.active != nil && !m.active.Descriptor().ReadOnly {
			return navClickBlocked
		}
		return navClickNavigate
	case phaseConfirm:
		// The panel owns the decision; a click behind it would approve or
		// decline by accident. The panel has its own buttons.
		return navClickBlocked
	default:
		return navClickNavigate
	}
}

// screenHasInput reports whether the current screen holds answers that would be
// discarded by navigating away.
func (m *Model) screenHasInput() bool {
	switch m.phase {
	case phaseForm:
		return m.form != nil && m.form.hasInput()
	case phaseWizardStep:
		if m.wizardEditing || m.wizardInput != "" {
			return true
		}
		for _, value := range m.wizardAnswers {
			if value != "" {
				return true
			}
		}
		return false
	case phaseWizardPreview:
		return len(m.wizardAnswers) > 0
	default:
		return false
	}
}

// hasInput reports whether any field has a typed or chosen answer.
func (f *formState) hasInput() bool {
	if f == nil {
		return false
	}
	for _, value := range f.values {
		if value != "" {
			return true
		}
	}
	for key := range f.choices {
		if f.choices[key] != 0 {
			return true
		}
	}
	return false
}

// activateNav applies one sidebar click. A blocked screen does not move the
// cursor either: the cursor marks what a second click would run, and a blocked
// click runs nothing. Otherwise the first click moves the cursor so the reader
// sees what the second click would act on.
func (m *Model) activateNav(index int, double bool) tea.Cmd {
	if m.navClickDecision() == navClickBlocked {
		if double {
			m.notice = msgNavBlocked
		}
		return nil
	}
	m.selected = index
	if !double {
		return nil
	}
	if m.navClickDecision() == navClickConfirm {
		m.pendingNav = index
		m.discardOpen = true
		m.confirmScroll = 0
		return nil
	}
	return m.start(m.features[index])
}
