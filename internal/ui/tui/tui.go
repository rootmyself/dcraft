// Package tui renders the interactive screen. It is assembled entirely from
// the feature registry and invokes features only through the common engine.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// Options are the shared TUI dependencies.
type Options struct {
	Engine   *core.Engine
	Registry *core.Registry
	Target   core.TargetRequest
}

// Run starts the interactive program. Normal quit (q or ctrl+c) does not touch
// services. Mutating features ask for confirmation first; ctrl+c during a
// mutating operation requests safe cancellation and keeps the screen for the
// canceled result.
func Run(ctx context.Context, opts Options) error {
	m := NewModel(ctx, opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

type phase int

const (
	phaseNoPrimary phase = iota
	phaseLoadingEnvironments
	phaseSelectingEnvironment
	phaseLoading
	phasePlanning
	phaseConfirm
	phaseLoadingInputs
	phaseForm
	phaseReady
)

// Model is the Bubble Tea model. It is exported for behavior tests.
type Model struct {
	ctx      context.Context
	engine   *core.Engine
	registry *core.Registry
	target   core.TargetRequest

	features []core.Feature
	primary  core.Feature
	active   core.Feature
	selected int

	envs        []core.Environment
	envSelected int

	phase        phase
	result       *core.Result
	currentStage *core.Stage
	events       chan core.Stage
	pending      core.Feature
	notice       string
	runCtx       context.Context
	runCancel    context.CancelFunc
	width        int
	height       int

	// pendingApproval and planText hold the generic confirmation state. Any
	// approval-gated feature that implements core.Planner reuses this screen;
	// no feature-specific branch is added here.
	pendingApproval core.ApprovalKind
	planText        string

	// req carries the feature input collected from the interactive form. A
	// feature that implements core.InputProvider fills it before running; the
	// CLI never uses this path.
	req core.Request
	// live is non-nil while a feature attaches to a running process. The TUI
	// renders its output and forwards keys for core.LiveAttach.
	live   *liveState
	liveCh chan string
	// lastLive keeps the tail of the last live output so the management view
	// still shows what the process printed after the screen closes.
	lastLive []string
	form     *formState
}

// NewModel builds the initial screen without running anything.
func NewModel(ctx context.Context, opts Options) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &Model{ctx: ctx, engine: opts.Engine, registry: opts.Registry, target: opts.Target, envSelected: -1}
	m.req = core.Request{Target: opts.Target}
	if opts.Registry != nil {
		m.features = opts.Registry.All()
		if primary, ok := opts.Registry.Primary(); ok {
			m.primary = primary
			m.active = primary
		}
	}
	if m.primary == nil {
		m.phase = phaseNoPrimary
		return m
	}
	for i, f := range m.features {
		if f.Descriptor().Name == m.primary.Descriptor().Name {
			m.selected = i
			break
		}
	}
	if opts.Engine != nil && opts.Target.Environment == "" && opts.Engine.SupportsEnvironments() {
		m.phase = phaseLoadingEnvironments
		return m
	}
	m.phase = phaseLoading
	m.events = make(chan core.Stage, 64)
	return m
}

type resultMsg struct{ res core.Result }

type stageMsg struct{ stage core.Stage }

type planMsg struct{ report core.PlanReport }

type inputsMsg struct {
	feature core.Feature
	report  core.InputsReport
}

type liveLineMsg struct{ line string }

type environmentListMsg struct {
	envs []core.Environment
	err  error
}

func (m *Model) Init() tea.Cmd {
	switch m.phase {
	case phaseLoadingEnvironments:
		return m.listEnvironmentsCmd()
	case phaseLoading:
		return m.begin(m.active)
	default:
		return nil
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case stageMsg:
		st := msg.stage
		m.currentStage = &st
		if m.phase == phaseLoading || m.phase == phasePlanning {
			return m, waitStageCmd(m.events)
		}
		return m, nil
	case planMsg:
		m.currentStage = nil
		if msg.report.Failed() {
			res := msg.report.Result()
			m.result = &res
			m.phase = phaseReady
			return m, nil
		}
		m.pendingApproval = m.active.Descriptor().Approval
		m.planText = msg.report.Text
		m.phase = phaseConfirm
		return m, nil
	case environmentListMsg:
		if m.phase != phaseLoadingEnvironments {
			return m, nil
		}
		// A broken catalog is not hidden here: running the primary feature
		// through the engine produces the same resolve_target failure with
		// Korean guidance and no Docker contact.
		if msg.err != nil || len(msg.envs) == 0 {
			return m, m.begin(m.primary)
		}
		m.envs = msg.envs
		m.envSelected = -1
		m.phase = phaseSelectingEnvironment
		return m, nil
	case inputsMsg:
		if m.phase != phaseLoadingInputs {
			return m, nil
		}
		if msg.report.Failed() {
			res := msg.report.Result()
			m.result = &res
			m.phase = phaseReady
			return m, nil
		}
		if len(msg.report.Fields) == 0 {
			return m, m.begin(msg.feature)
		}
		m.form = newFormState(msg.feature, msg.report.Fields, m.req)
		m.phase = phaseForm
		return m, nil
	case liveLineMsg:
		if m.live == nil {
			return m, nil
		}
		m.live.append(msg.line)
		return m, waitLiveCmd(m.liveCh)
	case resultMsg:
		res := msg.res
		m.result = &res
		m.currentStage = nil
		m.phase = phaseReady
		m.notice = ""
		if m.live != nil {
			m.live.writer.flush()
			m.drainLive()
			if res.Outcome == core.OutcomeCanceled {
				m.notice = msgLiveStopped
			}
			m.lastLive = tailOrKeep(lastLines(m.live.writer.tailLines(), 12), m.lastLive)
			m.live.close()
			m.live = nil
			m.liveCh = nil
		}
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
			m.runCtx = nil
		}
		return m, nil
	case tea.KeyMsg:
		if m.form != nil {
			return m.updateFormKey(msg)
		}
		if m.live != nil {
			return m.updateLiveKey(msg)
		}
		if m.phase == phaseConfirm {
			switch msg.String() {
			case "q", "ctrl+c":
				// A confirmation is not an operation; quitting is safe and
				// runs nothing.
				return m, tea.Quit
			case "esc", "n":
				if m.pendingApproval != core.ApprovalNone {
					m.declineApproval()
				} else {
					m.pending = nil
					m.phase = phaseReady
					m.notice = ""
				}
				return m, nil
			case "enter":
				if m.pendingApproval != core.ApprovalNone {
					feature := m.active
					approval := m.pendingApproval
					m.pendingApproval = ""
					m.planText = ""
					if feature == nil {
						m.phase = phaseReady
						return m, nil
					}
					return m, m.runApproved(feature, approval)
				}
				if m.pending == nil {
					m.phase = phaseReady
					return m, nil
				}
				f := m.pending
				m.pending = nil
				return m, m.begin(f)
			}
			return m, nil
		}
		switch msg.String() {
		case "q":
			if m.phase == phaseLoading && m.active != nil && !m.active.Descriptor().ReadOnly {
				// A mutating Compose process is still running. Quitting here
				// would orphan a hidden change; say so instead of hiding it.
				m.notice = msgBusyQuit
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+c":
			if m.phase == phaseLoading && m.active != nil && !m.active.Descriptor().ReadOnly {
				// Safe interruption: stop the external process, keep the
				// screen open so the canceled result and partial effects are
				// reported. A later q quits.
				if m.runCancel != nil {
					m.runCancel()
					m.runCancel = nil
				}
				m.notice = msgCanceling
				return m, nil
			}
			return m, tea.Quit
		}
		if m.phase == phaseSelectingEnvironment {
			switch msg.String() {
			case "up", "k":
				if len(m.envs) > 0 {
					if m.envSelected <= 0 {
						m.envSelected = len(m.envs) - 1
					} else {
						m.envSelected--
					}
				}
			case "down", "j":
				if len(m.envs) > 0 {
					m.envSelected = (m.envSelected + 1) % len(m.envs)
				}
			case "enter":
				if m.envSelected >= 0 && m.envSelected < len(m.envs) && m.primary != nil {
					m.target.Environment = m.envs[m.envSelected].Name
					return m, m.begin(m.primary)
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "r":
			if m.active == nil || m.phase != phaseReady {
				return m, nil
			}
			if m.active.Descriptor().ReadOnly || plannedApproval(m.active) {
				return m, m.start(m.active)
			}
			m.pending = m.active
			m.phase = phaseConfirm
			m.notice = ""
			return m, nil
		case "up", "k":
			if len(m.features) > 1 && m.phase == phaseReady {
				m.selected = (m.selected - 1 + len(m.features)) % len(m.features)
			}
			return m, nil
		case "down", "j":
			if len(m.features) > 1 && m.phase == phaseReady {
				m.selected = (m.selected + 1) % len(m.features)
			}
			return m, nil
		case "enter":
			if m.phase == phaseReady && len(m.features) > 0 {
				f := m.features[m.selected]
				if f.Descriptor().ReadOnly || plannedApproval(f) {
					return m, m.start(f)
				}
				m.pending = f
				m.phase = phaseConfirm
				m.notice = ""
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) listEnvironmentsCmd() tea.Cmd {
	engine := m.engine
	ctx := m.ctx
	req := m.target
	return func() tea.Msg {
		envs, _, err := engine.Environments(ctx, req)
		return environmentListMsg{envs: envs, err: err}
	}
}

// plannedApproval reports whether a feature's independent approval is
// collected through the read-only plan screen. Such a feature needs no extra
// generic confirmation: the plan itself is the confirmation.
func plannedApproval(f core.Feature) bool {
	if f == nil {
		return false
	}
	d := f.Descriptor()
	if d.Approval == core.ApprovalNone {
		return false
	}
	_, ok := f.(core.Planner)
	return ok
}

// start begins a feature from the menu or refresh, collecting interactive
// inputs first when the feature implements core.InputProvider.
func (m *Model) start(f core.Feature) tea.Cmd {
	if f == nil {
		return nil
	}
	if _, ok := f.(core.InputProvider); ok && m.engine != nil {
		m.active = f
		m.result = nil
		m.currentStage = nil
		m.notice = ""
		m.pending = nil
		m.phase = phaseLoadingInputs
		req := m.requestFor(f)
		engine := m.engine
		return func() tea.Msg {
			return inputsMsg{feature: f, report: engine.Inputs(m.ctx, req, nil)}
		}
	}
	return m.begin(f)
}

// begin starts a feature. Approval-gated features that can describe their
// exact effect go through a read-only plan first; everything else runs
// directly, preserving the ticket 01 behavior.
func (m *Model) begin(f core.Feature) tea.Cmd {
	if f == nil {
		return m.startRun(f, nil)
	}
	if m.req.Feature != f.Descriptor().Name {
		m.req = core.Request{Feature: f.Descriptor().Name, Target: m.target}
	}
	return m.startRun(f, nil)
}

// beginRequest runs a feature with inputs collected by the TUI form.
func (m *Model) beginRequest(f core.Feature, req core.Request) tea.Cmd {
	req.Feature = f.Descriptor().Name
	req.Target = m.target
	m.req = req
	return m.startRun(f, nil)
}

func (m *Model) startRun(f core.Feature, approvals []core.ApprovalKind) tea.Cmd {
	m.active = f
	m.result = nil
	m.currentStage = nil
	m.pendingApproval = ""
	m.planText = ""
	m.pending = nil
	m.notice = ""
	m.closeLive()
	m.form = nil
	m.lastLive = nil
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
		m.runCtx = nil
	}
	m.events = make(chan core.Stage, 64)
	m.setupLive(f)
	if f != nil && len(approvals) == 0 {
		desc := f.Descriptor()
		if desc.Approval != core.ApprovalNone {
			if _, ok := f.(core.Planner); ok {
				m.phase = phasePlanning
				return tea.Batch(m.planCmd(f, m.events), waitStageCmd(m.events))
			}
		}
	}
	m.phase = phaseLoading
	m.runCtx, m.runCancel = context.WithCancel(m.ctx)
	cmds := []tea.Cmd{m.runCmd(f, approvals, m.events, m.runCtx), waitStageCmd(m.events)}
	if m.live != nil {
		cmds = append(cmds, waitLiveCmd(m.liveCh))
	}
	return tea.Batch(cmds...)
}

func (m *Model) runApproved(f core.Feature, approval core.ApprovalKind) tea.Cmd {
	return m.startRun(f, []core.ApprovalKind{approval})
}

func (m *Model) declineApproval() {
	action := ""
	if m.active != nil {
		action = m.active.Descriptor().Name
	}
	res := core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        action,
		Outcome:       core.OutcomeCanceled,
		Target:        core.Target{ProjectDir: m.target.Path, Environment: m.target.Environment},
		Stages:        []core.Stage{},
		Diagnostics: []core.Diagnostic{{
			Category:    core.CategoryCanceled,
			Severity:    core.SeverityWarning,
			Message:     msgApprovalDeclined,
			NextActions: []string{msgApprovalDeclinedAction},
		}},
	}
	res.Target.Normalize()
	m.result = &res
	m.pendingApproval = ""
	m.planText = ""
	m.phase = phaseReady
}

func (m *Model) runCmd(f core.Feature, approvals []core.ApprovalKind, events chan core.Stage, ctx context.Context) tea.Cmd {
	engine := m.engine
	req := m.req
	req.Feature = f.Descriptor().Name
	req.Target = m.target
	req.Approvals = approvals
	req.Interactive = true
	req.JSON = false
	return func() tea.Msg {
		if engine == nil {
			close(events)
			return resultMsg{res: core.Result{
				SchemaVersion: core.SchemaVersion,
				Action:        req.Feature,
				Outcome:       core.OutcomeFailure,
				Diagnostics: []core.Diagnostic{{
					Category:    core.CategoryInternal,
					Severity:    core.SeverityError,
					Message:     msgNoEngine,
					NextActions: []string{msgNoEngineAction},
				}},
			}}
		}
		sink := core.SinkFunc(func(st core.Stage) {
			select {
			case events <- st:
			default:
			}
		})
		res := engine.Run(ctx, req, sink)
		close(events)
		return resultMsg{res: res}
	}
}

func (m *Model) planCmd(f core.Feature, events chan core.Stage) tea.Cmd {
	engine := m.engine
	ctx := m.ctx
	req := core.Request{
		Feature:     f.Descriptor().Name,
		Target:      m.target,
		Interactive: true,
	}
	return func() tea.Msg {
		if engine == nil {
			close(events)
			return planMsg{report: core.PlanReport{
				Feature: req.Feature,
				Outcome: core.OutcomeFailure,
				Target:  core.Target{ProjectDir: req.Target.Path, Environment: req.Target.Environment},
				Stages:  []core.Stage{},
				Diagnostics: []core.Diagnostic{{
					Category:    core.CategoryInternal,
					Severity:    core.SeverityError,
					Message:     msgNoEngine,
					NextActions: []string{msgNoEngineAction},
				}},
			}}
		}
		sink := core.SinkFunc(func(st core.Stage) {
			select {
			case events <- st:
			default:
			}
		})
		report := engine.Plan(ctx, req, sink)
		close(events)
		return planMsg{report: report}
	}
}

func waitStageCmd(events chan core.Stage) tea.Cmd {
	return func() tea.Msg {
		st, ok := <-events
		if !ok {
			return nil
		}
		return stageMsg{stage: st}
	}
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	hintStyle   = lipgloss.NewStyle().Faint(true)
	noticeStyle = lipgloss.NewStyle().Bold(true)
)

func (m *Model) View() string {
	var b strings.Builder
	title := msgApp
	shown := m.active
	if m.phase == phaseConfirm && m.pending != nil {
		shown = m.pending
	}
	if shown != nil {
		title += " · " + shown.Descriptor().Title
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	switch m.phase {
	case phaseNoPrimary:
		b.WriteString(msgNoPrimary)
		b.WriteString("\n")
	case phasePlanning:
		b.WriteString(msgPlanning)
		b.WriteString("\n")
		if m.currentStage != nil {
			b.WriteString(view.StageList([]core.Stage{*m.currentStage}))
			b.WriteString("\n")
		}
	case phaseConfirm:
		if m.pendingApproval != core.ApprovalNone {
			b.WriteString(titleStyle.Render(msgApprovalConfirmTitle))
			b.WriteString("\n\n")
			if m.active != nil {
				d := m.active.Descriptor()
				fmt.Fprintf(&b, "작업: %s (%s)\n", d.Title, d.Name)
			}
			fmt.Fprintf(&b, "승인: %s (%s)\n", m.pendingApproval.KoreanLabel(), m.pendingApproval)
			b.WriteString("\n")
			b.WriteString(m.planText)
			b.WriteString("\n\n")
			b.WriteString(hintStyle.Render(msgHintConfirm))
			b.WriteString("\n")
		} else {
			b.WriteString(m.confirmView())
			b.WriteString("\n")
		}
	case phaseLoadingEnvironments:
		b.WriteString(msgEnvChecking)
		b.WriteString("\n")
	case phaseSelectingEnvironment:
		b.WriteString(titleStyle.Render(msgEnvSelectTitle))
		b.WriteString("\n\n")
		b.WriteString(msgEnvSelectIntro)
		b.WriteString("\n\n")
		for i, env := range m.envs {
			marker := "  "
			if i == m.envSelected {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s — %s\n", marker, env.Name, environmentSummary(env))
		}
		b.WriteString("\n")
		b.WriteString(hintStyle.Render(msgEnvIsolationNote))
		b.WriteString("\n")
	case phaseLoadingInputs:
		b.WriteString(msgLoadingInputs)
		b.WriteString("\n")
	case phaseForm:
		b.WriteString(m.formView())
		b.WriteString("\n")
	case phaseLoading:
		if m.live != nil {
			b.WriteString(m.liveView())
			b.WriteString("\n")
			break
		}
		b.WriteString(msgWorking)
		b.WriteString("\n")
		if m.currentStage != nil {
			b.WriteString(view.StageList([]core.Stage{*m.currentStage}))
			b.WriteString("\n")
		}
	default:
		if len(m.lastLive) > 0 {
			b.WriteString(titleStyle.Render(msgLastOutput))
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
					b.WriteString(p.Present(*m.result, m.contentWidth()))
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
	}

	if len(m.features) > 1 && m.phase == phaseReady {
		b.WriteString("\n")
		b.WriteString(msgMenu)
		b.WriteString("\n")
		for i, f := range m.features {
			d := f.Descriptor()
			marker := "  "
			if i == m.selected {
				marker = "> "
			}
			b.WriteString(marker + d.Title + " (" + d.Name + ")\n")
		}
	}

	if m.notice != "" {
		b.WriteString("\n")
		b.WriteString(noticeStyle.Render(m.notice))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(hintStyle.Render(m.hints()))
	return b.String()
}

// confirmView explains the requested target and effect before a mutating
// feature runs. It shows only caller input; the engine resolves and reports
// the Compose project name after the operation starts.
func (m *Model) confirmView() string {
	var b strings.Builder
	b.WriteString(msgConfirmTitle)
	b.WriteString("\n")
	if m.pending == nil {
		return strings.TrimRight(b.String(), "\n")
	}
	d := m.pending.Descriptor()
	fmt.Fprintf(&b, "%s: %s (%s)\n", msgConfirmActionLabel, d.Title, d.Name)
	target := m.target.Path
	if target == "" {
		target = msgConfirmTargetNow
	}
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmTargetLabel, target)
	if m.target.Environment != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmEnvironment, m.target.Environment)
	}
	if len(m.target.Files) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmFileLabel, strings.Join(m.target.Files, ", "))
	}
	if len(m.target.Profiles) > 0 {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmProfile, strings.Join(m.target.Profiles, ", "))
	}
	if m.target.ProjectName != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmProject, m.target.ProjectName)
	} else {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmProject, msgConfirmProjectAuto)
	}
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmScopeLabel, msgConfirmScope)
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmEffectLabel, d.Summary)
	b.WriteString(msgConfirmRisk)
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) contentWidth() int {
	if m.width <= 0 {
		return 0
	}
	const padding = 2
	if m.width <= padding*2 {
		return m.width
	}
	return m.width - padding*2
}

func (m *Model) hints() string {
	if m.form != nil {
		return msgHintForm
	}
	if m.live != nil {
		if m.live.mode == core.LiveAttach {
			return msgHintAttach
		}
		return msgHintLive
	}
	if m.phase == phaseConfirm {
		if m.pendingApproval != core.ApprovalNone {
			return msgHintConfirm
		}
		return msgConfirmHints
	}
	if m.phase == phaseNoPrimary {
		return msgHintQuit
	}
	if m.phase == phaseSelectingEnvironment {
		return msgHintEnvSelect
	}
	if m.phase == phaseLoading && m.active != nil && !m.active.Descriptor().ReadOnly {
		return msgHintBusy
	}
	if m.phase != phaseReady {
		return msgHintQuit
	}
	if len(m.features) > 1 {
		return msgHintMenu
	}
	return msgHintSingle
}

func environmentSummary(env core.Environment) string {
	parts := []string{}
	if len(env.Files) == 0 {
		parts = append(parts, msgEnvDefaultFiles)
	} else {
		parts = append(parts, fmt.Sprintf("%s %d개", msgEnvComposeFiles, len(env.Files)))
	}
	if len(env.EnvFiles) > 0 {
		parts = append(parts, fmt.Sprintf("%s %d개", msgEnvEnvFiles, len(env.EnvFiles)))
	}
	if len(env.Profiles) > 0 {
		parts = append(parts, msgEnvProfiles+" "+strings.Join(env.Profiles, ", "))
	}
	return strings.Join(parts, " · ")
}
