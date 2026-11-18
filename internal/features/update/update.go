// Package update registers the flat `dcraft update` command. It is a thin
// registration unit: preview, build/pull/apply, readiness verification and the
// previous-good record are owned by internal/update so CLI and TUI share the
// same safety rules.
package update

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	behavior "github.com/rootmyself/dcraft/internal/update"
)

// Feature is the registered update command.
type Feature struct {
	control *behavior.Controller
}

func New(control *behavior.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "update",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		RecordsHistory:   true,
		SupportsServices: true,
		Flags: []core.Flag{
			{Name: "no-cache", Kind: core.FlagBool, Usage: msgFlagNoCache},
			{Name: "build-pull", Kind: core.FlagBool, Usage: msgFlagBuildPull},
			{Name: "no-pull", Kind: core.FlagBool, Usage: msgFlagNoPull},
			{Name: "force-recreate", Kind: core.FlagBool, Usage: msgFlagForceRecreate},
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

func (f *Feature) Present(res core.Result, width int) string {
	return behavior.Present(res, width)
}
