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
	if m.live != nil && m.live.mode == core.LiveAttach {
		// Attach ends with ctrl+], never with ctrl+c (which is forwarded to
		// the child), so the live row would be wrong.
		return []visual.Keycap{
			{Key: "ctrl+]", Label: "접속 종료", Priority: 0},
			{Key: "?", Label: "도움말", Priority: 3},
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

// bodyWidth is the width the body renderers may use. The two-pane sidebar is
// T3's; until it exists the body owns the whole width, so ContentWidth is
// called with the single-column mode. The seam stays so T3 changes one call,
// not every renderer.
func (m *Model) bodyWidth() int {
	if m.width <= 0 {
		return 0
	}
	return visual.ContentWidth(m.width, visual.ModeMedium)
}

// layoutMode is the breakpoint decision for the current terminal size. T3
// consumes it to show or collapse the sidebar; T2 uses it for compact-mode
// decoration decisions and as the seam T3 switches the body width to.
func (m *Model) layoutMode() visual.Mode {
	return visual.PickMode(m.width, m.height)
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

	title := m.screenTitle()
	if title != "" {
		add(m.title(title))
		add("")
	}

	add(m.phaseBody())

	if len(m.features) > 1 && m.phase == phaseReady {
		add("")
		add(m.sectionLabel(msgMenu))
		add(m.menuView())
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
		var b strings.Builder
		b.WriteString(msgPlanning)
		if m.currentStage != nil {
			b.WriteString("\n")
			b.WriteString(m.stageLine())
		}
		return b.String()
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
		var b strings.Builder
		b.WriteString(msgWorking)
		if m.currentStage != nil {
			b.WriteString("\n")
			b.WriteString(m.stageLine())
		}
		return b.String()
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
			b.WriteString(view.Failure(*m.result))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stageLine renders the current stage as `glyph title (Korean status)`. The
// wording is view.StageStatusLabel's, so it matches the CLI exactly; only the
// glyph comes from the injected theme, which is what makes a non-UTF-8 locale
// fall back to ASCII. The full timeline is T6.
func (m *Model) stageLine() string {
	st := *m.currentStage
	title := st.Title
	if title == "" {
		title = st.Name
	}
	return "  " + stageGlyph(m.theme, st.Status) + " " + title + " (" + view.StageStatusLabel(st.Status) + ")"
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
