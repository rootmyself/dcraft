// Package down implements the flat `dcraft down` command. Down is
// project-wide and never removes data volumes in ordinary use.
package down

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/lifecycle"
)

// Feature is the registered down command.
type Feature struct {
	control *lifecycle.Controller
}

func New(control *lifecycle.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:           "down",
		Title:          msgTitle,
		Summary:        msgSummary,
		Usage:          msgUsage,
		Section:        core.SectionOperate,
		Destructive:    true,
		RecordsHistory: true,
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
	return f.control.Run(ctx, in, lifecycle.ActionDown)
}

func (f *Feature) Present(res core.Result, width int) string {
	return lifecycle.Present(res, width)
}
