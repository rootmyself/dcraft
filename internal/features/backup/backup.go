// Package backup registers the flat `dcraft backup` command. Behavior,
// Korean guidance and the archive contract are owned by internal/backup so the
// CLI and TUI share the same safety rules.
package backup

import (
	"context"
	"strings"

	backupimpl "github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
)

// Feature is the registered backup command.
type Feature struct {
	control *backupimpl.Controller
}

func New(control *backupimpl.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:           "backup",
		Title:          backupimpl.MsgTitle,
		Summary:        backupimpl.MsgSummary,
		Usage:          backupimpl.MsgUsage,
		RecordsHistory: true,
		Wizard:         true,
		Flags: []core.Flag{
			{
				Name:      "output",
				Shorthand: "o",
				Kind:      core.FlagString,
				Required:  true,
				Usage:     "백업을 저장할 폴더 (필수, 예: ./dcraft-backups). 상대 경로는 현재 작업 폴더 기준이며 dcraft는 숨은 기본 위치를 선택하지 않습니다.",
			},
			{
				Name:     "volume",
				Kind:     core.FlagList,
				Required: true,
				Usage:    "백업할 데이터 볼륨 키 (반복 지정 가능, 예: --volume data). 대화형 화면에서는 목록에서 선택합니다.",
			},
		},
	}
}

func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.control == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     backupimpl.MsgNoBackend,
			NextActions: []string{backupimpl.MsgNoBackendAction},
		})
	}
	switch in.Option("wizard") {
	case "steps":
		return f.control.WizardSteps(ctx, in)
	case "apply":
		// The TUI wizard answers use the same names as the CLI flags. A
		// single selected volume becomes the one-element option list.
		if len(in.OptionLists["volume"]) == 0 {
			if volume := strings.TrimSpace(in.Option("volume")); volume != "" {
				if in.OptionLists == nil {
					in.OptionLists = map[string][]string{}
				}
				in.OptionLists["volume"] = []string{volume}
			}
		}
	}
	return f.control.Run(ctx, in)
}

// Plan lets the interactive confirmation show the selected volumes, affected
// services, prior state and destination before anything is stopped.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	if f.control == nil {
		return core.Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     backupimpl.MsgNoBackend,
			NextActions: []string{backupimpl.MsgNoBackendAction},
		})
	}
	return f.control.Plan(ctx, in)
}

// WizardPreview implements core.Wizard. It describes the effect of the
// answers collected by the TUI. The real consumer check and prior-state read
// happen again inside Run before any service is stopped.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	if f.control == nil {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     backupimpl.MsgNoBackend,
			NextActions: []string{backupimpl.MsgNoBackendAction},
		})
	}
	return f.control.WizardPreview(answers), nil
}

func (f *Feature) Present(res core.Result, width int) string {
	return backupimpl.Present(res, width)
}
