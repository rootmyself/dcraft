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
		Section:        core.SectionData,
		RecordsHistory: true,
		Wizard:         true,
		Flags: []core.Flag{
			{
				Name:      "output",
				Shorthand: "o",
				Kind:      core.FlagString,
				Required:  true,
				Usage:     "백업을 저장할 폴더 (필수, 예: ./dcraft-backups). 상대 경로는 현재 작업 폴더 기준이며 dcraft는 숨은 기본 위치를 선택하지 않음.",
			},
			{
				Name:     "volume",
				Kind:     core.FlagList,
				Required: false,
				Usage:    "백업할 이름 있는 데이터 볼륨 키 (반복 지정 가능, 예: --volume data). --bind와 함께 사용 가능.",
			},
			{
				Name:     "bind",
				Kind:     core.FlagList,
				Required: false,
				Usage:    "백업할 호스트 연결 폴더 (반복 지정 가능, 예: --bind /srv/app/data). 현재 스택의 Compose 파일이 선언한 폴더만 지정 가능.",
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
		// The TUI wizard answers use the same names as the CLI flags. The
		// mixed "sources" answer carries volume:<key> and bind:<path> lines;
		// it becomes the two repeatable option lists on the same Run path.
		if in.OptionLists == nil {
			in.OptionLists = map[string][]string{}
		}
		volumes, binds := backupimpl.ParseWizardSources(in.Option("sources"))
		if len(in.OptionLists["volume"]) == 0 {
			if len(volumes) > 0 {
				in.OptionLists["volume"] = volumes
			} else if volume := strings.TrimSpace(in.Option("volume")); volume != "" {
				in.OptionLists["volume"] = []string{volume}
			}
		}
		if len(in.OptionLists["bind"]) == 0 && len(binds) > 0 {
			in.OptionLists["bind"] = binds
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
