// Package stop implements the flat `dcraft stop` command. Stopping keeps
// containers and data; `dcraft up` resumes the stack.
package stop

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
)

// Feature is the registered stop command.
type Feature struct {
	control *lifecycle.Controller
}

func New(control *lifecycle.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "stop",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		RecordsHistory:   true,
		SupportsServices: true,
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
	return f.control.Run(ctx, in, lifecycle.ActionStop)
}

func (f *Feature) Present(res core.Result, width int) string {
	return lifecycle.Present(res, width)
}

// Document implements core.ViewPresenter. The TUI renders the structured
// version; the CLI keeps Present above, unchanged.
func (f *Feature) Document(res core.Result) core.Document {
	return lifecycle.Document(res)
}
