// Package tui renders the interactive screen. It is assembled entirely from
// the feature registry and invokes features only through the common engine.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/stack/present"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// Options are the shared TUI dependencies.
type Options struct {
	Engine   *core.Engine
	Registry *core.Registry
	Target   core.TargetRequest
	// Theme overrides theme detection. It exists for tests and for a future
	// caller that has already resolved the terminal profile; nil means the
	// TUI resolves it once from the environment.
	Theme *visual.Theme
	// SpinnerInterval overrides the working animation cadence. Tests inject a
	// short interval and drive the tick deterministically; production keeps the
	// default.
	SpinnerInterval time.Duration
	// MarqueeInterval overrides the sidebar animation cadence.
	MarqueeInterval time.Duration
	// MarqueeTick schedules a tick; nil uses Bubble Tea's timer.
	MarqueeTick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
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
	phaseEnvironmentActions
	phaseLoadingServices
	phaseSelectingServices
	phaseLoading
	phasePlanning
	phaseConfirm
	phaseLoadingInputs
	phaseForm
	phaseWizardLoading
	phaseWizardStep
	phaseWizardPreview
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

	// filter narrows the menu by title or summary and filtering is true while
	// "/" has the keyboard. navExpanded is the single-column menu's tab state:
	// it changes the one-row summary, never the row count.
	filter      string
	filtering   bool
	navExpanded bool

	envs        []core.Environment
	envSelected int
	envSwitch   bool
	envAction   int
	envCreated  string

	phase        phase
	result       *core.Result
	currentStage *core.Stage
	// stages is every stage the engine reported this run, in report order,
	// with a repeated name replacing its earlier snapshot. planStages is the
	// sequence the phase is expected to pass through, so the timeline can mark
	// the stages that are still ahead. Both are cleared when a new run starts.
	stages     []core.Stage
	planStages []core.Stage
	events     chan core.Stage
	pending    core.Feature
	notice     string
	runCtx     context.Context
	runCancel  context.CancelFunc
	width      int
	height     int

	// spinnerFrame and spinnerInterval drive the working animation. The frame
	// is an index into the theme's spin glyphs; the interval is injected so a
	// test can advance the animation without waiting on a wall clock.
	spinnerFrame    int
	spinnerInterval time.Duration
	marquee         marqueeState
	marqueeInterval time.Duration
	marqueeTick     func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	// theme is resolved once in NewModel and injected into every renderer.
	// facts holds the last observed header values; neither is a global and
	// nothing re-reads the environment during a redraw.
	theme visual.Theme
	facts visual.Facts

	// The body scrolls: body is the viewport over the scrollable rows, and
	// detailOpen switches the result body to the full-value view. The detail's
	// own position is kept separately so closing it returns to the table.
	body         visual.Viewport
	detailOpen   bool
	detailScroll visual.Viewport
	// resultDismissed hides a result sheet so the menu is reachable again. A
	// long result owns the screen while it scrolls; esc puts it away without
	// discarding the value (r re-reads, any feature reruns).
	resultDismissed bool

	// helpOpen is the "?" keymap panel; confirmScroll is the open panel's
	// first visible row. Both are screen state, never persisted: a new flow
	// starts at the top.
	helpOpen      bool
	confirmScroll int

	// pendingApproval and planText hold the generic confirmation state. Any
	// approval-gated feature that implements core.Planner reuses this screen;
	// no feature-specific branch is added here. pendingApprovals carries every
	// independent approval one plan needs (for example a feature approval and
	// script_trust).
	pendingApproval  core.ApprovalKind
	pendingApprovals []core.ApprovalKind
	approvedHooks    []core.HookEntry
	planText         string
	planFingerprint  string

	// req carries the feature input collected from the interactive form. A
	// feature that implements core.InputProvider fills it before running; the
	// CLI never uses this path.
	req core.Request
	// Service scope selection is generic: any feature whose descriptor
	// declares SupportsServices and that does not collect its own input sees
	// the same read-only list before its normal plan/confirm/run path.
	serviceFeature core.Feature
	serviceOptions []core.ServiceStatus
	serviceCursor  int
	serviceChecked []bool
	serviceWhole   bool
	// live is non-nil while a feature attaches to a running process. The TUI
	// renders its output and forwards keys for core.LiveAttach.
	live   *liveState
	liveCh chan string
	// liveTop is the first visible output row and liveFollow keeps the window
	// on the tail. The header above the output is fixed and never scrolls, so
	// the service, instance, command and the "still running" state stay
	// readable while the reader walks back through the log.
	liveTop    int
	liveFollow bool
	// lastLive keeps the tail of the last live output so the management view
	// still shows what the process printed after the screen closes.
	lastLive []string
	form     *formState
	// Wizard state is generic: the feature supplies the steps and the effect
	// preview, the TUI only collects answers with the keyboard.
	wizardFeature    core.Feature
	wizardSpec       core.WizardSpec
	wizardStep       int
	wizardChoice     int
	wizardInput      string
	wizardItems      []string
	wizardEditing    bool
	wizardAnswers    map[string]string
	wizardChecked    map[string]bool
	wizardPreview    string
	wizardPreviewErr error
	// wizardNotice is guidance about the answer being typed (an empty
	// required value). It is drawn inline under that field, not as a
	// screen-level notice: the problem must stay next to the value to fix.
	wizardNotice     string
	wizardAwaiting   bool
	wizardApplying   bool
	wizardCanceled   bool
	wizardReturnRes  *core.Result
	wizardReturnFeat core.Feature
}

// NewModel builds the initial screen without running anything.
func NewModel(ctx context.Context, opts Options) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &Model{
		ctx: ctx, engine: opts.Engine, registry: opts.Registry, target: opts.Target, envSelected: -1,
		theme: resolveTheme(nil, opts.Theme),
		facts: initialFacts(opts.Target),
	}
	m.spinnerInterval = opts.SpinnerInterval
	m.marqueeInterval = opts.MarqueeInterval
	m.marqueeTick = opts.MarqueeTick
	if m.marqueeTick == nil {
		m.marqueeTick = tea.Tick
	}
	if m.marqueeInterval <= 0 {
		m.marqueeInterval = 100 * time.Millisecond
	}
	if m.spinnerInterval <= 0 {
		m.spinnerInterval = defaultSpinnerInterval
	}
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

type servicesMsg struct {
	feature core.Feature
	report  core.ServicesReport
}

func (m *Model) Init() tea.Cmd {
	switch m.phase {
	case phaseLoadingEnvironments:
		return m.listEnvironmentsCmd()
	case phaseLoading:
		return m.start(m.active)
	default:
		return nil
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if tick, ok := msg.(marqueeTickMsg); ok {
		m.advanceMarquee(tick)
		return m, m.scheduleMarquee()
	}
	model, cmd := m.update(msg)
	next := m.scheduleMarquee()
	if cmd == nil {
		return model, next
	}
	if next == nil {
		return model, cmd
	}
	return model, tea.Batch(cmd, next)
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case stageMsg:
		st := msg.stage
		m.currentStage = &st
		m.observeStage(st)
		if m.phase == phaseLoading || m.phase == phasePlanning {
			return m, waitStageCmd(m.events)
		}
		return m, nil
	case spinnerTickMsg:
		return m, m.spinOneFrame()
	case planMsg:
		m.currentStage = nil
		// The plan's own stages are what the confirmation screen continues
		// from: the same timeline shape, with the run's remaining stages still
		// marked as pending. A failed plan makes no promise about a run.
		m.stages = append([]core.Stage{}, msg.report.Stages...)
		m.planStages = nil
		if msg.report.Failed() {
			res := msg.report.Result()
			m.result = &res
			m.phase = phaseReady
			return m, nil
		}
		if m.active != nil {
			m.setPlan(runPending(core.PlannedStages(m.active), msg.report))
		}
		m.observeTarget(msg.report.Target)
		m.observeTools(msg.report.Tools)
		m.planText = msg.report.Text
		m.planFingerprint = msg.report.Fingerprint
		m.approvedHooks = append([]core.HookEntry{}, msg.report.Hooks...)
		required := append([]core.ApprovalKind{}, msg.report.RequiredApprovals...)
		if m.active != nil {
			if own := m.active.Descriptor().Approval; own != core.ApprovalNone {
				required = append(required, own)
			}
		}
		required = uniqueApprovalKinds(required)
		if len(required) > 0 {
			m.pendingApprovals = required
			m.pendingApproval = required[0]
		} else {
			// Ordinary update: the plan is the preview; one deliberate
			// confirmation runs it without an extra approval kind.
			m.pending = m.active
		}
		m.phase = phaseConfirm
		m.confirmScroll = 0
		return m, nil
	case environmentListMsg:
		if m.phase != phaseLoadingEnvironments {
			return m, nil
		}
		if m.envCreated != "" {
			if msg.err == nil {
				m.envs = msg.envs
			} else {
				m.notice = msgEnvReloadFailed + msg.err.Error()
			}
			m.target.Environment = m.envCreated
			m.facts.Environment = m.envCreated
			m.envCreated = ""
			m.phase = phaseReady
			return m, nil
		}
		if m.envSwitch {
			if msg.err != nil || len(msg.envs) == 0 {
				m.envSwitch = false
				m.phase = phaseReady
				m.notice = msgEnvSwitchFailed
				return m, nil
			}
			m.envs = msg.envs
			m.envSelected = -1
			m.phase = phaseSelectingEnvironment
			return m, nil
		}
		// On startup the primary feature reports catalog failures through the engine.
		if msg.err != nil || len(msg.envs) == 0 {
			return m, m.start(m.primary)
		}
		m.envs = msg.envs
		m.envSelected = -1
		m.phase = phaseSelectingEnvironment
		return m, nil
	case servicesMsg:
		if m.phase != phaseLoadingServices || m.serviceFeature == nil {
			return m, nil
		}
		if msg.feature == nil || msg.feature.Descriptor().Name != m.serviceFeature.Descriptor().Name {
			return m, nil
		}
		if msg.report.Failed() {
			// A service list that cannot be read is a failure, not an empty
			// stack. Nothing runs and no confirmation is offered.
			res := msg.report.Result()
			m.active = msg.feature
			m.result = &res
			m.phase = phaseReady
			m.resetServiceSelection()
			return m, nil
		}
		m.observeTarget(msg.report.Target)
		m.observeTools(msg.report.Tools)
		m.serviceOptions = append([]core.ServiceStatus{}, msg.report.Services...)
		m.serviceChecked = make([]bool, len(m.serviceOptions))
		m.serviceCursor = 0
		m.serviceWhole = true
		m.phase = phaseSelectingServices
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
		m.observeTarget(msg.report.Target)
		m.observeTools(msg.report.Tools)
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
		if m.wizardCanceled {
			// A canceled wizard step run may deliver its result after the
			// user already returned to the previous screen.
			m.wizardCanceled = false
			return m, nil
		}
		res := msg.res
		m.currentStage = nil
		// A result ends the run, so the timeline stops animating. The reported
		// stages come from the result itself; a failed or canceled run keeps
		// the stages that never ran so their absence is visible next to the
		// diagnostics.
		m.stages = append([]core.Stage{}, res.Stages...)
		if res.Outcome == core.OutcomeSuccess {
			m.planStages = nil
		}
		m.observeTarget(res.Target)
		m.observeTools(res.Tools)
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
			m.runCtx = nil
		}
		if m.wizardAwaiting {
			m.wizardAwaiting = false
			m.beginWizardSteps(res)
			return m, nil
		}
		created := ""
		if m.wizardApplying && res.Outcome == core.OutcomeSuccess && m.wizardFeature != nil && m.wizardFeature.Descriptor().EnvironmentAdmin {
			created = strings.TrimSpace(m.wizardAnswers["name"])
		}
		if m.wizardApplying {
			m.wizardApplying = false
			m.wizardFeature = nil
			m.wizardSpec = core.WizardSpec{}
			m.wizardAnswers = nil
			m.wizardPreview = ""
			m.wizardPreviewErr = nil
		}
		m.result = &res
		m.phase = phaseReady
		m.notice = ""
		m.resultDismissed = false
		m.resetScrollState()
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
		if created != "" && m.engine != nil && m.engine.SupportsEnvironments() {
			m.envCreated = created
			m.phase = phaseLoadingEnvironments
			return m, m.listEnvironmentsCmd()
		}
		return m, nil
	case tea.KeyMsg:
		// The help panel is the outermost layer: while it is open it owns
		// every key, and closing it runs nothing.
		if m.helpOpen {
			return m, m.updateHelpKey(msg)
		}
		// The filter owns the keyboard while "/" is active: q and r are text,
		// not commands, so a search can contain any letter.
		if m.filtering {
			return m, m.updateFilterKey(msg)
		}
		if m.form != nil {
			return m.updateFormKey(msg)
		}
		if m.live != nil {
			return m.updateLiveKey(msg)
		}
		// Body scrolling runs before the phase handlers: PgUp/PgDn, Home/End
		// and g/G have no other meaning on a result screen. The detail view
		// owns every key while it is open, so esc/d close it instead of
		// clearing the screen.
		if m.detailOpen {
			return m, m.detailKey(msg)
		}
		if handled, cmd := m.scrollKey(msg); handled {
			return m, cmd
		}
		if m.phase == phaseSelectingServices {
			return m.updateServiceKey(msg)
		}
		if m.phase == phaseConfirm {
			if msg.String() == "?" {
				m.openHelp()
				return m, nil
			}
			return m.updateConfirmKey(msg)
		}
		switch msg.String() {
		case "q":
			if m.wizardTextInput() {
				// A typed value may legitimately contain q; the wizard
				// handler below receives it.
				break
			}
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
		case "esc":
			if m.phase == phaseEnvironmentActions || (m.phase == phaseSelectingEnvironment && m.envSwitch) {
				m.envSwitch = false
				m.phase = phaseReady
				return m, nil
			}
			if m.isWizardPhase() {
				if m.phase == phaseWizardStep && m.wizardEditing {
					m.wizardEditing = false
					m.wizardInput = ""
					m.wizardNotice = ""
					return m, nil
				}
				m.cancelWizard()
				return m, nil
			}
			if m.navMenuVisible() && m.filter != "" {
				// esc clears the filter: the whole registry comes back and the
				// cursor stays on the same feature when it is still visible.
				m.filter = ""
				m.filtering = false
				m.keepSelectionVisible()
			}
			// A result sheet that covers the menu is put away, not acted on:
			// nothing was selected, so nothing must run. A short result that
			// leaves the menu visible keeps its previous behaviour (esc is a
			// no-op), so an accidental key does not blank the screen.
			if m.phase == phaseReady && m.result != nil && !m.resultDismissed && m.resultScrolls() {
				m.resultDismissed = true
				m.body = visual.Viewport{}
			}
			return m, nil
		}
		if m.navMenuVisible() {
			switch msg.String() {
			case "/":
				m.filtering = true
				return m, nil
			case "tab", "shift+tab":
				// The single-column summary expands in place. It is one row
				// either way, so the content keeps its height. Only the
				// medium layout has that row; elsewhere tab has no meaning
				// and must not pretend it did something.
				if m.layoutMode() == visual.ModeMedium {
					m.navExpanded = !m.navExpanded
				}
				return m, nil
			}
		}
		if m.isWizardPhase() {
			return m, m.updateWizard(msg)
		}
		if m.phase == phaseEnvironmentActions {
			switch msg.String() {
			case "?":
				m.openHelp()
			case "up", "k":
				m.envAction = 1 - m.envAction
			case "down", "j":
				m.envAction = 1 - m.envAction
			case "enter":
				if m.envAction == 0 {
					return m, m.startRunPath(m.active)
				}
				m.envSwitch = true
				m.phase = phaseLoadingEnvironments
				return m, m.listEnvironmentsCmd()
			}
			return m, nil
		}
		if m.phase == phaseSelectingEnvironment {
			switch msg.String() {
			case "?":
				m.openHelp()
				return m, nil
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
			case "pgup":
				m.envSelected = max(0, m.envSelected-max(1, m.currentViewport().Visible()))
			case "pgdown":
				m.envSelected = min(len(m.envs)-1, max(0, m.envSelected)+max(1, m.currentViewport().Visible()))
			case "home", "g":
				m.envSelected = 0
			case "end", "G":
				m.envSelected = len(m.envs) - 1
			case "enter":
				if m.envSelected >= 0 && m.envSelected < len(m.envs) && m.primary != nil {
					m.target.Environment = m.envs[m.envSelected].Name
					if m.envSwitch {
						m.envSwitch = false
						m.facts.Environment = m.target.Environment
						m.result = nil
						m.resultDismissed = false
						m.resetScrollState()
						m.phase = phaseReady
						return m, nil
					}
					return m, m.start(m.primary)
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "?":
			// Help is available on every screen that advertises it. It only
			// shows the current screen's keys and closes with esc; it never
			// runs anything.
			m.openHelp()
			return m, nil
		case "d":
			if m.phase == phaseReady && m.hasDetail() {
				m.openDetail()
				return m, nil
			}
			return m, nil
		case "r":
			if m.active == nil || m.phase != phaseReady {
				return m, nil
			}
			if m.resultDismissed && m.result != nil {
				m.resultDismissed = false
				return m, nil
			}
			return m, m.start(m.active)
		case "up", "k":
			if m.navMenuVisible() {
				if m.selectionVisible() {
					m.moveSelection(-1)
				} else if m.resultScrolls() {
					m.applyScroll(func(v *visual.Viewport) { v.Move(-1) })
				}
			}
			return m, nil
		case "down", "j":
			if m.navMenuVisible() {
				if m.selectionVisible() {
					m.moveSelection(1)
				} else if m.resultScrolls() {
					m.applyScroll(func(v *visual.Viewport) { v.Move(1) })
				}
			}
			return m, nil
		case "enter":
			// While the result sheet covers the menu, or a filter has hidden
			// the navigation, the highlighted feature is not visible; running
			// it would act on an unseen selection.
			if m.selectionVisible() {
				if f := m.selectedFeature(); f != nil {
					return m, m.start(f)
				}
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

// plansBeforeRun reports whether a feature can describe its planned work
// before it runs. Such a feature sees a read-only plan first: approval-gated
// features collect their independent approval on that screen, and an ordinary
// update simply confirms the same target and planned work once.
func plansBeforeRun(f core.Feature) bool {
	if f == nil {
		return false
	}
	if f.Descriptor().Hooks != core.HookNone {
		// up/update need the hook plan and the independent script_trust
		// decision before anything runs, even when no Planner is registered.
		return true
	}
	_, ok := f.(core.Planner)
	return ok
}

// uniqueApprovalKinds keeps each independent approval once, in request order.
func uniqueApprovalKinds(kinds []core.ApprovalKind) []core.ApprovalKind {
	out := make([]core.ApprovalKind, 0, len(kinds))
	seen := map[core.ApprovalKind]bool{}
	for _, kind := range kinds {
		if kind == core.ApprovalNone || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

// start begins a user-selected feature. Nothing runs until a supporting
// feature has first offered the current stack's service scope and the user
// has confirmed or narrowed it. The confirmation/plan path stays the same:
// this only fills Request.Services before it.
func (m *Model) start(f core.Feature) tea.Cmd {
	if f == nil {
		return nil
	}
	m.ensureRequest(f)
	m.pending = nil
	if f.Descriptor().EnvironmentAdmin && m.engine != nil && m.engine.SupportsEnvironments() {
		m.active = f
		m.envAction = 0
		m.phase = phaseEnvironmentActions
		return nil
	}
	if m.wantsServiceSelection(f) {
		return m.beginServiceSelection(f)
	}
	return m.startRunPath(f)
}

// startRunPath is the existing menu/refresh decision without another service
// screen: guided features collect their own answers, input-provider features
// open their form, planned/read-only features go to the plan, and every other
// mutating feature asks for confirmation first.
func (m *Model) startRunPath(f core.Feature) tea.Cmd {
	if f == nil {
		return nil
	}
	if f.Descriptor().Wizard {
		return m.beginWizard(f)
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
	if f.Descriptor().ReadOnly || plansBeforeRun(f) {
		return m.begin(f)
	}
	m.pending = f
	// The screen is a bare confirmation with no plan behind it, so no stage
	// sequence is known; a previous run's timeline must not leak into it.
	m.stages = nil
	m.planStages = nil
	m.phase = phaseConfirm
	m.confirmScroll = 0
	m.notice = ""
	return nil
}

// wantsServiceSelection reports whether the generic scope screen applies. A
// feature that collects its own service input (the exec form or a wizard's
// steps) keeps that existing interaction; no feature name is checked.
func (m *Model) wantsServiceSelection(f core.Feature) bool {
	if f == nil || m.engine == nil {
		return false
	}
	d := f.Descriptor()
	if !d.SupportsServices || d.Wizard {
		return false
	}
	_, hasOwnInput := f.(core.InputProvider)
	return !hasOwnInput
}

func (m *Model) beginServiceSelection(f core.Feature) tea.Cmd {
	m.serviceFeature = f
	m.serviceOptions = nil
	m.serviceCursor = 0
	m.serviceChecked = nil
	m.serviceWhole = true
	// The table starts at its first row, never at the previous screen's scroll
	// position: the select screen measures different rows than a result body.
	m.body = visual.Viewport{}
	m.req.Feature = f.Descriptor().Name
	m.req.Target = m.target
	// Never let a previous run's scope leak into this one before the user
	// chooses again.
	m.req.Services = nil
	m.notice = ""
	m.phase = phaseLoadingServices
	engine := m.engine
	ctx := m.ctx
	req := m.req
	return func() tea.Msg {
		return servicesMsg{feature: f, report: engine.Services(ctx, req, nil)}
	}
}

func (m *Model) resetServiceSelection() {
	m.serviceFeature = nil
	m.serviceOptions = nil
	m.serviceCursor = 0
	m.serviceChecked = nil
	m.serviceWhole = true
	// The table window is reset with the rows it measured: a later screen with
	// a different service count must not start mid-list.
	m.body = visual.Viewport{}
}

// updateServiceKey drives the scope table. The first row is the whole stack and
// is selected until an individual service is checked; space on the whole row
// clears every individual choice. Enter feeds Request.Services, esc returns to
// the menu, and q/ctrl+c quit without running anything. The cursor window follows
// the selection, so every row stays reachable on a large stack.
func (m *Model) updateServiceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.serviceOptions) + 1
	switch msg.String() {
	case "up", "k":
		m.serviceCursor = (m.serviceCursor - 1 + total) % total
		m.followServiceCursor()
		return m, nil
	case "down", "j":
		m.serviceCursor = (m.serviceCursor + 1) % total
		m.followServiceCursor()
		return m, nil
	case "pgup":
		m.serviceCursor = max(m.serviceCursor-m.pageRows(), 0)
		m.followServiceCursor()
		return m, nil
	case "pgdown":
		m.serviceCursor = min(m.serviceCursor+m.pageRows(), total-1)
		m.followServiceCursor()
		return m, nil
	case "home", "g":
		m.serviceCursor = 0
		m.followServiceCursor()
		return m, nil
	case "end", "G":
		m.serviceCursor = total - 1
		m.followServiceCursor()
		return m, nil
	case " ":
		if m.serviceCursor == 0 {
			m.serviceWhole = true
			for i := range m.serviceChecked {
				m.serviceChecked[i] = false
			}
			return m, nil
		}
		index := m.serviceCursor - 1
		if index < 0 || index >= len(m.serviceOptions) || !m.serviceOptions[index].Configured {
			return m, nil
		}
		m.serviceChecked[index] = !m.serviceChecked[index]
		m.serviceWhole = !anyChecked(m.serviceChecked)
		return m, nil
	case "enter":
		feature := m.serviceFeature
		if feature == nil {
			return m, nil
		}
		var services []string
		if !m.serviceWhole {
			for i, svc := range m.serviceOptions {
				if i < len(m.serviceChecked) && m.serviceChecked[i] {
					services = append(services, svc.Name)
				}
			}
		}
		m.resetServiceSelection()
		m.req.Feature = feature.Descriptor().Name
		m.req.Target = m.target
		m.req.Services = services
		return m, m.startRunPath(feature)
	case "esc":
		m.resetServiceSelection()
		m.phase = phaseReady
		m.notice = msgServiceCanceled
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// pageRows is how far PgUp/PgDn move on the scope table: one visible window.
func (m *Model) pageRows() int {
	if rows := m.serviceViewport().Visible(); rows > 0 {
		return rows
	}
	return 1
}

func anyChecked(checked []bool) bool {
	for _, ok := range checked {
		if ok {
			return true
		}
	}
	return false
}

// begin starts a feature. Features that can describe their planned work go
// through a read-only plan first; everything else runs directly, preserving the
// ticket 01 behavior.
func (m *Model) begin(f core.Feature) tea.Cmd {
	if f == nil {
		return m.startRun(f, nil)
	}
	m.active = f
	if m.req.Feature != f.Descriptor().Name {
		m.req = core.Request{Feature: f.Descriptor().Name, Target: m.target}
	}
	// A new flow has no approved plan yet; the plan message sets it again.
	m.req.PlanFingerprint = ""
	m.resetRunState()
	m.setupLive(f)
	if plansBeforeRun(f) {
		m.phase = phasePlanning
		m.setPlan(core.PlannedPlanStages(f))
		m.stages = nil
		return tea.Batch(m.planCmd(f, m.events), waitStageCmd(m.events), m.spinnerCmd())
	}
	m.setPlan(core.PlannedStages(f))
	m.stages = nil
	return m.launchRun(f, nil, nil)
}

// beginRequest runs a feature with inputs collected by the TUI form.
func (m *Model) beginRequest(f core.Feature, req core.Request) tea.Cmd {
	req.Feature = f.Descriptor().Name
	req.Target = m.target
	// Inputs were collected without a plan, so there is nothing to bind yet.
	req.PlanFingerprint = ""
	m.req = req
	m.resetRunState()
	m.setupLive(f)
	return m.launchRun(f, nil, nil)
}

// startRun runs a feature without another plan pass. The plan (when shown) is
// already the confirmation; ordinary updates need no second approval.
func (m *Model) startRun(f core.Feature, options map[string]string) tea.Cmd {
	m.ensureRequest(f)
	// The confirmed plan is bound to its previewed inputs. It is copied into
	// the request before resetRunState clears the screen state.
	m.req.PlanFingerprint = m.planFingerprint
	// The confirmed plan's observed stages and its remaining sequence are what
	// the running screen continues from; both are copied before the reset.
	planned, done := m.planStages, m.stages
	m.resetRunState()
	m.setPlan(planned)
	m.stages = done
	m.setupLive(f)
	return m.launchRun(f, nil, options)
}

func (m *Model) runApproved(f core.Feature, approvals []core.ApprovalKind) tea.Cmd {
	m.ensureRequest(f)
	m.req.PlanFingerprint = m.planFingerprint
	planned, done := m.planStages, m.stages
	m.resetRunState()
	m.setPlan(planned)
	m.stages = done
	m.setupLive(f)
	return m.launchRun(f, approvals, nil)
}

// beginWithOptions runs a wizard step listing or apply action with the
// feature's declared approval. The steps run only returns the questions (the
// engine skips approval for that read-only call); an approval-gated apply is
// routed by applyWizard through the generic plan + confirmation screen, which
// is what actually supplies the approval. This is generic UI collection; the
// engine still decides whether the approval is enough, and script_trust
// remains a separate later decision.
func (m *Model) beginWithOptions(f core.Feature, options map[string]string) tea.Cmd {
	m.ensureRequest(f)
	m.resetRunState()
	m.setupLive(f)
	var approvals []core.ApprovalKind
	if f != nil {
		if kind := f.Descriptor().Approval; kind != core.ApprovalNone {
			approvals = []core.ApprovalKind{kind}
		}
	}
	return m.launchRun(f, approvals, options)
}

// ensureRequest resets collected inputs when the selected feature changes, so
// one feature's service/argument choices never leak into another operation.
func (m *Model) ensureRequest(f core.Feature) {
	if f == nil {
		return
	}
	if m.req.Feature != f.Descriptor().Name {
		m.req = core.Request{Feature: f.Descriptor().Name, Target: m.target}
	}
	m.req.Target = m.target
}

// resetRunState clears the previous screen and starts a fresh stage channel.
// Collected feature inputs in m.req are preserved unless the feature changes.
func (m *Model) resetRunState() {
	m.result = nil
	m.resultDismissed = false
	m.resetScrollState()
	m.currentStage = nil
	m.stages = nil
	m.planStages = nil
	m.pending = nil
	m.pendingApproval = ""
	m.pendingApprovals = nil
	m.approvedHooks = nil
	m.planText = ""
	m.planFingerprint = ""
	m.notice = ""
	m.helpOpen = false
	m.confirmScroll = 0
	m.closeLive()
	m.form = nil
	m.lastLive = nil
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
		m.runCtx = nil
	}
	m.events = make(chan core.Stage, 64)
}

func (m *Model) launchRun(f core.Feature, approvals []core.ApprovalKind, options map[string]string) tea.Cmd {
	m.active = f
	m.phase = phaseLoading
	if len(m.planStages) == 0 {
		m.setPlan(core.PlannedStages(f))
	}
	m.runCtx, m.runCancel = context.WithCancel(m.ctx)
	cmds := []tea.Cmd{m.runCmd(f, approvals, options, m.events, m.runCtx), waitStageCmd(m.events)}
	if m.live != nil {
		cmds = append(cmds, waitLiveCmd(m.liveCh))
	}
	if spin := m.spinnerCmd(); spin != nil {
		cmds = append(cmds, spin)
	}
	return tea.Batch(cmds...)
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
	m.pendingApprovals = nil
	m.approvedHooks = nil
	m.planText = ""
	m.planFingerprint = ""
	m.req.PlanFingerprint = ""
	m.confirmScroll = 0
	m.phase = phaseReady
}

func (m *Model) runCmd(f core.Feature, approvals []core.ApprovalKind, options map[string]string, events chan core.Stage, ctx context.Context) tea.Cmd {
	engine := m.engine
	req := m.req
	req.Feature = f.Descriptor().Name
	req.Target = m.target
	req.Approvals = approvals
	req.ApprovedHooks = append([]core.HookEntry{}, m.approvedHooks...)
	// The confirmed plan fingerprint was copied into m.req before the screen
	// state was reset.
	req.PlanFingerprint = m.req.PlanFingerprint
	req.Interactive = true
	req.JSON = false
	if options != nil {
		if req.Options == nil {
			req.Options = map[string]string{}
		}
		for key, value := range options {
			req.Options[key] = value
		}
	}
	m.approvedHooks = nil
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
	req := m.req
	req.Feature = f.Descriptor().Name
	req.Target = m.target
	req.Interactive = true
	if req.Options == nil {
		req.Options = map[string]string{}
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

// View composes the three fixed bands: the header, the phase body and the
// keycap footer. Every line fits the terminal width exactly, so a resize
// reflows immediately and never leaves the frame ragged.
func (m *Model) View() string {
	body := m.renderBody()
	if m.overlayOpen() {
		// The panel covers the body; what shows through is the screen the
		// user was on, dimmed. It still states the stack identity and the
		// keys, so the panel never hides where the change would land.
		body = m.backgroundModel().renderBody()
	}
	lines := m.composeFrame(body)
	// No trailing newline: the frame already occupies exactly the terminal
	// height, and Bubble Tea drops the top line of a view taller than the
	// window. One extra newline would scroll the header off screen.
	return strings.Join(lines, "\n")
}

// menuWindow bounds how many feature rows the screen shows so a growing
// feature registry cannot push the content off screen. The selection window
// follows the cursor and the omitted rows are stated explicitly.
const menuWindow = 7

// menuView is the single-column feature menu: the same sectioned list as the
// sidebar, with the machine name kept because the layout has the width for it.
// The cursor marker is the two-cell "> " form the existing key tests assert.
// budget bounds the rows so a long registry cannot push the notice or the
// result off the body; zero means no coding yet.
func (m *Model) menuView(budget int) string {
	if budget <= 0 {
		budget = menuWindow
	}
	lines := visual.MenuLines(m.theme, m.bodyWidth(), m.nav(), budget, !m.compact())
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// compactByHeight reports a terminal too short for the decorated layout. The
// compact mode is chosen by width or height, but only the height case needs the
// navigation summary: a narrow terminal still has rows for the menu list.
func (m *Model) compactByHeight() bool {
	return m.height > 0 && m.height < visual.CompactMinRows
}

// navLine is the one-row navigation of the single-column layouts. The wide
// layout has the persistent sidebar instead. A compact-by-height screen keeps
// the row too: spec stories 11/12/14 require the current position to stay
// visible, and a screen that hid the only navigation would let ↑/↓ move an
// invisible cursor. Before the first WindowSizeMsg it renders nothing; a guess
// would only be replaced one frame later.
func (m *Model) navLine() string {
	if m.width <= 0 || m.layoutMode() == visual.ModeWide {
		return ""
	}
	if m.layoutMode() == visual.ModeCompact && !m.compactByHeight() {
		// Compact by width: the menu list is drawn, and it names the position.
		return ""
	}
	if !m.navMenuVisible() {
		return ""
	}
	return visual.NavRow(m.theme, m.bodyWidth(), m.nav(), m.navExpanded)
}

// serviceSelectCellWidth is the exact width of the selection column. It is
// four cells because the cursor glyph and the checkbox together are four
// (`▸[x]`), which keeps every row's name starting in the same cell.
const serviceSelectCellWidth = 4

// serviceScopeColumns is the approved column contract for the scope table: the
// status table's columns with the selection column first, in the amended order
// 서비스 · 상태 · 인스턴스 · health · 이미지 · 포트.
func serviceScopeColumns() []core.Column {
	return []core.Column{
		{Header: msgServiceColumnSelect, Fixed: serviceSelectCellWidth},
		{Header: msgServiceColumnService, Weight: 30, Min: 12, Max: 24},
		{Header: msgServiceColumnState, Fixed: 11},
		{Header: msgServiceColumnInstances, Fixed: 8, Align: core.AlignRight},
		{Header: msgServiceColumnHealth, Fixed: 8, DropPriority: 3},
		{Header: msgServiceColumnImage, Weight: 45, Min: 16, Max: 34, DropPriority: 2},
		{Header: msgServiceColumnPort, Fixed: 13, DropPriority: 1},
	}
}

// serviceTable is the scope table for the current catalog. compact folds a
// health problem into the state cell, because the health column is not drawn
// there; the icon and the Korean label both survive the fold.
func (m *Model) serviceTable(compact bool) core.Table {
	table := core.Table{Columns: serviceScopeColumns(), EmptyText: msgServiceNoServices}
	for i, svc := range m.serviceOptions {
		checked := "[ ]"
		if i < len(m.serviceChecked) && m.serviceChecked[i] {
			checked = "[x]"
		}
		// The cursor is the gutter glyph next to the checkbox: the approved
		// non-color position marker, and it keeps the checkbox itself aligned.
		marker := " "
		if m.serviceCursor == i+1 {
			marker = m.theme.Glyphs.Sel
		}
		state := present.StateLabelValue(svc.State)
		stateBadge := present.StateBadgeValue(svc.State)
		if compact {
			if folded, ok := serviceFoldedHealth(svc.Health); ok {
				state, stateBadge = folded, present.HealthBadgeValue(svc.Health)
			}
		}
		table.Rows = append(table.Rows, core.Row{
			Selectable: svc.Configured,
			Cells: []core.Cell{
				{Text: marker + checked},
				{Text: svc.Name, Detail: svc.Name},
				{Text: state, Badge: stateBadge},
				{Text: fmt.Sprintf("%d", svc.Instances)},
				{Text: present.HealthLabelValue(svc.Health), Badge: present.HealthBadgeValue(svc.Health)},
				{Text: serviceImageLabel(svc), Detail: svc.Image},
				{Text: servicePortLabel(svc), Detail: svc.Ports},
			},
		})
	}
	return table
}

// serviceScreen splits the scope screen on the same terms as the result table:
// the heading, target, whole-stack row and table header never scroll, the
// service rows do, and the guidance below stays put. The whole-stack row is
// pinned above the table rather than being one of its rows: it has no state,
// image or port, and keeping it visible means a narrowed scope can always be
// widened again without scrolling back to the first row.
func (m *Model) serviceScreen() screenLines {
	th := m.theme
	width := m.bodyWidth()
	compact := m.compact()

	fixed := []string{th.Bold(visual.TokenAccent, msgServiceSelectTitle)}
	dir := m.target.Path
	if dir == "" {
		dir = msgConfirmTargetNow
	}
	fixed = append(fixed, msgConfirmTargetLabel+": "+dir)
	if m.target.Environment != "" {
		fixed = append(fixed, msgConfirmEnvironment+": "+m.target.Environment)
	}
	if !compact {
		fixed = append(fixed, "", m.hintText(msgServiceSelectIntro))
	}
	fixed = append(fixed, "", m.wholeStackRow())

	table := m.serviceTable(compact)
	tail := m.serviceTail(compact)
	if width <= 0 {
		// No size yet: render the cells complete instead of guessing widths.
		// The next WindowSizeMsg reflows the same table.
		rows := visual.RenderDocument(th, core.Document{
			Blocks: []core.Block{{Kind: core.BlockTable, Table: &table}},
		}, 0)
		return screenLines{Fixed: fixed, Band: rows, Tail: tail}
	}
	layout := visual.LayoutTableOptions(th, table, width, visual.TableOptions{
		Compact:        compact,
		CompactColumns: 3,
		// The whole-stack row is pinned above the table, so the cursor is a
		// table row only when it points at a service.
		CursorRow: m.serviceCursor,
	})
	if layout.Header != "" {
		fixed = append(fixed, layout.Header)
	}
	return screenLines{Fixed: fixed, Band: layout.Rows, Tail: tail}
}

// serviceTail is the guidance below the table. Compact keeps only the
// unconfigured marker, because it states a fact about a visible row; the note
// about engine revalidation can wait for a decorated screen.
func (m *Model) serviceTail(compact bool) []string {
	var tail []string
	if flag := m.unconfiguredFlag(); flag != "" {
		tail = append(tail, flag)
	}
	if !compact {
		tail = append(tail, "", m.hintText(msgServiceSelectionNote))
	}
	return tail
}

// wholeStackRow is the always-visible whole-stack choice. It uses the hook
// glyph rather than a checkbox: it is not a service row with state, and its
// marker sits in the same cell as the table's cursor so the label lines up.
func (m *Model) wholeStackRow() string {
	marker := " "
	if m.serviceCursor == 0 {
		marker = m.theme.Glyphs.Sel
	}
	checked := "[ ]"
	if m.serviceWhole {
		checked = "[x]"
	}
	return marker + checked + " " + msgServiceWhole
}

// unconfiguredFlag names the services that are visible but cannot be selected,
// so the muted row is not the only channel saying why.
func (m *Model) unconfiguredFlag() string {
	var names []string
	for _, svc := range m.serviceOptions {
		if !svc.Configured {
			names = append(names, svc.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return m.hintText(msgServiceNotConfigured + ": " + strings.Join(names, ", "))
}

// serviceView is the flat rendering of the scope screen. The frame draws the
// banded form so the header stays fixed; this exists for callers that want the
// whole screen as text.
func (m *Model) serviceView() string {
	return strings.Join(m.serviceScreen().lines(), "\n")
}

// serviceScrolls reports whether the service rows have rows outside the window.
func (m *Model) serviceScrolls() bool {
	return m.serviceGeometry().scrolls
}

// serviceViewport is the scrolling window over the service rows, taken from the
// same layout pass that draws them.
func (m *Model) serviceViewport() visual.Viewport {
	return m.serviceGeometry().viewport
}

// followServiceCursor scrolls the minimum distance that keeps the cursor row
// visible, so a large stack can be walked without losing the selection. The
// whole-stack row is pinned above the table, so choosing it returns the window
// to the first service row instead of leaving the band at the bottom.
func (m *Model) followServiceCursor() {
	v := m.serviceViewport()
	if m.serviceCursor <= 0 {
		v.Home()
		m.body.Top = v.Top
		return
	}
	v.Follow(m.serviceCursor - 1)
	m.body.Top = v.Top
}

// serviceFoldedHealth is the compact state-cell wording for a health problem.
// A healthy or absent healthcheck is not a problem, so the container state
// stays: `실행 중` says more than `정상` when the column is already gone. The
// word itself comes from the shared mapping, never a second copy.
func serviceFoldedHealth(health string) (string, bool) {
	switch stack.Health(health) {
	case stack.HealthUnhealthy, stack.HealthStarting:
		return present.HealthLabelValue(health), true
	default:
		return "", false
	}
}

// serviceImageLabel never leaves a cell empty: a catalog that cannot report the
// image shows the approved dash, which is a fact ("unknown here"), not a blank.
func serviceImageLabel(svc core.ServiceStatus) string {
	if svc.Image == "" {
		return msgServiceNoPort
	}
	return svc.Image
}

func servicePortLabel(svc core.ServiceStatus) string {
	if svc.Ports == "" {
		return msgServiceNoPort
	}
	return svc.Ports
}

// serviceScopeText is the exact scope shown on the plan/confirm screen. It
// never silently turns a prior explicit selection back into the whole stack.
func serviceScopeText(services []string) string {
	if len(services) == 0 {
		return msgConfirmScope
	}
	return strings.Join(services, ", ")
}

// contentWidth is the body width for presenter calls. It is kept for the
// existing call sites; bodyWidth is the shell-aware equivalent.
func (m *Model) contentWidth() int {
	return m.bodyWidth()
}

func environmentSummary(env core.Environment) string {
	parts := []string{}
	if len(env.Files) == 0 {
		parts = append(parts, msgEnvDefaultFiles)
	} else {
		parts = append(parts, fmt.Sprintf(msgEnvCount, msgEnvComposeFiles, len(env.Files)))
	}
	if len(env.EnvFiles) > 0 {
		parts = append(parts, fmt.Sprintf(msgEnvCount, msgEnvEnvFiles, len(env.EnvFiles)))
	}
	if len(env.Profiles) > 0 {
		parts = append(parts, msgEnvProfiles+" "+strings.Join(env.Profiles, ", "))
	}
	return strings.Join(parts, " · ")
}
