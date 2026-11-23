package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// overlayOpen reports whether a panel is covering the body. There are exactly
// two overlays and one approval surface: the confirmation panel (used by every
// approval and by the wizard's apply) and the help panel.
func (m *Model) overlayOpen() bool {
	return m.helpOpen || m.confirmOpen()
}

// confirmOpen reports whether the confirmation panel owns the keyboard.
func (m *Model) confirmOpen() bool {
	return m.phase == phaseConfirm && (m.pending != nil || m.pendingApproval != core.ApprovalNone)
}

// backgroundModel is what the body shows under a panel: the screen the user was
// on, without the panel's own subject taking it over. The confirmation phase is
// the only state that is not a screen of its own, so the background is the last
// result plus the menu — the same body the user saw before choosing the feature.
func (m *Model) backgroundModel() *Model {
	if !m.overlayOpen() {
		return m
	}
	c := *m
	c.helpOpen = false
	if m.confirmOpen() {
		// The confirmation phase has no screen of its own: the body under
		// the panel is the last result plus the menu.
		c.phase = phaseReady
		c.pending = nil
		c.pendingApprovals = nil
		c.notice = ""
	}
	return &c
}

// overlaySpec is the panel drawn over the dimmed body.
func (m *Model) overlaySpec() (visual.ModalSpec, bool) {
	if m.helpOpen {
		return m.helpSpec(), true
	}
	if m.confirmOpen() {
		return m.confirmSpec(), true
	}
	return visual.ModalSpec{}, false
}

// confirmFeature is the operation the panel describes: the ordinary
// confirmation target, or the active feature of an approval request.
func (m *Model) confirmFeature() core.Feature {
	if m.pending != nil {
		return m.pending
	}
	return m.active
}

// confirmSpec is the confirmation panel content: the nine approved fields in
// their fixed order, the independent approvals, the plan preview, and the risk
// statement that is pinned outside the scrolling band. The field list never
// shrinks, so a narrow terminal scrolls instead of dropping a field.
func (m *Model) confirmSpec() visual.ModalSpec {
	spec := visual.ModalSpec{
		Title:     msgConfirmTitle,
		Badge:     msgConfirmRiskLabel,
		RiskLabel: msgConfirmRiskLabel,
		Risk:      []string{msgConfirmRisk},
		Buttons:   []string{msgModalApprove, msgModalCancel},
		ScrollTop: m.confirmScroll,
	}
	if len(m.pendingApprovals) > 0 {
		spec.Title = msgApprovalConfirmTitle
	}
	feature := m.confirmFeature()
	action := ""
	effect := ""
	if feature != nil {
		d := feature.Descriptor()
		action = d.Title + " (" + d.Name + ")"
		effect = d.Summary
	}
	target := m.target.Path
	if target == "" {
		target = msgConfirmTargetNow
	}
	project := m.target.ProjectName
	if project == "" {
		project = msgConfirmProjectAuto
	}
	// The approved order. A value the model does not know is stated with the
	// neutral "-": deleting the field would make one screen incomparable with
	// the next.
	spec.Rows = []visual.ModalRow{
		{Label: msgConfirmActionLabel + ":", Value: action},
		{Label: msgConfirmTargetLabel + ":", Value: target},
		{Label: msgConfirmEnvironment + ":", Value: orDash(m.target.Environment)},
		{Label: msgConfirmFileLabel + ":", Value: orDash(strings.Join(m.target.Files, ", "))},
		{Label: msgConfirmProfile + ":", Value: orDash(strings.Join(m.target.Profiles, ", "))},
		{Label: msgConfirmProject + ":", Value: project},
		{Label: msgConfirmScopeLabel + ":", Value: serviceScopeText(m.req.Services)},
		{Label: msgConfirmEffectLabel + ":", Value: effect, Wrap: true},
	}
	// The plan and the stage timeline are pre-formatted blocks; the panel
	// separates the block from the fields and keeps their own blank lines.
	if text := strings.TrimSpace(m.planText); text != "" {
		spec.Extra = append(spec.Extra, strings.Split(text, "\n")...)
	}
	if block := m.timelineBlock(m.stages, m.planStages); block != "" {
		if len(spec.Extra) > 0 {
			spec.Extra = append(spec.Extra, "")
		}
		spec.Extra = append(spec.Extra, strings.Split(block, "\n")...)
	}
	if len(m.pendingApprovals) > 0 {
		spec.Section = msgApprovalsSection
		for _, kind := range m.pendingApprovals {
			spec.Items = append(spec.Items, "[x] "+kind.KoreanLabel()+" ("+string(kind)+")")
		}
	}
	return spec
}

// orDash states a known-empty value without inventing new wording.
func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

// helpSpec lists the keys of the screen the help was opened from. It reads the
// same keycap row the footer draws, so the help can never name a key that the
// footer does not and vice versa.
func (m *Model) helpSpec() visual.ModalSpec {
	spec := visual.ModalSpec{Title: msgHelpTitle, ScrollTop: m.confirmScroll}
	// Only the screen's own keys are listed. The way out of the overlay is a
	// keycap, and keycaps live in the footer: repeating `[esc] 닫기` inside the
	// panel would be a second footer.
	for _, item := range m.screenKeycaps() {
		if item.Key == "?" {
			continue
		}
		spec.Items = append(spec.Items, item.Text())
	}
	return spec
}

// openHelp shows the keymap of the current screen. It runs nothing.
func (m *Model) openHelp() {
	if !m.helpAvailable() {
		return
	}
	m.helpOpen = true
	m.confirmScroll = 0
}

// helpAvailable reports whether the current screen advertises help. A screen
// whose "?" is text (a filter field or a wizard answer) does not.
func (m *Model) helpAvailable() bool {
	for _, item := range m.screenKeycaps() {
		if item.Key == "?" {
			return true
		}
	}
	return false
}

// updateHelpKey drives the help panel. esc closes it; nothing else runs.
func (m *Model) updateHelpKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "?":
		m.helpOpen = false
		m.confirmScroll = 0
		return nil
	case "up", "k":
		m.scrollOverlay(-1)
	case "down", "j":
		m.scrollOverlay(1)
	case "pgup":
		m.scrollOverlay(-m.overlayPage())
	case "pgdown":
		m.scrollOverlay(m.overlayPage())
	case "home", "g":
		m.confirmScroll = 0
	case "end", "G":
		m.confirmScroll = m.overlayMaxTop()
	}
	return nil
}

// updateConfirmKey drives the confirmation panel. enter approves and runs; esc
// declines and runs nothing. Approving stays a single key on purpose: one
// deliberate accept keeps the approval path unambiguous. Arrows and page keys
// scroll the panel body.
func (m *Model) updateConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		// A confirmation is not an operation; quitting is safe and runs
		// nothing.
		return m, tea.Quit
	case "esc", "n":
		if m.pendingApproval != core.ApprovalNone {
			// An approval request is declined explicitly: the canceled
			// result states that nothing ran, and it is the screen the user
			// returns to.
			m.declineApproval()
			return m, nil
		}
		// An ordinary confirmation has no approval to decline. Closing it
		// returns to the screen the user came from, unchanged: nothing ran,
		// so there is nothing new to report.
		m.pending = nil
		m.planText = ""
		m.planFingerprint = ""
		m.req.PlanFingerprint = ""
		m.confirmScroll = 0
		m.phase = phaseReady
		m.notice = ""
		return m, nil
	case "enter":
		return m.approveConfirmation()
	case "up", "k":
		m.scrollOverlay(-1)
		return m, nil
	case "down", "j":
		m.scrollOverlay(1)
		return m, nil
	case "pgup":
		m.scrollOverlay(-m.overlayPage())
		return m, nil
	case "pgdown":
		m.scrollOverlay(m.overlayPage())
		return m, nil
	case "home", "g":
		m.confirmScroll = 0
		return m, nil
	case "end", "G":
		m.confirmScroll = m.overlayMaxTop()
		return m, nil
	}
	return m, nil
}

// approveConfirmation is the single accept path for both the ordinary
// confirmation and an approval request. The engine still decides whether the
// collected approvals are enough.
func (m *Model) approveConfirmation() (tea.Model, tea.Cmd) {
	if m.pendingApproval != core.ApprovalNone {
		feature := m.active
		approvals := append([]core.ApprovalKind{}, m.pendingApprovals...)
		m.pendingApproval = ""
		m.pendingApprovals = nil
		m.planText = ""
		m.confirmScroll = 0
		if feature == nil {
			m.phase = phaseReady
			return m, nil
		}
		return m, m.runApproved(feature, approvals)
	}
	if m.pending == nil {
		m.phase = phaseReady
		return m, nil
	}
	f := m.pending
	m.pending = nil
	m.confirmScroll = 0
	return m, m.startRun(f, nil)
}

// scrollOverlay moves the open panel's body by delta rows, clamped to the rows
// that exist.
func (m *Model) scrollOverlay(delta int) {
	max := m.overlayMaxTop()
	m.confirmScroll += delta
	if m.confirmScroll < 0 {
		m.confirmScroll = 0
	}
	if m.confirmScroll > max {
		m.confirmScroll = max
	}
}

// overlayPage is one visible page of the open panel.
func (m *Model) overlayPage() int {
	visible, _ := m.overlayMetrics()
	if visible < 1 {
		return 1
	}
	return visible
}

// overlayMaxTop is the largest scroll position the panel's content allows.
func (m *Model) overlayMaxTop() int {
	_, max := m.overlayMetrics()
	return max
}

// overlayMetrics reports the drawn body rows and the last legal scroll
// position of the open panel, from the same geometry the renderer uses.
func (m *Model) overlayMetrics() (visible, maxTop int) {
	spec, ok := m.overlaySpec()
	if !ok {
		return 0, 0
	}
	return visual.ModalMetrics(m.theme, m.width, m.height, spec)
}
