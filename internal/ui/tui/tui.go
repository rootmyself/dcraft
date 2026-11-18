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
	phaseLoadingEnvironments
	phaseSelectingEnvironment
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

	envs        []core.Environment
	envSelected int

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
	m := &Model{ctx: ctx, engine: opts.Engine, registry: opts.Registry, target: opts.Target, envSelected: -1}
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

type environmentListMsg struct {
	envs []core.Environment
	err  error
}

func (m *Model) Init() tea.Cmd {
	switch m.phase {
	case phaseLoadingEnvironments:
		return m.listEnvironmentsCmd()
	case phaseLoading:
		return tea.Batch(m.runCmd(m.active, m.events), waitStageCmd(m.events))
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
		if m.phase == phaseLoading {
			return m, waitStageCmd(m.events)
		}
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

func (m *Model) listEnvironmentsCmd() tea.Cmd {
	engine := m.engine
	ctx := m.ctx
	req := m.target
	return func() tea.Msg {
		envs, _, err := engine.Environments(ctx, req)
		return environmentListMsg{envs: envs, err: err}
	}
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
	if m.phase == phaseSelectingEnvironment {
		return msgHintEnvSelect
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
