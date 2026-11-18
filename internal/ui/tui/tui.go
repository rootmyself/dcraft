// Package tui renders the interactive screen. It is assembled entirely from
// the feature registry and invokes features only through the common engine.
package tui

import (
	"context"
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

func (m *Model) Init() tea.Cmd {
	if m.phase != phaseLoading {
		return nil
	}
	return tea.Batch(m.runCmd(m.active, m.events), waitStageCmd(m.events))
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
		if m.phase == phaseLoading {
			return m, waitStageCmd(m.events)
		}
		return m, nil
	case resultMsg:
		res := msg.res
		m.result = &res
		m.currentStage = nil
		m.phase = phaseReady
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			if m.active != nil && m.phase != phaseLoading {
				return m, m.begin(m.active)
			}
			return m, nil
		case "up", "k":
			if len(m.features) > 1 && m.phase != phaseLoading {
				m.selected = (m.selected - 1 + len(m.features)) % len(m.features)
			}
			return m, nil
		case "down", "j":
			if len(m.features) > 1 && m.phase != phaseLoading {
				m.selected = (m.selected + 1) % len(m.features)
			}
			return m, nil
		case "enter":
			if m.phase != phaseLoading && len(m.features) > 0 {
				return m, m.begin(m.features[m.selected])
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) begin(f core.Feature) tea.Cmd {
	m.active = f
	m.phase = phaseLoading
	m.result = nil
	m.currentStage = nil
	m.events = make(chan core.Stage, 64)
	return tea.Batch(m.runCmd(f, m.events), waitStageCmd(m.events))
}

func (m *Model) runCmd(f core.Feature, events chan core.Stage) tea.Cmd {
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
	if len(m.features) > 1 {
		return msgHintMenu
	}
	return msgHintSingle
}
