// Package restore registers the flat `dcraft restore` command. Behavior,
// Korean guidance and the safe replacement contract are owned by
// internal/restore so CLI and TUI share the same safety rules.
package restore

import (
	"context"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	restoreimpl "github.com/rootmyself/dcraft/internal/restore"
)

// Feature is the registered restore command.
type Feature struct {
	control *restoreimpl.Controller
}

func New(control *restoreimpl.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:           "restore",
		Title:          restoreimpl.MsgTitle,
		Summary:        restoreimpl.MsgSummary,
		Usage:          restoreimpl.MsgUsage,
		Section:        core.SectionData,
		Destructive:    true,
		Approval:       core.ApprovalDataRestore,
		RecordsHistory: true,
		Wizard:         true,
		Flags: []core.Flag{
			{
				Name:     "backup",
				Kind:     core.FlagString,
				Required: true,
				Usage:    "복원할 완료된 dcraft 백업 v1 폴더 (필수, 예: ./dcraft-backups/dcraft-proj-volume-data-20260101T000000Z).",
			},
			{
				Name:  "volume",
				Kind:  core.FlagString,
				Usage: "복원 대상 볼륨 키. 생략하면 백업에 기록된 볼륨을 사용합니다. 백업의 프로젝트·실행환경과 다른 대상은 항상 거부합니다. 연결 폴더 백업에는 사용할 수 없습니다.",
			},
			{
				Name:  "bind",
				Kind:  core.FlagString,
				Usage: "복원 대상 연결 폴더. 현재 스택의 Compose 파일이 선언한 폴더(경로 또는 마운트 대상)만 지정할 수 있습니다. 생략하면 백업에 기록된 경로와 같은 선언 폴더를 사용합니다.",
			},
			{
				Name:  "safety-output",
				Kind:  core.FlagString,
				Usage: "현재 데이터의 안전 백업을 만들 별도 호스트 폴더. 기본은 백업 폴더 옆의 dcraft-safety입니다.",
			},
			{
				Name:  "allow-path-mismatch",
				Kind:  core.FlagBool,
				Usage: "연결 폴더 백업에 기록된 경로와 현재 스택의 선언 폴더가 달라도 --bind로 지정한 폴더에 명시적으로 매핑합니다. 경고를 표시하며 기본은 거부입니다.",
			},
		},
	}
}

func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.control == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     restoreimpl.MsgNoBackend,
			NextActions: []string{restoreimpl.MsgNoBackendAction},
		})
	}
	switch in.Option("wizard") {
	case "steps":
		return f.control.WizardSteps(ctx, in)
	case "apply":
		in = normalizeWizardInvocation(in)
	}
	return f.control.Run(ctx, in)
}

// normalizeWizardInvocation maps the TUI's single target answer onto the same
// option names the CLI uses. The command still validates and resolves
// everything through the shared controller.
func normalizeWizardInvocation(in core.Invocation) core.Invocation {
	if in.OptionLists == nil {
		in.OptionLists = map[string][]string{}
	}
	if in.Options == nil {
		in.Options = map[string]string{}
	}
	volumes, binds := restoreimpl.ParseWizardTarget(in.Option("target"))
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
	if strings.TrimSpace(in.Option("volume")) == "" && len(volumes) > 0 {
		in.Options["volume"] = volumes[0]
	}
	if strings.TrimSpace(in.Option("bind")) == "" && len(binds) > 0 {
		in.Options["bind"] = binds[0]
	}
	if strings.TrimSpace(in.Option("safety-output")) == "" {
		if safety := strings.TrimSpace(in.Option("safety")); safety != "" {
			in.Options["safety-output"] = safety
		}
	}
	return in
}

// Plan shows the verified target, overwrite impact, interruption and safety
// backup location before the generic data_restore approval is collected.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	if f.control == nil {
		return core.Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     restoreimpl.MsgNoBackend,
			NextActions: []string{restoreimpl.MsgNoBackendAction},
		})
	}
	if in.Option("wizard") == "apply" {
		// The generic plan runs before approval and must see the same selected
		// target the later apply run will use.
		in = normalizeWizardInvocation(in)
	}
	return f.control.Plan(ctx, in)
}

func (f *Feature) WizardSteps(ctx context.Context, in core.Invocation) (core.WizardSpec, error) {
	if f.control == nil {
		return core.WizardSpec{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     restoreimpl.MsgNoBackend,
			NextActions: []string{restoreimpl.MsgNoBackendAction},
		})
	}
	return f.control.WizardSteps(ctx, in)
}

func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	if f.control == nil {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     restoreimpl.MsgNoBackend,
			NextActions: []string{restoreimpl.MsgNoBackendAction},
		})
	}
	return f.control.WizardPreview(answers)
}

func (f *Feature) Present(res core.Result, width int) string {
	return restoreimpl.Present(res, width)
}

func (f *Feature) CanceledDiagnostic() core.Diagnostic {
	if f.control == nil {
		return core.Diagnostic{Message: restoreimpl.MsgNoBackend, NextActions: []string{restoreimpl.MsgNoBackendAction}}
	}
	return f.control.CanceledDiagnostic()
}
