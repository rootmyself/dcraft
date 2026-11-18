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
// services: this ticket only runs read-only features from the TUI.
func Run(ctx context.Context, opts Options) error {
	m := NewModel(ctx, opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

type phase int

const (
	phaseNoPrimary phase = iota
	phaseLoading
	phasePlanning
	phaseConfirm
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

	phase        phase
	result       *core.Result
	currentStage *core.Stage
	events       chan core.Stage
	width        int
	height       int

	// pendingApproval and planText hold the generic confirmation state. Any
	// approval-gated feature that implements core.Planner reuses this screen;
	// no feature-specific branch is added here.
	pendingApproval core.ApprovalKind
	planText        string
}

// NewModel builds the initial screen without running anything.
func NewModel(ctx context.Context, opts Options) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &Model{ctx: ctx, engine: opts.Engine, registry: opts.Registry, target: opts.Target}
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
	m.phase = phaseLoading
	for i, f := range m.features {
		if f.Descriptor().Name == m.primary.Descriptor().Name {
			m.selected = i
			break
		}
	}
	m.events = make(chan core.Stage, 64)
	return m
}

type resultMsg struct{ res core.Result }

type stageMsg struct{ stage core.Stage }

type planMsg struct{ report core.PlanReport }

func (m *Model) Init() tea.Cmd {
	if m.phase != phaseLoading {
		return nil
	}
	return m.begin(m.active)
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
			m.result = resultFromPlan(msg.report)
			m.phase = phaseReady
			return m, nil
		}
		m.pendingApproval = m.active.Descriptor().Approval
		m.planText = msg.report.Text
		m.phase = phaseConfirm
		return m, nil
	case resultMsg:
		res := msg.res
		m.result = &res
		m.currentStage = nil
		m.phase = phaseReady
		return m, nil
	case tea.KeyMsg:
		if m.phase == phaseConfirm {
			switch msg.String() {
			case "esc", "n":
				m.declineApproval()
				return m, nil
			case "enter":
				feature := m.active
				approval := m.pendingApproval
				m.pendingApproval = ""
				m.planText = ""
				if feature == nil {
					return m, nil
				}
				return m, m.runApproved(feature, approval)
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			if m.active != nil && m.phase == phaseReady {
				return m, m.begin(m.active)
			}
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
				return m, m.begin(m.features[m.selected])
			}
			return m, nil
		}
	}
	return m, nil
}

// begin starts a feature. Approval-gated features that can describe their
// exact effect go through a read-only plan first; everything else runs
// directly, preserving the ticket 01 behavior.
func (m *Model) begin(f core.Feature) tea.Cmd {
	m.active = f
	m.result = nil
	m.currentStage = nil
	m.pendingApproval = ""
	m.planText = ""
	m.events = make(chan core.Stage, 64)
	if f != nil {
		desc := f.Descriptor()
		if desc.Approval != core.ApprovalNone {
			if _, ok := f.(core.Planner); ok {
				m.phase = phasePlanning
				return tea.Batch(m.planCmd(f, m.events), waitStageCmd(m.events))
			}
		}
	}
	m.phase = phaseLoading
	return tea.Batch(m.runCmd(f, nil, m.events), waitStageCmd(m.events))
}

func (m *Model) runApproved(f core.Feature, approval core.ApprovalKind) tea.Cmd {
	m.phase = phaseLoading
	m.result = nil
	m.currentStage = nil
	m.events = make(chan core.Stage, 64)
	return tea.Batch(m.runCmd(f, []core.ApprovalKind{approval}, m.events), waitStageCmd(m.events))
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

func resultFromPlan(report core.PlanReport) *core.Result {
	res := core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        report.Feature,
		Outcome:       report.Outcome,
		Target:        report.Target,
		Tools:         report.Tools,
		Stages:        report.Stages,
		Diagnostics:   report.Diagnostics,
	}
	if res.Outcome == "" {
		res.Outcome = core.OutcomeFailure
	}
	res.Target.Normalize()
	return &res
}

func (m *Model) runCmd(f core.Feature, approvals []core.ApprovalKind, events chan core.Stage) tea.Cmd {
	engine := m.engine
	ctx := m.ctx
	req := core.Request{
		Feature:     f.Descriptor().Name,
		Target:      m.target,
		Approvals:   approvals,
		Interactive: true,
	}
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
	titleStyle = lipgloss.NewStyle().Bold(true)
	hintStyle  = lipgloss.NewStyle().Faint(true)
)

func (m *Model) View() string {
	var b strings.Builder
	title := msgApp
	if m.active != nil {
		title += " · " + m.active.Descriptor().Title
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
		b.WriteString(titleStyle.Render(msgConfirmTitle))
		b.WriteString("\n\n")
		if m.active != nil {
			d := m.active.Descriptor()
			fmt.Fprintf(&b, "작업: %s (%s)\n", d.Title, d.Name)
		}
		if m.pendingApproval != core.ApprovalNone {
			fmt.Fprintf(&b, "승인: %s (%s)\n", m.pendingApproval.KoreanLabel(), m.pendingApproval)
		}
		b.WriteString("\n")
		b.WriteString(m.planText)
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render(msgHintConfirm))
		b.WriteString("\n")
	case phaseLoading:
		b.WriteString(msgWorking)
		b.WriteString("\n")
		if m.currentStage != nil {
			b.WriteString(view.StageList([]core.Stage{*m.currentStage}))
			b.WriteString("\n")
		}
	default:
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

	if len(m.features) > 1 {
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

	b.WriteString("\n")
	b.WriteString(hintStyle.Render(m.hints()))
	return b.String()
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
	if m.phase == phaseNoPrimary {
		return msgHintQuit
	}
	if m.phase == phaseConfirm {
		return msgHintConfirm
	}
	if m.phase == phasePlanning || m.phase == phaseLoading {
		return msgHintQuit
	}
	if len(m.features) > 1 {
		return msgHintMenu
	}
	return msgHintSingle
}
