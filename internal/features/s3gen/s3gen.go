// Package s3gen registers the flat `dcraft generate-s3-hook` command. It owns
// the guided S3 inputs, the preview and the save step. It never contacts S3,
// never runs the AWS CLI, never registers a hook and never grants script trust:
// registration and execution remain the explicit hooks/script_trust decisions.
package s3gen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/generators"
	s3gen "github.com/rootmyself/dcraft/internal/generators/s3"
)

// Feature is the registered generator command.
type Feature struct {
	// projectDir is captured by the wizard "steps" run so WizardPreview can
	// resolve relative paths; the wizard interface only receives answers.
	projectDir string
}

func New() *Feature { return &Feature{} }

// Payload is the machine-readable result, published as data.generator. It
// contains no credentials and no script body.
type Payload struct {
	Generator Report `json:"generator"`
}

// Report describes what was written. Registered is always false: registration
// is a separate explicit `dcraft hooks --add` decision.
type Report struct {
	Action              string   `json:"action"`
	Script              string   `json:"script"`
	ScriptPath          string   `json:"script_path"`
	Destination         string   `json:"destination"`
	DestinationRelative string   `json:"destination_relative,omitempty"`
	Source              string   `json:"source"`
	Mode                string   `json:"mode"`
	Profile             string   `json:"aws_profile,omitempty"`
	SHA256              string   `json:"sha256"`
	Created             bool     `json:"created"`
	Replaced            bool     `json:"replaced"`
	Registered          bool     `json:"registered"`
	TrustRequired       bool     `json:"trust_required"`
	NextCommands        []string `json:"next_commands"`
	Notices             []string `json:"notices"`
}

// Descriptor implements core.Feature. Saving a generated script uses the
// independent file_overwrite approval for both a new file and a replacement:
// non-interactive callers must pass it explicitly, and the TUI preview screen
// is the confirmation that supplies it. It is never reused as script_trust.
func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:    "generate-s3-hook",
		Title:   msgTitle,
		Summary: msgSummary,
		Usage:   msgUsage,
		// Saving a project file is a local-config edit. Only the Docker CLI
		// presence is required by the published preflight contract; the
		// generated script needs aws, but generation does not.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
		Approval:       core.ApprovalFileOverwrite,
		Wizard:         true,
		Flags: []core.Flag{
			{Name: "source", Kind: core.FlagString, Required: true, Usage: msgFlagSource},
			{Name: "mode", Kind: core.FlagString, Required: true, Usage: msgFlagMode},
			{Name: "dest", Kind: core.FlagString, Required: true, Usage: msgFlagDest},
			{Name: "output", Kind: core.FlagString, Required: true, Usage: msgFlagOut},
			{Name: "aws-profile", Kind: core.FlagString, Usage: msgFlagAwsProfile},
		},
	}
}

// Run implements core.Feature. Options["wizard"]="steps" returns the guided
// flow; "apply" saves the collected answers through the same path as CLI flags.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.projectDir = in.Target.ProjectDir
	switch in.Option("wizard") {
	case "steps":
		return f.wizardSteps(), nil
	case "apply":
		return f.generate(ctx, in)
	}
	return f.generate(ctx, in)
}

func (f *Feature) build(in core.Invocation) (s3gen.Generated, error) {
	layout, err := generators.ResolveLayout(in.Target.ProjectDir, in.Option("dest"), in.Option("output"))
	if err != nil {
		return s3gen.Generated{}, inputError(err)
	}
	gen, err := s3gen.Build(s3gen.Input{
		Layout:  layout,
		Source:  in.Option("source"),
		Mode:    s3gen.Mode(in.Option("mode")),
		Profile: in.Option("aws-profile"),
	})
	if err != nil {
		return s3gen.Generated{}, inputError(err)
	}
	return gen, nil
}

func (f *Feature) generate(ctx context.Context, in core.Invocation) (any, error) {
	gen, err := core.RunStageValue(in.Reporter, "validate", msgStageValidate, func() (s3gen.Generated, error) {
		return f.build(in)
	})
	if err != nil {
		return nil, err
	}
	created, err := core.RunStageValue(in.Reporter, "write", msgStageWrite, func() (bool, error) {
		return generators.WriteScript(gen.Layout.ScriptPath, gen.Content)
	})
	if err != nil {
		return nil, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryGeneratorFailed,
			Severity:    core.SeverityError,
			Message:     "스크립트 파일을 저장하지 못했습니다.",
			Detail:      err.Error(),
			NextActions: []string{msgWriteActionOne, msgWriteActionTwo},
		})
	}
	return f.payload(gen, created), nil
}

func (f *Feature) payload(gen s3gen.Generated, created bool) Payload {
	script := displayScript(gen.Layout)
	report := Report{
		Action:              "generate-s3-hook",
		Script:              script,
		ScriptPath:          gen.Layout.ScriptPath,
		Destination:         gen.Layout.Destination,
		DestinationRelative: gen.Layout.DestinationRel,
		Source:              gen.Source,
		Mode:                string(gen.Mode),
		Profile:             gen.Profile,
		SHA256:              gen.SHA256,
		Created:             created,
		Replaced:            !created,
		Registered:          false,
		TrustRequired:       true,
		NextCommands: []string{
			"dcraft hooks --add --stage pre --script " + shellQuote(script),
			"dcraft up --approve script_trust",
		},
		Notices: []string{
			msgDoneNotRun,
			msgDoneTrust,
			msgDoneGeneration,
			msgDoneAWS,
		},
	}
	if gen.Mode == s3gen.ModeTar {
		report.Notices = append(report.Notices, msgDoneTarSafe)
	} else {
		report.Notices = append(report.Notices, msgDoneNoDelete)
	}
	if !created {
		report.Notices = append([]string{msgPlanReplaceFile}, report.Notices...)
	}
	if !gen.Layout.ScriptInsideProject {
		report.Notices = append(report.Notices, msgDoneOutside)
	}
	return Payload{Generator: report}
}

// Plan implements core.Planner so the CLI shows the exact effects before the
// save. It writes nothing and contacts no remote.
func (f *Feature) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	gen, err := f.build(in)
	if err != nil {
		return core.Plan{}, err
	}
	return core.Plan{Text: previewText(gen)}, nil
}

func (f *Feature) wizardSteps() core.WizardSpec {
	return core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{Name: "source", Title: msgWizardSourceTitle, Prompt: msgWizardSourcePrompt, AllowCustom: true, CustomHint: msgWizardSourceHint},
			{
				Name: "mode", Title: msgWizardModeTitle, Prompt: msgWizardModePrompt, Default: "sync",
				Options: []core.WizardOption{
					{Value: "sync", Label: msgWizardSyncLabel},
					{Value: "tar", Label: msgWizardTarLabel},
				},
			},
			{Name: "dest", Title: msgWizardDestTitle, Prompt: msgWizardDestPrompt, AllowCustom: true, Default: "deploy", CustomHint: msgWizardDestHint},
			{Name: "output", Title: msgWizardOutTitle, Prompt: msgWizardOutPrompt, AllowCustom: true, Default: "scripts/s3-sync.sh", CustomHint: msgWizardOutHint},
		},
	}
}

// WizardPreview implements core.Wizard. It is the confirmation screen before
// the same engine Run path saves the file.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	project := f.projectDir
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", inputError(err)
		}
		project = cwd
	}
	layout, err := generators.ResolveLayout(project, answers["dest"], answers["output"])
	if err != nil {
		return "", inputError(err)
	}
	gen, err := s3gen.Build(s3gen.Input{
		Layout:  layout,
		Source:  answers["source"],
		Mode:    s3gen.Mode(answers["mode"]),
		Profile: answers["aws-profile"],
	})
	if err != nil {
		return "", inputError(err)
	}
	return previewText(gen), nil
}

func previewText(gen s3gen.Generated) string {
	layout := gen.Layout
	save := msgPlanNewFile
	if gen.Existed {
		save = msgPlanReplaceFile
	}
	script := displayScript(layout)
	var b strings.Builder
	b.WriteString(msgPlanHeader)
	b.WriteString("\n\n")
	b.WriteString(msgPlanNoNetwork)
	b.WriteString("\n")
	b.WriteString(msgPlanNoRegister)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanNext)
	fmt.Fprintf(&b, "  dcraft hooks --add --stage pre --script %s\n", shellQuote(script))
	fmt.Fprintf(&b, "  dcraft up --approve script_trust\n")
	fmt.Fprintf(&b, "  스크립트 내용이 바뀌면 새 script_trust 승인이 필요합니다.\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanApproval)
	fmt.Fprintf(&b, "  이 작업은 file_overwrite 승인이 필요합니다(새 파일 생성 또는 기존 파일 교체). 자동화에서는 --approve file_overwrite를 명시하세요.\n")
	fmt.Fprintf(&b, "  생성 승인과 첫 실행의 script_trust 승인은 별개입니다.\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanInputs)
	fmt.Fprintf(&b, "  S3 원본: %s\n", gen.Source)
	fmt.Fprintf(&b, "  방식: %s\n", modeLabel(gen.Mode))
	fmt.Fprintf(&b, "  받을 폴더: %s\n", layout.Destination)
	if layout.DestinationRel != layout.Destination {
		fmt.Fprintf(&b, "    프로젝트 기준: %s\n", layout.DestinationRel)
	}
	fmt.Fprintf(&b, "  스크립트: %s\n", layout.ScriptPath)
	if layout.ScriptRel != layout.ScriptPath {
		fmt.Fprintf(&b, "    프로젝트 기준: %s\n", layout.ScriptRel)
	}
	fmt.Fprintf(&b, "  프로젝트 폴더: %s\n", layout.ProjectDir)
	if gen.Profile != "" {
		fmt.Fprintf(&b, "  AWS 프로필: %s (기존 AWS CLI 구성의 프로필 이름, 비밀값 아님)\n", gen.Profile)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanExecution)
	fmt.Fprintf(&b, "  훅으로 등록하면 dcraft가 프로젝트 폴더에서 실행합니다. 스크립트는 자기 위치에서 프로젝트 폴더를 계산합니다.\n")
	fmt.Fprintf(&b, "  %s\n", save)
	fmt.Fprintf(&b, "  실행 권한 0755로 저장합니다. .dcraft.yaml은 이 단계에서 수정하지 않습니다.\n")
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanEffects)
	b.WriteString(commandPreview(gen))
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanPrereq)
	b.WriteString(msgPlanPrereqBody)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanFailure)
	b.WriteString(msgPlanFailureBody)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanEdit)
	b.WriteString(msgPlanEditBody)
	return strings.TrimRight(b.String(), "\n")
}

func commandPreview(gen s3gen.Generated) string {
	profile := ""
	if gen.Profile != "" {
		profile = " --profile " + shellQuote(gen.Profile)
	}
	source := shellQuote(gen.Source)
	dest := shellQuote(gen.Layout.Destination)
	var b strings.Builder
	if gen.Mode == s3gen.ModeTar {
		fmt.Fprintf(&b, "  - 다음 단계를 임시 폴더에서 수행합니다(프로필 지정 시 --profile 추가).\n")
		fmt.Fprintf(&b, "      aws s3 cp %s <임시 폴더>/artifact.tar.gz --only-show-errors --no-progress%s\n", source, profile)
		fmt.Fprintf(&b, "      gzip -t <임시 폴더>/artifact.tar.gz\n")
		fmt.Fprintf(&b, "      tar -tzf <임시 폴더>/artifact.tar.gz\n")
		fmt.Fprintf(&b, "      tar -tvzf <임시 폴더>/artifact.tar.gz\n")
		fmt.Fprintf(&b, "      tar -xzf <임시 폴더>/artifact.tar.gz -C <빈 임시 폴더>\n")
		fmt.Fprintf(&b, "      cp -R <빈 임시 폴더>/. %s/\n", dest)
		b.WriteString("  - 절대 경로, .. 경로, 밖을 가리키는 심볼릭/하드 링크, 지원하지 않는 파일 형식을 거부합니다.\n")
		b.WriteString("  - 압축은 빈 임시 폴더에만 풀고, 검증 후 받을 폴더로 복사합니다. 받을 폴더에 심볼릭 링크가 있으면 복사하지 않습니다.\n")
		b.WriteString("  - 원본에 없는 받을 폴더의 기존 파일은 삭제하지 않습니다.\n")
		b.WriteString("  - 임시 파일과 임시 폴더는 스크립트 종료 시 정리합니다.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  - 기존 AWS CLI 구성으로 다음 명령을 실행합니다(프로필 지정 시 --profile 추가).\n")
	fmt.Fprintf(&b, "      aws s3 sync %s %s --only-show-errors --no-progress%s\n", source, dest, profile)
	b.WriteString("  - 원본에 없는 받을 폴더의 기존 파일을 삭제하는 삭제 동기화 옵션은 넣지 않습니다.\n")
	b.WriteString("  - 받을 폴더의 기존 파일은 그대로 남습니다. 받을 폴더가 프로젝트 밖을 가리키거나 심볼릭 링크면 중단합니다.\n")
	return b.String()
}

func modeLabel(mode s3gen.Mode) string {
	if mode == s3gen.ModeTar {
		return "tar.gz 다운로드·검증·해제 (tar)"
	}
	return "디렉터리 동기화 (sync, 삭제 동기화 없음)"
}

func displayScript(layout generators.Layout) string {
	if layout.ScriptInsideProject && layout.ScriptRel != "" {
		return layout.ScriptRel
	}
	return layout.ScriptPath
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func inputError(err error) *core.Error {
	var input *generators.InputError
	if errors.As(err, &input) {
		detail := input.Field
		if input.Detail != "" {
			detail = input.Field + "=" + input.Detail
		}
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryGeneratorInputInvalid,
			Severity:    core.SeverityError,
			Message:     input.Message,
			Detail:      detail,
			NextActions: []string{msgInputActionOne, msgInputActionTwo},
		})
	}
	return core.FailCause(err, core.Diagnostic{
		Category:    core.CategoryGeneratorInputInvalid,
		Severity:    core.SeverityError,
		Message:     "생성 입력을 확인하지 못했습니다.",
		Detail:      err.Error(),
		NextActions: []string{msgInputActionOne, msgInputActionTwo},
	})
}

// Present renders the saved script, its identity and the explicit next commands.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := res.Data.(Payload)
	if !ok {
		if ptr, ptrOK := res.Data.(*Payload); ptrOK && ptr != nil {
			payload = *ptr
		} else {
			return "S3 스크립트 생성 결과를 표시할 수 없습니다."
		}
	}
	report := payload.Generator
	var b strings.Builder
	b.WriteString(msgDoneHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%s: %s\n", msgDoneScript, report.Script)
	fmt.Fprintf(&b, "%s: %s\n", msgDoneSource, report.Source)
	fmt.Fprintf(&b, "%s: %s\n", msgDoneDest, report.Destination)
	fmt.Fprintf(&b, "%s: %s\n", msgDoneMode, report.Mode)
	if report.Replaced {
		fmt.Fprintf(&b, "%s: 기존 파일 교체\n", msgDoneSave)
	} else {
		fmt.Fprintf(&b, "%s: 새 파일 생성\n", msgDoneSave)
	}
	fmt.Fprintf(&b, "%s: sha256:%s\n", msgDoneSHA, report.SHA256)
	b.WriteString("\n")
	for _, notice := range report.Notices {
		fmt.Fprintf(&b, "- %s\n", notice)
	}
	b.WriteString("\n다음 명령:\n")
	for _, command := range report.NextCommands {
		fmt.Fprintf(&b, "  %s\n", command)
	}
	return strings.TrimRight(b.String(), "\n")
}
