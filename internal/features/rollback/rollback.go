// Package rollback registers the flat `dcraft rollback` command. It is a thin
// registration unit: the record, prechecks, apply and readiness verification
// are owned by internal/rollback so CLI and TUI share the same safety rules.
package rollback

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	behavior "github.com/rootmyself/dcraft/internal/rollback"
)

// Feature is the registered rollback command.
type Feature struct {
	control *behavior.Controller
}

func New(control *behavior.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:           "rollback",
		Title:          msgTitle,
		Summary:        msgSummary,
		Usage:          msgUsage,
		Approval:       core.ApprovalPreviousGood,
		RecordsHistory: true,
		Flags: []core.Flag{
			{Name: "timeout", Kind: core.FlagString, Default: "60", Usage: msgFlagTimeout},
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
	opts, err := behavior.ParseOptions(in)
	if err != nil {
		return nil, err
	}
	return f.control.Run(ctx, in, opts)
}

// Plan is the read-only preview shown before the previous_version approval is
// collected. It changes nothing and never claims an exact future-state diff.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	if f.control == nil {
		return core.Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoController,
			NextActions: []string{msgNoControllerNext},
		})
	}
	plan, err := f.control.Preview(ctx, in)
	if err != nil {
		return core.Plan{}, err
	}
	return core.Plan{Text: plan.Text}, nil
}

func (f *Feature) Present(res core.Result, width int) string {
	return behavior.Present(res, width)
}
