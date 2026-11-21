package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// resolveTheme produces the one theme the model injects into every renderer.
// The environment is read here, once, at construction: renderers only ever see
// the resolved value, so a redraw cannot change color mid-session.
func resolveTheme(getenv func(string) string, explicit *visual.Theme) visual.Theme {
	if explicit != nil {
		return *explicit
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	// lipgloss is Bubble Tea's own renderer; bubbletea's init already asked the
	// terminal (or fell back) and cached the answer, so this is a read, not a
	// second query.
	return visual.Resolve(visual.Detect(getenv, colorProfile(), lipgloss.HasDarkBackground()))
}

// colorProfile maps lipgloss's detected terminal profile onto the visual
// package's capability levels. It is env-based and never queries the terminal.
func colorProfile() visual.Profile {
	switch lipgloss.ColorProfile() {
	case termenv.TrueColor:
		return visual.ProfileTrueColor
	case termenv.ANSI256:
		return visual.Profile256
	case termenv.ANSI:
		return visual.Profile16
	default:
		return visual.ProfileNone
	}
}

// initialFacts seeds the header from what the caller already asked for. The
// folder name is the stack identity before an operation reports a project
// name; no Docker call is made to learn it.
func initialFacts(target core.TargetRequest) visual.Facts {
	f := visual.Facts{Environment: target.Environment}
	if target.ProjectName != "" {
		f.Project = target.ProjectName
	} else if base := projectBase(target.Path); base != "" {
		f.Project = base
	}
	return f
}

func projectBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// observeTarget records the last observed stack identity. A value that was not
// observed this time keeps the previous one: the header states facts, not
// guesses, and a failed run must not erase a known project name.
func (m *Model) observeTarget(target core.Target) {
	if target.ProjectName != "" {
		m.facts.Project = target.ProjectName
	} else if m.facts.Project == "" {
		if base := projectBase(target.ProjectDir); base != "" {
			m.facts.Project = base
		}
	}
	if target.Environment != "" {
		m.facts.Environment = target.Environment
	}
}

// observeTools records the last observed Docker/Compose facts. Nothing here
// polls: only a plan, service read or run updates them.
func (m *Model) observeTools(tools core.ToolInfo) {
	if tools.DockerVersion != "" {
		m.facts.Docker = tools.DockerVersion
	}
	if tools.ComposeVersion != "" {
		m.facts.Compose = tools.ComposeVersion
	}
	if kind := localEnvironment(tools.EndpointKind); kind != "" {
		m.facts.Local = kind
	}
}

// localEnvironment names the verified local endpoint kind. Remote endpoints
// are refused before engine access, so only local kinds get a label.
func localEnvironment(kind string) string {
	switch kind {
	case "unix", "npipe", "fd":
		return msgLocalEndpoint
	default:
		return ""
	}
}
