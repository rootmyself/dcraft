// Package up implements the flat `dcraft up` command. It is a thin
// registration unit: all behavior is owned by internal/lifecycle so CLI and TUI
// share the same safety rules.
package up

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
)

// Feature is the registered up command.
type Feature struct {
	control *lifecycle.Controller
}

func New(control *lifecycle.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "up",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		RecordsHistory:   true,
		SupportsServices: true,
		Hooks:            core.HookUpUpdate,
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
	return f.control.Run(ctx, in, lifecycle.ActionUp)
}

func (f *Feature) Present(res core.Result, width int) string {
	return lifecycle.Present(res, width)
}
