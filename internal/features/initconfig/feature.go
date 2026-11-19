// Package initconfig registers the flat `dcraft init` command. The generator,
// validator and atomic save live in internal/initconfig so the CLI and the TUI
// wizard use exactly the same rules.
package initconfig

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
	initimpl "github.com/rootmyself/dcraft/internal/initconfig"
)

// Feature is the registered first-run configuration command.
type Feature struct {
	control *initimpl.Controller
}

func New(control *initimpl.Controller) *Feature { return &Feature{control: control} }

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:    "init",
		Title:   initimpl.Title(),
		Summary: initimpl.Summary(),
		Usage:   initimpl.Usage(),
		// Generation and `docker compose config` validation do not need a
		// reachable engine; the narrow requirement keeps init usable while the
		// engine is stopped. A missing Compose is reported by the validation
		// stage with its own diagnostic.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
		Wizard:         true,
		Flags: []core.Flag{
			{
				Name: "from", Kind: core.FlagString, Required: true,
				Usage: "구성 방식: dockerfile 또는 image (필수). Dockerfile 방식은 빌드 컨텍스트와 Dockerfile을, 이미지 방식은 이미지 참조를 사용합니다.",
			},
			{
				Name: "service-name", Kind: core.FlagString, Required: true,
				Usage: "생성할 Compose 서비스 이름 (필수, 예: app). 대화형 화면에서는 app을 제안하지만 미리보기에서 확인해야 합니다.",
			},
			{
				Name: "context", Kind: core.FlagString,
				Usage: "Dockerfile 방식의 빌드 컨텍스트 폴더 (기본: 현재 프로젝트 폴더). 상대 경로는 프로젝트 폴더 기준입니다.",
			},
			{
				Name: "dockerfile", Kind: core.FlagString,
				Usage: "Dockerfile 방식의 Dockerfile 경로 (기본: Dockerfile). 빌드 컨텍스트 기준이며 실제 파일이 있어야 합니다.",
			},
			{
				Name: "image", Kind: core.FlagString,
				Usage: "이미지 방식의 이미지 참조 (예: nginx:1.27-alpine). 태그나 digest를 포함할 수 있으며 이미지를 pull하지 않습니다.",
			},
			{
				Name: "output", Shorthand: "o", Kind: core.FlagString,
				Usage: "저장할 Compose 파일 경로 (기본: compose.yaml). 상대 경로는 프로젝트 폴더 기준입니다. 기존 파일 덮어쓰기는 --approve file_overwrite가 필요합니다.",
			},
			{
				Name: "port", Kind: core.FlagList,
				Usage: "공개할 포트 (반복 지정 가능, 예: --port 8080:80). 이미지나 Dockerfile에서 포트를 추측하지 않으며 직접 지정한 값만 기록합니다.",
			},
			{
				Name: "volume", Kind: core.FlagList,
				Usage: "볼륨/바인드 마운트 (반복 지정 가능, 예: --volume ./data:/data). 이름 있는 볼륨은 최상위 volumes에 선언합니다.",
			},
			{
				Name: "env-var", Kind: core.FlagList,
				Usage: "환경변수 KEY=VALUE (반복 지정 가능). 값은 compose.yaml에 평문으로 저장되므로 비밀값은 dcraft secrets 참조를 사용하세요.",
			},
			{
				Name: "command", Kind: core.FlagString,
				Usage: "이미지 기본 CMD를 덮어쓸 명령 (선택). 지정하지 않으면 이미지/Dockerfile 기본값을 사용합니다.",
			},
			{
				Name: "restart", Kind: core.FlagString,
				Usage: "재시작 정책: no, always, unless-stopped, on-failure, on-failure:횟수 (선택). 지정하지 않으면 Compose 기본값을 사용합니다.",
			},
		},
	}
}

// Run implements core.Feature. The wizard "steps" run only builds questions;
// "apply" and the ordinary CLI path share the same generator and saver.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.control == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "최초 구성기를 준비하지 못했습니다.",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."},
		})
	}
	if in.Option("wizard") == "steps" {
		return f.control.WizardSteps(ctx, in)
	}
	return f.control.Run(ctx, in)
}

// Plan is the CLI's read-only preview before the ordinary (no-approval) save.
// It shows the exact file and effects and writes nothing.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	if f.control == nil {
		return core.Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "최초 구성기를 준비하지 못했습니다.",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."},
		})
	}
	return f.control.Plan(ctx, in)
}

// RequiredApproval implements core.ConditionalApproval: file_overwrite is
// needed only when the chosen destination already exists.
func (f *Feature) RequiredApproval(ctx context.Context, in core.Invocation) (core.ApprovalKind, error) {
	if f.control == nil {
		return core.ApprovalNone, nil
	}
	return f.control.RequiredApproval(ctx, in)
}

// WizardPreview implements core.Wizard. It describes the generated content and
// effects without saving.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	if f.control == nil {
		return "", core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "최초 구성기를 준비하지 못했습니다.",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."},
		})
	}
	return f.control.WizardPreview(answers)
}

func (f *Feature) Present(res core.Result, width int) string {
	return initimpl.Present(res, width)
}

// CanceledDiagnostic describes the atomic save window honestly.
func (f *Feature) CanceledDiagnostic() core.Diagnostic {
	return initimpl.CanceledDiagnostic()
}
