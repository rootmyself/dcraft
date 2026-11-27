// Package restart implements the flat `dcraft restart` command. Restart does
// not apply new configuration or images; `dcraft up` is the configuration
// path.
package restart

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
)

// Feature is the registered restart command.
type Feature struct {
	control *lifecycle.Controller
}

func New(control *lifecycle.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "restart",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		Section:          core.SectionOperate,
		RecordsHistory:   true,
		SupportsServices: true,
		Menu: []core.MenuEntry{
			{Kind: core.MenuRun, Feature: "restart", Label: msgMenuLabel, Summary: msgMenuSummary, Order: 1},
		},
	}
}

func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.control == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoController,
			NextActions: []string{msgNoControllerNext},
		})
	}
	return f.control.Run(ctx, in, lifecycle.ActionRestart)
}

func (f *Feature) Present(res core.Result, width int) string {
	return lifecycle.Present(res, width)
}

// Document implements core.ViewPresenter. The TUI renders the structured
// version; the CLI keeps Present above, unchanged.
func (f *Feature) Document(res core.Result) core.Document {
	return lifecycle.Document(res)
}
