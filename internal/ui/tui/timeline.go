package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// defaultSpinnerInterval is one animation frame of the working screen. It is a
// model option rather than a constant at the call site so tests can drive the
// animation without a wall clock.
const defaultSpinnerInterval = 120 * time.Millisecond

// spinnerTickMsg advances the spinner one frame. It is a private message: only
// the model's own tick command produces it, so no key or feature can inject a
// frame.
type spinnerTickMsg struct{}

// spinnerTickCmd schedules the next frame. The returned command is a tea.Tick,
// so Bubble Tea's event loop owns the cadence and a blocked Run call cannot
// freeze the screen.
func spinnerTickCmd(every time.Duration) tea.Cmd {
	return tea.Tick(every, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// animated reports whether the current screen shows a running operation. Live
// screens render process output instead of a timeline, so they do not animate.
func (m *Model) animated() bool {
	if m.live != nil {
		return false
	}
	return m.phase == phaseLoading || m.phase == phasePlanning
}

// spinnerCmd starts the animation for a run or plan that is about to begin.
func (m *Model) spinnerCmd() tea.Cmd {
	if !m.animated() {
		return nil
	}
	return spinnerTickCmd(m.spinnerInterval)
}

// spinOneFrame advances the spinner and returns the command for the next frame.
// It stops on its own once the phase is no longer animated, so a finished run
// leaves no timer behind.
func (m *Model) spinOneFrame() tea.Cmd {
	if !m.animated() {
		return nil
	}
	n := len(m.theme.Glyphs.Spin)
	if n > 0 {
		m.spinnerFrame = (m.spinnerFrame + 1) % n
	}
	return spinnerTickCmd(m.spinnerInterval)
}

// setPlan replaces the sequence the current phase is expected to pass through.
// Callers pass the engine's own planned stages, so the timeline never invents a
// stage the operation will not reach.
func (m *Model) setPlan(stages []core.Stage) {
	m.planStages = append([]core.Stage{}, stages...)
}

// observeStage records one stage snapshot. A stage sends a started snapshot and
// then a terminal one under the same name; the later snapshot replaces the
// earlier row so the timeline never shows the same stage twice. A snapshot never
// runs backwards: a late "started" event must not turn a completed stage back
// into a running one.
func (m *Model) observeStage(st core.Stage) {
	for i := range m.stages {
		if m.stages[i].Name != st.Name {
			continue
		}
		if m.stages[i].Status != core.StageStarted && st.Status == core.StageStarted {
			return
		}
		m.stages[i] = st
		return
	}
	m.stages = append(m.stages, st)
}

// timelineRow is one line of the stage timeline. An empty Status means the
// stage is planned but has not been observed.
type timelineRow struct {
	stage   core.Stage
	current bool
}

// buildTimeline merges what the engine reported with the sequence it will still
// pass through. The plan is the backbone, so rows always follow the engine's
// order even when a snapshot arrives late; an observed row replaces its planned
// entry and an unobserved entry stays pending.
//
// Two rules keep the order honest:
//   - A planned stage that comes before the last observed one but never
//     reported did not run (a hook stage with no scripts, for example). It is
//     dropped rather than left pending under real progress.
//   - A feature that publishes its own stages replaces the single shared
//     execute/plan placeholder, so no duplicate row appears.
func buildTimeline(observed, plan []core.Stage) []timelineRow {
	planIndex := make(map[string]int, len(plan))
	for i, st := range plan {
		planIndex[st.Name] = i
	}
	// observedByPlan groups observed snapshots by the plan entry they replace.
	// loose holds the snapshots the plan does not name, keyed by the plan
	// position they should be drawn before.
	observedByPlan := make(map[int][]core.Stage, len(observed))
	loose := make(map[int][]core.Stage, len(observed))
	lastObserved := -1
	anchor := 0
	hasOwn := false
	for _, st := range observed {
		if idx, ok := planIndex[st.Name]; ok {
			observedByPlan[idx] = append(observedByPlan[idx], st)
			lastObserved = idx
			anchor = idx + 1
			continue
		}
		loose[anchor] = append(loose[anchor], st)
		if !core.IsSharedStage(st.Name) {
			hasOwn = true
		}
	}

	rows := make([]timelineRow, 0, len(plan)+len(observed))
	appendObserved := func(stages []core.Stage) {
		for _, st := range stages {
			rows = append(rows, timelineRow{stage: st, current: st.Status == core.StageStarted})
		}
	}
	for idx, st := range plan {
		appendObserved(loose[idx])
		if obs := observedByPlan[idx]; len(obs) > 0 {
			appendObserved(obs)
			continue
		}
		if idx <= lastObserved {
			continue
		}
		if st.Status == "" && isPlaceholder(st.Name) && hasOwn {
			continue
		}
		rows = append(rows, timelineRow{stage: st})
	}
	appendObserved(loose[len(plan)])
	return rows
}

// isPlaceholder reports the one shared stage a feature may replace with its own
// progress. The engine emits it only when the feature published no stages.
func isPlaceholder(name string) bool {
	return name == core.StageNameExecute || name == core.StageNamePlan
}

// timelineView renders the stage timeline for the shell body. Compact screens
// keep the current stage and its immediate neighbours; every other mode lists
// every stage. The omitted rows are stated explicitly, never silently cut.
func (m *Model) timelineView(observed, plan []core.Stage) []string {
	rows, start, end := m.timelineWindow(observed, plan)
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, end-start+2)
	if start > 0 {
		out = append(out, m.hintText(fmt.Sprintf(msgMenuMoreAbove, m.theme.Glyphs.Ell)))
	}
	for _, row := range rows[start:end] {
		out = append(out, m.timelineRowLines(row)...)
	}
	if end < len(rows) {
		out = append(out, m.hintText(fmt.Sprintf(msgMenuMoreBelow, m.theme.Glyphs.Ell)))
	}
	return out
}

// timelineWindow applies the compact rule and returns the full row list with
// the visible slice. Both the renderer and the spinner decision use it, so the
// animation state and the drawn rows can never disagree.
func (m *Model) timelineWindow(observed, plan []core.Stage) ([]timelineRow, int, int) {
	rows := buildTimeline(observed, plan)
	start, end := 0, len(rows)
	if m.compact() && len(rows) > 3 {
		cur := timelineFocus(rows)
		start = cur - 1
		if start < 0 {
			start = 0
		}
		end = start + 3
		if end > len(rows) {
			end = len(rows)
			start = end - 3
		}
	}
	return rows, start, end
}

// timelineFocus is the row a compact window centres on: the stage that is
// running, else the first stage still ahead, else the last observed one.
func timelineFocus(rows []timelineRow) int {
	for i, row := range rows {
		if row.current {
			return i
		}
	}
	for i, row := range rows {
		if row.stage.Status == "" {
			return i
		}
	}
	return len(rows) - 1
}

// timelineRowLine renders one row as `glyph title (Korean status)`. The status
// word comes from the shared view wording, and a pending stage says 예정, so the
// reader never has to infer state from a symbol or a color.
func (m *Model) timelineRowLine(row timelineRow) string {
	title := row.stage.Title
	if title == "" {
		title = row.stage.Name
	}
	label := msgStagePending
	if row.stage.Status != "" {
		label = view.StageStatusLabel(row.stage.Status)
	}
	glyph, tok := m.timelineGlyph(row)
	return "  " + m.theme.Paint(tok, glyph) + " " + title + " " + m.hintText("("+label+")")
}

// timelineGlyph is the themed mark for one row plus the semantic color it
// carries. Color is the third channel: the glyph and the label already say it.
func (m *Model) timelineGlyph(row timelineRow) (string, visual.Token) {
	switch row.stage.Status {
	case "":
		return m.theme.Glyphs.Dot, visual.TokenMuted
	case core.StageSucceeded:
		return m.theme.Glyphs.OK, visual.TokenSuccess
	case core.StageFailed:
		return m.theme.Glyphs.Danger, visual.TokenDanger
	case core.StageCanceled:
		return m.theme.Glyphs.Stopped, visual.TokenMuted
	case core.StageStarted:
		return m.theme.Glyphs.SpinnerFrame(m.spinnerFrame), visual.TokenAccent
	default:
		return m.theme.Glyphs.Dot, visual.TokenMuted
	}
}

// workingBody is the working screen: the heading and the stage timeline. The
// spinner sits on the running stage; when no visible stage is running it sits on
// the heading, so the screen never looks frozen.
func (m *Model) workingBody(heading string) string {
	rows, start, end := m.timelineWindow(m.stages, m.planStages)
	visible := rows[start:end]
	spinning := false
	for _, row := range visible {
		if row.current {
			spinning = true
			break
		}
	}
	if !spinning {
		heading = m.theme.Paint(visual.TokenAccent, m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)) + " " + heading
	}
	if len(visible) == 0 {
		return heading
	}
	return heading + "\n" + strings.Join(m.timelineView(m.stages, m.planStages), "\n")
}

// timelineBlock renders the timeline as a titled section for screens that show
// other content above it (the confirmation screen).
func (m *Model) timelineBlock(observed, plan []core.Stage) string {
	lines := m.timelineView(observed, plan)
	if len(lines) == 0 {
		return ""
	}
	return m.sectionLabel(msgStageTimeline) + "\n" + strings.Join(lines, "\n")
}

// runPending narrows the shared run sequence to what the plan did not already
// prove will be skipped. Host-script stages appear only when the plan found
// entries for them, because a hook stage with no scripts never reports and a
// confirmed plan already knows which stages have scripts.
func runPending(planned []core.Stage, report core.PlanReport) []core.Stage {
	pre, post := false, false
	for _, entry := range report.Hooks {
		switch entry.Stage {
		case core.HookStagePre:
			pre = true
		case core.HookStagePost:
			post = true
		}
	}
	out := make([]core.Stage, 0, len(planned))
	for _, st := range planned {
		switch st.Name {
		case core.StageNameHooksPre, core.StageNameRevalidate:
			if !pre {
				continue
			}
		case core.StageNameHooksPost:
			if !post {
				continue
			}
		}
		out = append(out, st)
	}
	return out
}
