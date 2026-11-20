package tui

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// footerPhase maps the model's current interaction phase onto the approved
// keycap row. The five contract phases are used exactly as approved; two
// screens need keys the contract rows do not cover, so they supply their own
// rows (see footerItems): the service toggle list, and the wizard.
func (m *Model) footerPhase() visual.Phase {
	if m.form != nil {
		return visual.PhaseForm
	}
	if m.live != nil {
		return visual.PhaseLive
	}
	switch m.phase {
	case phaseConfirm:
		return visual.PhaseConfirm
	case phaseSelectingServices, phaseWizardLoading, phaseWizardStep, phaseWizardPreview, phaseLoadingInputs:
		return visual.PhaseForm
	case phaseLoading, phasePlanning:
		if m.active != nil && !m.active.Descriptor().ReadOnly {
			return visual.PhaseBusy
		}
	}
	return visual.PhaseMenu
}

// footerItems is the exact keycap row for the current screen. The approved
// phase rows apply verbatim except where a screen's keys are a strict superset
// that no approved row covers; those rows keep the approved chips and add only
// the keys that screen actually accepts.
func (m *Model) footerItems() []visual.Keycap {
	if m.filtering {
		// While "/" has the keyboard the only two meanings are clear and
		// commit; q, r and the arrows are characters or menu moves here.
		return []visual.Keycap{
			{Key: "esc", Label: "해제", Priority: 0},
			{Key: "enter", Label: "확정", Priority: 0},
		}
	}
	if m.live != nil && m.live.mode == core.LiveAttach {
		// Attach ends with ctrl+], never with ctrl+c (which is forwarded to
		// the child), so the live row would be wrong.
		return []visual.Keycap{
			{Key: "ctrl+]", Label: "접속 종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
		}
	}
	if m.live == nil && (m.phase == phaseLoading || m.phase == phasePlanning) &&
		m.active != nil && m.active.Descriptor().ReadOnly {
		// A read-only operation in flight (not a live screen) accepts only
		// quit; the menu keys do not work until the result is ready, so the
		// row states just that. A mutating run returns PhaseBusy instead.
		return []visual.Keycap{
			{Key: "q", Label: "종료", Priority: 0},
		}
	}
	if m.phase == phaseSelectingServices {
		return []visual.Keycap{
			{Key: "↑↓", Label: "이동", Priority: 1},
			{Key: "space", Label: "선택/해제", Priority: 1},
			{Key: "enter", Label: "실행", Priority: 0},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
		}
	}
	if m.isWizardPhase() {
		// The wizard's keys are not the form's: it moves with arrows and goes
		// back with left, and tab does nothing. Advertising the form row here
		// would name a key that has no effect.
		if m.phase == phaseWizardPreview {
			if m.wizardPreviewErr != nil {
				// Enter does nothing while the preview is invalid, so the row
				// must not offer it.
				return []visual.Keycap{
					{Key: "left", Label: "이전", Priority: 0},
					{Key: "esc", Label: "취소", Priority: 0},
					{Key: "q", Label: "종료", Priority: 0},
				}
			}
			return []visual.Keycap{
				{Key: "enter", Label: "저장", Priority: 0},
				{Key: "left", Label: "이전", Priority: 2},
				{Key: "esc", Label: "취소", Priority: 0},
				{Key: "q", Label: "종료", Priority: 0},
			}
		}
		return []visual.Keycap{
			{Key: "↑↓", Label: "선택", Priority: 1},
			{Key: "enter", Label: "다음", Priority: 0},
			{Key: "left", Label: "이전", Priority: 2},
			{Key: "esc", Label: "취소", Priority: 0},
			{Key: "q", Label: "종료", Priority: 0},
		}
	}
	return visual.FooterItems(m.footerPhase())
}

// bodyWidth is the width the body renderers may use. In the wide layout the
// sidebar owns its cells and the approved one-cell gap; every other layout
// gives the body the whole width. The budget follows the pane that is actually
// drawn, so a hidden menu never costs the body its width.
func (m *Model) bodyWidth() int {
	if m.width <= 0 {
		return 0
	}
	if m.sidebarVisible() {
		return visual.ContentWidth(m.width, visual.ModeWide)
	}
	return visual.ContentWidth(m.width, visual.ModeMedium)
}

// layoutMode is the breakpoint decision for the current terminal size.
func (m *Model) layoutMode() visual.Mode {
	return visual.PickMode(m.width, m.height)
}

// sidebarVisible reports whether the persistent left pane is drawn. A menu of
// one feature is not worth a pane, and the body then keeps its full width.
func (m *Model) sidebarVisible() bool {
	return m.width > 0 && m.layoutMode() == visual.ModeWide && len(m.features) > 1
}

// compact reports whether decoration must be dropped. Before the first
// WindowSizeMsg the size is unknown, so the screen renders its full content
// rather than guessing that it is small.
func (m *Model) compact() bool {
	return m.width > 0 && m.layoutMode() == visual.ModeCompact
}

// renderBody builds every body line: the screen title, the phase content, the
// feature menu and the notice. It never draws the header or the keycap footer;
// those belong to the shell.
func (m *Model) renderBody() []string {
	var lines []string
	add := func(s string) {
		s = strings.TrimRight(s, "\n")
		if s == "" {
			return
		}
		lines = append(lines, strings.Split(s, "\n")...)
	}

	// The medium layout states the navigation in exactly one row directly
	// under the header. It is one row collapsed and expanded, so opening the
	// menu never costs the content a line.
	if navLine := m.navLine(); navLine != "" {
		add(navLine)
	}

	title := m.screenTitle()
	if title != "" {
		add(m.title(title))
		add("")
	}

	add(m.phaseBody())

	// The full list is drawn wherever the navigation is not already a list:
	// the wide layout has the persistent sidebar. The medium layout keeps the
	// one-row summary as a headline *and* the list, because a collapsed row
	// that names only one feature would hide the rest of the menu. Before the
	// first WindowSizeMsg the sidebar is not drawn, so the list is.
	if len(m.features) > 1 && m.phase == phaseReady && !m.sidebarVisible() {
		add("")
		add(m.sectionLabel(msgMenu))
		budget := 0
		if m.height > 0 {
			// Everything the body has left after the lines already written.
			budget = visual.BodyRows(m.height) - len(lines)
		}
		add(m.menuView(budget))
	}
	if m.notice != "" {
		add("")
		add(m.noticeText(m.notice))
	}
	// Trim trailing blanks so the shell's overflow note sits right under the
	// last real line.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// screenTitle is the current feature's title. The stack identity lives in the
// header, so the body does not repeat "dcraft".
func (m *Model) screenTitle() string {
	shown := m.active
	if (m.phase == phaseLoadingServices || m.phase == phaseSelectingServices) && m.serviceFeature != nil {
		shown = m.serviceFeature
	}
	if m.phase == phaseConfirm && m.pending != nil {
		shown = m.pending
	}
	if m.live != nil && m.active != nil {
		shown = m.active
	}
	if shown == nil {
		return ""
	}
	return shown.Descriptor().Title
}

// phaseBody renders the content of the current phase. Screen content is
// unchanged by T2; only the surrounding frame is new.
func (m *Model) phaseBody() string {
	switch m.phase {
	case phaseNoPrimary:
		return msgNoPrimary
	case phasePlanning:
		return m.workingBody(msgPlanning)
	case phaseConfirm:
		if m.pendingApproval != core.ApprovalNone {
			return m.planView()
		}
		return m.confirmView()
	case phaseLoadingEnvironments:
		return msgEnvChecking
	case phaseSelectingEnvironment:
		return m.environmentView()
	case phaseLoadingServices:
		return msgServiceLoading
	case phaseSelectingServices:
		return m.serviceView()
	case phaseLoadingInputs:
		return msgLoadingInputs
	case phaseForm:
		return m.formView()
	case phaseWizardLoading, phaseWizardStep, phaseWizardPreview:
		return m.wizardView()
	case phaseLoading:
		if m.live != nil {
			return m.liveView()
		}
		return m.workingBody(msgWorking)
	default:
		return m.resultView()
	}
}

// planView is the approval screen: the generic plan plus the independent
// approval kinds. Field order and wording are unchanged; only styling moved to
// the theme.
func (m *Model) planView() string {
	var b strings.Builder
	b.WriteString(m.title(msgApprovalConfirmTitle))
	b.WriteString("\n")
	if m.active != nil {
		d := m.active.Descriptor()
		b.WriteString("작업: " + d.Title + " (" + d.Name + ")\n")
	}
	b.WriteString(msgConfirmScopeLabel + ": " + serviceScopeText(m.req.Services) + "\n")
	b.WriteString("승인: " + m.pendingApproval.KoreanLabel() + " (" + string(m.pendingApproval) + ")\n")
	for _, kind := range m.pendingApprovals {
		if kind == m.pendingApproval {
			continue
		}
		b.WriteString("승인: " + kind.KoreanLabel() + " (" + string(kind) + ")\n")
	}
	b.WriteString("\n")
	b.WriteString(m.planText)
	if block := m.timelineBlock(m.stages, m.planStages); block != "" {
		b.WriteString("\n\n")
		b.WriteString(block)
	}
	return strings.TrimRight(b.String(), "\n")
}

// environmentView lists named environments; nothing is preselected.
func (m *Model) environmentView() string {
	var b strings.Builder
	b.WriteString(m.title(msgEnvSelectTitle))
	b.WriteString("\n")
	b.WriteString(msgEnvSelectIntro)
	b.WriteString("\n")
	for i, env := range m.envs {
		marker := "  "
		if i == m.envSelected {
			marker = "> "
		}
		b.WriteString(marker + env.Name + " — " + environmentSummary(env) + "\n")
	}
	b.WriteString(m.hintText(msgEnvIsolationNote))
	b.WriteString("\n")
	return b.String()
}

// resultView renders the last result, the kept live tail and warnings.
func (m *Model) resultView() string {
	var b strings.Builder
	if len(m.lastLive) > 0 {
		b.WriteString(m.title(msgLastOutput))
		b.WriteString("\n")
		for _, line := range m.lastLive {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if m.result != nil {
		if m.result.Outcome == core.OutcomeSuccess {
			if p, ok := m.active.(core.Presenter); ok {
				b.WriteString(p.Present(*m.result, m.bodyWidth()))
			} else {
				b.WriteString(view.SuccessFallback(*m.result))
			}
			if warnings := view.Warnings(m.result.Diagnostics); len(warnings) > 0 {
				b.WriteString("\n\n")
				b.WriteString(view.Diagnostics(warnings))
			}
		} else {
			// The outcome, target, operation ID and diagnostics are the shared
			// Korean text the CLI prints; the stage rows below them are the
			// TUI's own timeline, so a failed stage stays next to the stages
			// that never ran.
			b.WriteString(view.FailureText(*m.result))
			if stages := m.timelineView(m.result.Stages, m.planStages); len(stages) > 0 {
				b.WriteString("\n\n")
				b.WriteString(m.sectionLabel(msgStageTimeline))
				b.WriteString("\n")
				b.WriteString(strings.Join(stages, "\n"))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stageGlyph is the themed status symbol for one stage. The Korean label is
// always drawn next to it, so color and icon are never the only channel.
func stageGlyph(th visual.Theme, status core.StageStatus) string {
	switch status {
	case core.StageSucceeded:
		return th.Glyphs.OK
	case core.StageFailed:
		return th.Glyphs.Danger
	case core.StageCanceled:
		return th.Glyphs.Stopped
	case core.StageStarted:
		return th.Glyphs.Spin[0]
	default:
		return th.Glyphs.Dot
	}
}

// Shell-level text styles. Every renderer gets them from the injected theme;
// none reads the environment or a package-level style.

func (m *Model) title(s string) string {
	return m.theme.Bold(visual.TokenAccent, s)
}

func (m *Model) sectionLabel(s string) string {
	return m.theme.Bold(visual.TokenMuted, s)
}

func (m *Model) hintText(s string) string {
	return m.theme.Paint(visual.TokenMuted, s)
}

func (m *Model) noticeText(s string) string {
	return m.theme.Bold(visual.TokenWarning, s)
}

// wrapProse wraps a paragraph to the body width. Prose may wrap; paths and
// identifiers may not. The CLI paths pass width 0, which means "no wrapping".
func (m *Model) wrapProse(text string) string {
	width := m.bodyWidth()
	if width <= 0 || strings.TrimSpace(text) == "" {
		return text
	}
	return strings.Join(visual.Wrap(text, width), "\n")
}
