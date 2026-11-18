// Package gitgen registers the flat `dcraft generate-git-hook` command. It
// owns the guided Git inputs, the preview and the save step. It never contacts
// Git, never registers a hook and never grants script trust: those remain the
// existing hooks registration and the engine's independent script_trust
// approval on first execution.
package gitgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/generators"
	gitgen "github.com/rootmyself/dcraft/internal/generators/git"
)

// Feature is the registered generator command.
type Feature struct {
	// projectDir is captured by the wizard "steps" run so WizardPreview can
	// resolve relative paths; the wizard interface only receives answers.
	projectDir string
}

func New() *Feature { return &Feature{} }

// Payload is the machine-readable result, published as data.generator.
// It contains no credentials and no script body.
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
	Repository          string   `json:"repository"`
	Ref                 string   `json:"ref"`
	RefKind             string   `json:"ref_kind"`
	Stage               string   `json:"stage"`
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
		Name:    "generate-git-hook",
		Title:   msgTitle,
		Summary: msgSummary,
		Usage:   msgUsage,
		// Saving a project file is a local-config edit. Only the Docker CLI
		// presence is required by the published preflight contract; the
		// generated script needs git, but generation does not.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
		Approval:       core.ApprovalFileOverwrite,
		Wizard:         true,
		Flags: []core.Flag{
			{Name: "repo", Kind: core.FlagString, Required: true, Usage: msgFlagRepo},
			{Name: "ref", Kind: core.FlagString, Required: true, Usage: msgFlagRef},
			{Name: "dest", Kind: core.FlagString, Required: true, Usage: msgFlagDest},
			{Name: "output", Kind: core.FlagString, Required: true, Usage: msgFlagOut},
			{Name: "stage", Kind: core.FlagString, Default: "pre", Usage: msgFlagStage},
			{Name: "allow-outside", Kind: core.FlagBool, Usage: msgFlagOutside},
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

func (f *Feature) build(in core.Invocation) (gitgen.Generated, error) {
	layout, err := generators.ResolveLayout(
		in.Target.ProjectDir,
		in.Option("dest"),
		in.Option("output"),
		generators.Options{AllowOutside: in.BoolOption("allow-outside")},
	)
	if err != nil {
		return gitgen.Generated{}, inputError(err)
	}
	gen, err := gitgen.Build(gitgen.Input{
		Layout:     layout,
		Repository: in.Option("repo"),
		Ref:        in.Option("ref"),
		Stage:      in.Option("stage"),
	})
	if err != nil {
		return gitgen.Generated{}, inputError(err)
	}
	return gen, nil
}

func (f *Feature) generate(ctx context.Context, in core.Invocation) (any, error) {
	gen, err := core.RunStageValue(in.Reporter, "validate", msgStageValidate, func() (gitgen.Generated, error) {
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

func (f *Feature) payload(gen gitgen.Generated, created bool) Payload {
	script := displayScript(gen.Layout)
	report := Report{
		Action:              "generate-git-hook",
		Script:              script,
		ScriptPath:          gen.Layout.ScriptPath,
		Destination:         gen.Layout.Destination,
		DestinationRelative: gen.Layout.DestinationRel,
		Repository:          gen.Repository,
		Ref:                 gen.RefShort,
		RefKind:             string(gen.Kind),
		Stage:               gen.Stage,
		SHA256:              gen.SHA256,
		Created:             created,
		Replaced:            !created,
		Registered:          false,
		TrustRequired:       true,
		NextCommands: []string{
			"dcraft hooks --add --stage " + gen.Stage + " --script " + shellQuote(script),
			"dcraft up --approve script_trust",
		},
		Notices: []string{
			msgDoneNotRun,
			msgDoneTrust,
			msgDoneGeneration,
			"dcraft update에도 같은 사전/사후 스크립트가 적용됩니다. 스크립트를 수정하면 다시 신뢰 승인이 필요합니다.",
		},
	}
	if !created {
		report.Notices = append([]string{msgPlanReplaceFile}, report.Notices...)
	}
	if !gen.Layout.ScriptInsideProject || !strings.HasPrefix(filepath.Clean(gen.Layout.Destination), filepath.Clean(gen.Layout.ProjectDir)+string(filepath.Separator)) {
		report.Notices = append(report.Notices, "프로젝트 밖 경로가 명시적 확인(--allow-outside)으로 포함되었습니다. 이동·백업 시 경로를 직접 확인하세요.")
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
			{Name: "repo", Title: msgWizardRepoTitle, Prompt: msgWizardRepoPrompt, AllowCustom: true, CustomHint: msgWizardRepoHint},
			{Name: "ref", Title: msgWizardRefTitle, Prompt: msgWizardRefPrompt, AllowCustom: true, CustomHint: msgWizardRefHint},
			{Name: "dest", Title: msgWizardDestTitle, Prompt: msgWizardDestPrompt, AllowCustom: true, Default: "src", CustomHint: msgWizardDestHint},
			{Name: "output", Title: msgWizardOutTitle, Prompt: msgWizardOutPrompt, AllowCustom: true, Default: "scripts/git-sync.sh", CustomHint: msgWizardOutHint},
			{
				Name: "stage", Title: msgWizardStageTitle, Prompt: msgWizardStagePrompt, Default: "pre",
				Options: []core.WizardOption{
					{Value: "pre", Label: msgWizardPreLabel},
					{Value: "post", Label: msgWizardPostLabel},
				},
			},
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
	layout, err := generators.ResolveLayout(project, answers["dest"], answers["output"], generators.Options{})
	if err != nil {
		return "", inputError(err)
	}
	gen, err := gitgen.Build(gitgen.Input{
		Layout:     layout,
		Repository: answers["repo"],
		Ref:        answers["ref"],
		Stage:      answers["stage"],
	})
	if err != nil {
		return "", inputError(err)
	}
	return previewText(gen), nil
}

func previewText(gen gitgen.Generated) string {
	layout := gen.Layout
	save := msgPlanNewFile
	if gen.Existed {
		save = msgPlanReplaceFile
	}
	kind := msgPlanKindAuto
	switch gen.Kind {
	case gitgen.KindBranch:
		kind = msgPlanKindBranch
	case gitgen.KindTag:
		kind = msgPlanKindTag
	case gitgen.KindCommit:
		kind = msgPlanKindCommit
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
	fmt.Fprintf(&b, "  dcraft hooks --add --stage %s --script %s\n", gen.Stage, shellQuote(script))
	fmt.Fprintf(&b, "  dcraft up --approve script_trust\n")
	fmt.Fprintf(&b, "  스크립트 내용이 바뀌면 새 script_trust 승인이 필요합니다.\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanApproval)
	fmt.Fprintf(&b, "  이 작업은 file_overwrite 승인이 필요합니다(새 파일 생성 또는 기존 파일 교체). 자동화에서는 --approve file_overwrite를 명시하세요.\n")
	fmt.Fprintf(&b, "  생성 승인과 첫 실행의 script_trust 승인은 별개입니다.\n\n")

	fmt.Fprintf(&b, "%s\n", msgPlanInputs)
	fmt.Fprintf(&b, "  저장소: %s\n", gen.Repository)
	fmt.Fprintf(&b, "  ref: %s (%s)\n", gen.RefShort, kind)
	fmt.Fprintf(&b, "  실행 시점: %s\n", stageLabel(gen.Stage))
	fmt.Fprintf(&b, "  받을 폴더: %s\n", layout.Destination)
	if layout.DestinationRel != layout.Destination {
		fmt.Fprintf(&b, "    프로젝트 기준: %s\n", layout.DestinationRel)
	}
	fmt.Fprintf(&b, "  스크립트: %s\n", layout.ScriptPath)
	if layout.ScriptRel != layout.ScriptPath {
		fmt.Fprintf(&b, "    프로젝트 기준: %s\n", layout.ScriptRel)
	}
	fmt.Fprintf(&b, "  프로젝트 폴더: %s\n", layout.ProjectDir)
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanExecution)
	fmt.Fprintf(&b, "  훅으로 등록하면 dcraft가 프로젝트 폴더에서 실행합니다. 스크립트는 자기 위치에서 프로젝트 폴더를 계산합니다.\n")
	fmt.Fprintf(&b, "  %s\n", save)
	fmt.Fprintf(&b, "  실행 권한 0755로 저장합니다. .dcraft.yaml은 이 단계에서 수정하지 않습니다.\n")
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanEffects)
	b.WriteString(msgPlanEffectsBody)
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanPrereq)
	b.WriteString(msgPlanPrereqBody)
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanFailure)
	b.WriteString(msgPlanFailureBody)
	b.WriteString("\n")

	fmt.Fprintf(&b, "%s\n", msgPlanEdit)
	b.WriteString(msgPlanEditBody)
	return strings.TrimRight(b.String(), "\n")
}

func stageLabel(stage string) string {
	if stage == "post" {
		return "사후(post) — up/update 완료 후"
	}
	return "사전(pre) — up/update 전"
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
			return "Git 스크립트 생성 결과를 표시할 수 없습니다."
		}
	}
	report := payload.Generator
	var b strings.Builder
	b.WriteString(msgDoneHeader)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%s: %s\n", msgDoneScript, report.Script)
	fmt.Fprintf(&b, "%s: %s\n", msgDoneDest, report.Destination)
	fmt.Fprintf(&b, "%s: %s (%s)\n", msgDoneRef, report.Ref, report.RefKind)
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
