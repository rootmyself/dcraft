// Package tui renders the interactive screen. It is assembled entirely from
// the feature registry and invokes features only through the common engine.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
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

	// theme is resolved once in NewModel and injected into every renderer.
	// facts holds the last observed header values; neither is a global and
	// nothing re-reads the environment during a redraw.
	theme visual.Theme
	facts visual.Facts

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
	wizardAnswers    map[string]string
	wizardChecked    map[string]bool
	wizardPreview    string
	wizardPreviewErr error
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
		return m, nil
	case environmentListMsg:
		if m.phase != phaseLoadingEnvironments {
			return m, nil
		}
		// A broken catalog is not hidden here: running the primary feature
		// through the engine produces the same resolve_target failure with
		// Korean guidance and no Docker contact.
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
		return m, nil
	case tea.KeyMsg:
		if m.form != nil {
			return m.updateFormKey(msg)
		}
		if m.live != nil {
			return m.updateLiveKey(msg)
		}
		if m.phase == phaseSelectingServices {
			return m.updateServiceKey(msg)
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
					m.planText = ""
					m.phase = phaseReady
					m.notice = ""
				}
				return m, nil
			case "enter":
				if m.pendingApproval != core.ApprovalNone {
					feature := m.active
					approvals := append([]core.ApprovalKind{}, m.pendingApprovals...)
					m.pendingApproval = ""
					m.pendingApprovals = nil
					m.planText = ""
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
				return m, m.startRun(f, nil)
			}
			return m, nil
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
			if m.isWizardPhase() {
				m.cancelWizard()
			}
			return m, nil
		}
		if m.isWizardPhase() {
			return m, m.updateWizard(msg)
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
					return m, m.start(m.primary)
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "r":
			if m.active == nil || m.phase != phaseReady {
				return m, nil
			}
			return m, m.start(m.active)
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
				return m, m.start(m.features[m.selected])
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
	m.phase = phaseConfirm
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
}

// updateServiceKey drives the scope screen. The first row is the whole stack
// and is selected until an individual service is checked; space on the whole
// row clears every individual choice. Enter feeds Request.Services, esc
// returns to the menu, and q/ctrl+c quit without running anything.
func (m *Model) updateServiceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.serviceOptions) + 1
	switch msg.String() {
	case "up", "k":
		m.serviceCursor = (m.serviceCursor - 1 + total) % total
		return m, nil
	case "down", "j":
		m.serviceCursor = (m.serviceCursor + 1) % total
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
		return tea.Batch(m.planCmd(f, m.events), waitStageCmd(m.events))
	}
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
	m.resetRunState()
	m.setupLive(f)
	return m.launchRun(f, nil, options)
}

func (m *Model) runApproved(f core.Feature, approvals []core.ApprovalKind) tea.Cmd {
	m.ensureRequest(f)
	m.req.PlanFingerprint = m.planFingerprint
	m.resetRunState()
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
	m.currentStage = nil
	m.pending = nil
	m.pendingApproval = ""
	m.pendingApprovals = nil
	m.approvedHooks = nil
	m.planText = ""
	m.planFingerprint = ""
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
}

func (m *Model) launchRun(f core.Feature, approvals []core.ApprovalKind, options map[string]string) tea.Cmd {
	m.active = f
	m.phase = phaseLoading
	m.runCtx, m.runCancel = context.WithCancel(m.ctx)
	cmds := []tea.Cmd{m.runCmd(f, approvals, options, m.events, m.runCtx), waitStageCmd(m.events)}
	if m.live != nil {
		cmds = append(cmds, waitLiveCmd(m.liveCh))
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
	lines := m.composeFrame(m.renderBody())
	return strings.Join(lines, "\n") + "\n"
}

// menuWindow bounds how many feature rows the screen shows so a growing
// feature registry cannot push the content off screen. The selection window
// follows the cursor and the omitted rows are stated explicitly.
const menuWindow = 7

func (m *Model) menuView() string {
	var b strings.Builder
	start, end := 0, len(m.features)
	if end > menuWindow {
		start = m.selected - menuWindow/2
		if start < 0 {
			start = 0
		}
		if start+menuWindow > end {
			start = end - menuWindow
		}
		end = start + menuWindow
	}
	if start > 0 {
		b.WriteString(m.hintText(fmt.Sprintf(msgMenuMoreAbove, m.theme.Glyphs.Ell)))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		d := m.features[i].Descriptor()
		marker := "  "
		if i == m.selected {
			marker = "> "
		}
		// Compact drops the secondary column (the machine name) and keeps the
		// Korean title; the wide and medium layouts show both.
		if m.compact() {
			b.WriteString(marker + d.Title + "\n")
			continue
		}
		b.WriteString(marker + d.Title + " (" + d.Name + ")\n")
	}
	if end < len(m.features) {
		b.WriteString(m.hintText(fmt.Sprintf(msgMenuMoreBelow, m.theme.Glyphs.Ell)))
		b.WriteString("\n")
	}
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
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmScopeLabel, serviceScopeText(m.req.Services))
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmEffectLabel, d.Summary)
	if strings.TrimSpace(m.planText) != "" {
		b.WriteString("\n")
		b.WriteString(m.planText)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// serviceWindow bounds the visible rows so a large stack cannot push the list
// off the terminal. The cursor-centered window keeps every row reachable.
const serviceWindow = 10

func (m *Model) serviceView() string {
	var b strings.Builder
	b.WriteString(m.title(msgServiceSelectTitle))
	b.WriteString("\n\n")
	dir := m.target.Path
	if dir == "" {
		dir = msgConfirmTargetNow
	}
	fmt.Fprintf(&b, "%s: %s\n", msgConfirmTargetLabel, dir)
	if m.target.Environment != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgConfirmEnvironment, m.target.Environment)
	}
	b.WriteString("\n")
	b.WriteString(msgServiceSelectIntro)
	b.WriteString("\n\n")

	rows := len(m.serviceOptions) + 1
	start, end := 0, rows
	if rows > serviceWindow {
		start = m.serviceCursor - serviceWindow/2
		if start < 0 {
			start = 0
		}
		if start+serviceWindow > rows {
			start = rows - serviceWindow
		}
		end = start + serviceWindow
	}
	if start > 0 {
		b.WriteString(m.hintText(fmt.Sprintf(msgMenuMoreAbove, m.theme.Glyphs.Ell)))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		marker := "  "
		if i == m.serviceCursor {
			marker = "> "
		}
		checked := " "
		if i == 0 {
			if m.serviceWhole {
				checked = "x"
			}
			fmt.Fprintf(&b, "%s[%s] %s\n", marker, checked, msgServiceWhole)
			continue
		}
		svc := m.serviceOptions[i-1]
		if i-1 < len(m.serviceChecked) && m.serviceChecked[i-1] {
			checked = "x"
		}
		fmt.Fprintf(&b, "%s[%s] %s · %s\n", marker, checked, svc.Name, serviceSummary(svc))
	}
	if end < rows {
		b.WriteString(m.hintText(fmt.Sprintf(msgMenuMoreBelow, m.theme.Glyphs.Ell)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.hintText(msgServiceSelectionNote))
	return strings.TrimRight(b.String(), "\n")
}

func serviceSummary(svc core.ServiceStatus) string {
	parts := []string{serviceStateLabel(svc.State)}
	if health := serviceHealthLabel(svc.Health); health != "" {
		parts = append(parts, health)
	}
	if svc.Instances == 0 {
		parts = append(parts, msgServiceNoInstances)
	} else {
		parts = append(parts, fmt.Sprintf(msgServiceInstances, svc.Instances))
	}
	if !svc.Configured {
		parts = append(parts, msgServiceNotConfigured)
	}
	return strings.Join(parts, " · ")
}

func serviceStateLabel(state string) string {
	switch state {
	case "running":
		return msgServiceStateRunning
	case "exited":
		return msgServiceStateExited
	case "paused":
		return msgServiceStatePaused
	case "restarting":
		return msgServiceStateRestarting
	case "created":
		return msgServiceStateCreated
	case "not_created":
		return msgServiceStateNotCreated
	case "":
		return msgServiceStateUnknown
	default:
		return state
	}
}

func serviceHealthLabel(health string) string {
	switch health {
	case "", "none":
		return msgServiceHealthNone
	case "healthy":
		return msgServiceHealthy
	case "starting":
		return msgServiceHealthStarting
	case "unhealthy":
		return msgServiceUnhealthy
	default:
		return health
	}
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
