package initconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rootmyself/dcraft/internal/core"
)

// Controller owns collecting, generating, validating and saving first-run
// Compose configuration. CLI flags and TUI wizard answers go through the same
// path; the controller never runs the stack.
type Controller struct {
	validator Validator
	saver     Saver

	mu            sync.Mutex
	lastTarget    core.Target
	lastApprovals []core.ApprovalKind
	wizardStarted bool
}

// New builds the controller from the narrow validation and save boundaries.
func New(validator Validator, saver Saver) *Controller {
	return &Controller{validator: validator, saver: saver}
}

// Payload is the machine-readable result under data.init.
type Payload struct {
	Init Report `json:"init"`
}

// Report describes the saved file and its effects. Content is exactly what was
// written; dcraft never adds a tool-only key to the Compose file.
type Report struct {
	Mode        string     `json:"mode"`
	Service     string     `json:"service"`
	Path        string     `json:"path"`
	ProjectName string     `json:"project_name"`
	Content     string     `json:"content"`
	Changed     bool       `json:"changed"`
	Overwritten bool       `json:"overwritten"`
	Validation  Validation `json:"validation"`
	Resources   []Resource `json:"resources"`
	Notices     []string   `json:"notices"`
	NextCommand string     `json:"next_command"`
}

type prepared struct {
	input       Input
	dest        Destination
	spec        *Spec
	validation  Validation
	overwrite   bool
	notices     []string
	resources   []Resource
	nextCommand string
}

// Run implements the feature operation. The engine has already resolved the
// target, checked preflight and enforced the file_overwrite approval for an
// existing destination; this method rechecks the file facts it depends on.
func (c *Controller) Run(ctx context.Context, in core.Invocation) (any, error) {
	prepared, err := c.prepare(ctx, in, in.Option("wizard") == "apply", false)
	if err != nil {
		return nil, err
	}
	err = core.RunStage(in.Reporter, "write", msgStageWrite, func() error {
		return c.save(ctx, prepared)
	})
	if err != nil {
		return nil, err
	}
	return Payload{Init: prepared.report()}, nil
}

// Plan shows the exact generated file, destination, project name, effects and
// next command without writing anything.
func (c *Controller) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	prepared, err := c.prepare(ctx, in, in.Option("wizard") == "apply", true)
	if err != nil {
		return core.Plan{}, err
	}
	return core.Plan{Text: previewText(prepared)}, nil
}

// RequiredApproval implements core.ConditionalApproval. It is read-only and
// answers only whether replacing an existing destination needs the independent
// file_overwrite approval. A standard Compose file with a different explicit
// destination is not approved here; Run refuses it before any write.
func (c *Controller) RequiredApproval(ctx context.Context, in core.Invocation) (core.ApprovalKind, error) {
	if err := ctx.Err(); err != nil {
		return core.ApprovalNone, err
	}
	projectDir := strings.TrimSpace(in.Target.ProjectDir)
	if projectDir == "" {
		return core.ApprovalNone, inputErr("대상 프로젝트 폴더 확인 실패.")
	}
	dest, err := destinationPath(projectDir, strings.TrimSpace(in.Option("output")))
	if err != nil {
		return core.ApprovalNone, err
	}
	if dest.Exists && !dest.Explicit {
		// A default destination that already exists is never approved
		// implicitly: the user must name the file and approve replacing it.
		return core.ApprovalNone, explicitOverwriteError(dest)
	}
	existing := existingComposeFiles(projectDir, in.Target.Files)
	if len(existing) > 0 {
		for _, path := range existing {
			if sameFile(path, dest.Path) {
				return core.ApprovalFileOverwrite, nil
			}
		}
		return core.ApprovalNone, nil
	}
	if dest.Exists {
		return core.ApprovalFileOverwrite, nil
	}
	return core.ApprovalNone, nil
}

func (c *Controller) prepare(ctx context.Context, in core.Invocation, wizard, preview bool) (*prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type collected struct {
		input    Input
		dest     Destination
		existing []string
	}
	collect, err := core.RunStageValue(in.Reporter, "collect", msgStageCollect, func() (collected, error) {
		input, err := collectInput(in, wizard)
		if err != nil {
			return collected{}, err
		}
		dest, err := destinationPath(in.Target.ProjectDir, input.Output)
		if err != nil {
			return collected{}, err
		}
		// The engine inspected approvals before preflight. If the destination
		// appeared (or changed) after that inspection, a run that did not carry
		// file_overwrite must not turn into an overwrite just because prepare
		// sees the file now. A read-only preview still describes the overwrite.
		if dest.Exists && !preview && !core.HasApproval(in.Approvals, core.ApprovalFileOverwrite) {
			return collected{}, destinationChangedError(dest.Path)
		}
		if dest.Exists && !dest.Explicit {
			return collected{}, explicitOverwriteError(dest)
		}
		existing := existingComposeFiles(in.Target.ProjectDir, in.Target.Files)
		if len(existing) > 0 {
			matched := false
			for _, path := range existing {
				if sameFile(path, dest.Path) {
					matched = true
					break
				}
			}
			if !matched {
				return collected{}, existingConfigurationError(existing, dest)
			}
		}
		return collected{input: input, dest: dest, existing: existing}, nil
	})
	if err != nil {
		return nil, err
	}

	spec, err := core.RunStageValue(in.Reporter, "preview", msgStagePreview, func() (*Spec, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return buildSpec(in.Target.ProjectDir, collect.input, collect.dest)
	})
	if err != nil {
		return nil, err
	}

	validation, err := core.RunStageValue(in.Reporter, "validate", msgStageValidate, func() (Validation, error) {
		if c.validator == nil {
			return Validation{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryInternal,
				Severity:    core.SeverityError,
				Message:     "Compose 검증기 준비 실패.",
				NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행."},
			})
		}
		result, err := c.validator.Validate(ctx, collect.dest.Dir, []byte(spec.Content))
		if err != nil {
			return Validation{}, err
		}
		if !result.Checked || strings.TrimSpace(result.ProjectName) == "" {
			return Validation{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryInitInvalid,
				Severity:    core.SeverityError,
				Message:     msgComposeOutput,
				NextActions: []string{msgComposeRejectedAct, msgComposeNoFile},
			})
		}
		if err := validationError(result, collect.input.Service); err != nil {
			return Validation{}, err
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}

	overwrite := collect.dest.Exists
	notices := append([]string{}, spec.Notices...)
	if len(collect.existing) > 1 {
		notices = append(notices, msgNoticeOtherExisting)
	}
	for _, override := range overrideComposeFiles(in.Target.ProjectDir) {
		if !fileInList(collect.existing, override) {
			notices = append(notices, fmt.Sprintf(msgNoticeOverride, override))
		}
	}
	if overwrite {
		notices = append(notices, msgNoticeOverwrite)
	}
	if in.Target.ProjectName != "" {
		notices = append(notices, msgNoticeProjectName)
	}
	notices = append(notices, msgNoticeComposeName)

	resources := make([]Resource, 0, len(spec.Resources)+1)
	for _, resource := range spec.Resources {
		if resource.Kind == "named_volume" {
			if actual := validation.Volumes[resource.Name]; actual != "" {
				resource.Name = actual
			}
		}
		resources = append(resources, resource)
	}
	if validation.DefaultNetwork != "" {
		resources = append(resources, Resource{
			Kind: "network", Name: validation.DefaultNetwork,
			Detail: "dcraft up 시 Compose가 생성",
		})
	}
	return &prepared{
		input:       collect.input,
		dest:        collect.dest,
		spec:        spec,
		validation:  validation,
		overwrite:   overwrite,
		notices:     notices,
		resources:   resources,
		nextCommand: nextCommandFor(collect.dest, in.Target.ProjectDir, in.Target.ProjectName),
	}, nil
}

// destinationChangedError reports a save-time state change: the destination
// was absent when approval was inspected (or the preview was shown), and a
// file now exists. dcraft refuses to replace it without a fresh preview.
func destinationChangedError(path string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInitDestinationChanged,
		Severity:    core.SeverityError,
		Message:     msgDestinationChanged,
		Detail:      path,
		NextActions: []string{msgDestinationChangedActOne, msgDestinationChangedActTwo, msgDestinationChangedActThree},
	})
}

// explicitOverwriteError is used by the conditional approval inspection and
// again by prepare. A default path is never treated as an explicit overwrite,
// even with file_overwrite: the destination must be named.
func explicitOverwriteError(dest Destination) *core.Error {
	return inputErrDetail(
		"기존 파일을 덮어쓰려면 저장 경로 명시 필요.",
		fmt.Sprintf("기존 파일: %s\n--output %s와 --approve file_overwrite를 함께 지정. 기존 파일은 변경하지 않았음.", dest.Path, dest.Path),
		"--output <기존 파일>로 대상을 명시.",
		"자동화에서는 --approve file_overwrite도 함께 지정.",
	)
}

func fileInList(files []string, target string) bool {
	for _, path := range files {
		if sameFile(path, target) {
			return true
		}
	}
	return false
}

func existingConfigurationError(existing []string, dest Destination) *core.Error {
	detail := "기존 Compose 파일:\n- " + strings.Join(existing, "\n- ")
	if dest.Explicit {
		detail += "\n\n요청한 저장 경로:\n- " + dest.Path
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInitExistingConfiguration,
		Severity:    core.SeverityError,
		Message:     msgExistingConfig,
		Detail:      detail,
		NextActions: []string{msgExistingAction, msgExistingOverwrite, msgExistingUntouched},
	})
}

func (c *Controller) save(ctx context.Context, prepared *prepared) error {
	if c.saver == nil {
		return core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     "파일 저장기 준비 실패.",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행."},
		})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := c.saver.Save(ctx, prepared.dest.Path, []byte(prepared.spec.Content), prepared.overwrite)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case errors.Is(err, ErrExists):
		return destinationChangedError(prepared.dest.Path)
	case errors.Is(err, ErrSymlink), errors.Is(err, ErrNotRegular):
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInitInputInvalid,
			Severity:    core.SeverityError,
			Message:     msgOutputInvalid,
			Detail:      fmt.Sprintf("%s: %v", prepared.dest.Path, err),
			NextActions: []string{"일반 파일 경로 지정.", msgComposeNoFile},
		})
	default:
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryInitFailed,
			Severity:    core.SeverityError,
			Message:     msgSaveFailed,
			Detail:      fmt.Sprintf("%s: %v", prepared.dest.Path, err),
			NextActions: []string{msgSaveFailedAct, msgComposeNoFile},
		})
	}
}

func (p *prepared) report() Report {
	report := Report{
		Mode:        string(p.input.Mode),
		Service:     p.input.Service,
		Path:        p.dest.Path,
		ProjectName: p.validation.ProjectName,
		Content:     p.spec.Content,
		Changed:     true,
		Overwritten: p.overwrite,
		Validation:  p.validation,
		Resources:   append([]Resource{}, p.resources...),
		Notices:     append([]string{}, p.notices...),
		NextCommand: p.nextCommand,
	}
	if report.Resources == nil {
		report.Resources = []Resource{}
	}
	if report.Notices == nil {
		report.Notices = []string{}
	}
	return report
}

// RememberWizardTarget stores the resolved target for the interactive preview,
// which the published Wizard interface calls without an Invocation.
func (c *Controller) RememberWizardTarget(target core.Target, approvals []core.ApprovalKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastTarget = target
	c.lastApprovals = append([]core.ApprovalKind{}, approvals...)
	c.wizardStarted = true
}

func (c *Controller) wizardState() (core.Target, []core.ApprovalKind, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastTarget, append([]core.ApprovalKind{}, c.lastApprovals...), c.wizardStarted
}

// WizardSteps builds the guided questions. Defaults are visible suggestions
// (a Dockerfile if one exists, service "app"), never silent answers.
func (c *Controller) WizardSteps(ctx context.Context, in core.Invocation) (core.WizardSpec, error) {
	if err := ctx.Err(); err != nil {
		return core.WizardSpec{}, err
	}
	projectDir := in.Target.ProjectDir
	existing := existingComposeFiles(projectDir, in.Target.Files)
	defaultOutput := "compose.yaml"
	if explicit := strings.TrimSpace(in.Option("output")); explicit != "" {
		defaultOutput = explicit
	} else if len(existing) > 0 {
		if rel, err := filepath.Rel(projectDir, existing[0]); err == nil && insideProject(projectDir, existing[0]) {
			defaultOutput = filepath.ToSlash(rel)
		} else {
			defaultOutput = existing[0]
		}
	}
	modeDefault := string(ModeImage)
	if info, err := os.Stat(filepath.Join(projectDir, "Dockerfile")); err == nil && info.Mode().IsRegular() {
		modeDefault = string(ModeDockerfile)
	}
	var approvals []core.ApprovalKind
	if dest, err := destinationPath(projectDir, defaultOutput); err == nil && dest.Exists {
		approvals = append(approvals, core.ApprovalFileOverwrite)
	}
	c.RememberWizardTarget(in.Target, approvals)

	const modeStep = "from"
	spec := core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{
				Name: "from", Title: msgWizardModeTitle, Prompt: msgWizardModePrompt,
				Default: modeDefault,
				Options: []core.WizardOption{
					{Value: string(ModeDockerfile), Label: msgWizardModeDocker},
					{Value: string(ModeImage), Label: msgWizardModeImage},
				},
			},
			{
				Name: "service-name", Title: msgWizardServiceTitle, Prompt: msgWizardServicePrompt,
				AllowCustom: true, Default: "app", CustomHint: msgWizardServiceHint,
			},
			{
				Name: "context", Title: msgWizardContextTitle, Prompt: msgWizardContextPrompt,
				AllowCustom: true, Default: ".", CustomHint: msgWizardContextHint, DependsOn: modeStep, DependsValue: string(ModeDockerfile),
			},
			{
				Name: "dockerfile", Title: msgWizardDockerTitle, Prompt: msgWizardDockerPrompt,
				AllowCustom: true, Default: "Dockerfile", CustomHint: msgWizardDockerHint, DependsOn: modeStep, DependsValue: string(ModeDockerfile),
			},
			{
				Name: "image", Title: msgWizardImageTitle, Prompt: msgWizardImagePrompt,
				AllowCustom: true, CustomHint: msgWizardImageHint, DependsOn: modeStep, DependsValue: string(ModeImage),
			},
			{
				Name: "output", Title: msgWizardOutputTitle, Prompt: msgWizardOutputPrompt,
				AllowCustom: true, Default: defaultOutput, CustomHint: msgWizardOutputHint,
			},
			{
				Name: "port", Title: msgWizardPortTitle, Prompt: msgWizardPortPrompt,
				AllowCustom: true, List: true, Optional: true, CustomHint: msgWizardPortHint,
			},
			{
				Name: "volume", Title: msgWizardVolumeTitle, Prompt: msgWizardVolumePrompt,
				AllowCustom: true, List: true, Optional: true, CustomHint: msgWizardVolumeHint,
			},
			{
				Name: "env-var", Title: msgWizardEnvTitle, Prompt: msgWizardEnvPrompt,
				AllowCustom: true, List: true, Optional: true, CustomHint: msgWizardEnvHint,
			},
			{
				Name: "command", Title: msgWizardCommandTitle, Prompt: msgWizardCommandPrompt,
				AllowCustom: true, List: true, Optional: true, CustomHint: msgWizardCommandHint,
			},
			{
				Name: "restart", Title: msgWizardRestartTitle, Prompt: msgWizardRestartPrompt,
				Default: "",
				Options: []core.WizardOption{
					{Value: "", Label: msgWizardRestartNone},
					{Value: "no", Label: msgWizardRestartNo},
					{Value: "always", Label: msgWizardRestartAlways},
					{Value: "unless-stopped", Label: msgWizardRestartUnless},
					{Value: "on-failure", Label: msgWizardRestartFailure},
				},
			},
		},
		RequiredApprovals: approvals,
	}
	return spec, nil
}

// WizardPreview implements core.Wizard. It uses the same generator and
// validator as the apply run, but never saves. The preview refuses to offer an
// overwrite that the wizard did not collect file_overwrite for; the user must
// go back or use the CLI with the explicit destination and approval.
func (c *Controller) WizardPreview(answers map[string]string) (string, error) {
	target, approvals, ok := c.wizardState()
	if !ok || target.ProjectDir == "" {
		return "", inputErr("대화형 설정을 먼저 시작 필요.")
	}
	in := core.Invocation{
		Target:  target,
		Options: map[string]string{},
	}
	for key, value := range answers {
		in.Options[key] = value
	}
	prepared, err := c.prepare(context.Background(), in, true, true)
	if err != nil {
		return "", err
	}
	if prepared.overwrite && !core.HasApproval(approvals, core.ApprovalFileOverwrite) {
		return "", inputErrDetail(
			"선택한 저장 경로에 기존 파일이 있어 파일 덮어쓰기(file_overwrite) 승인이 필요.",
			fmt.Sprintf("저장 경로: %s\n기본 저장 경로로 돌아가거나, CLI에서 --output %s --approve file_overwrite를 명시.", prepared.dest.Path, prepared.dest.Path),
			"← 이전으로 돌아가 저장 경로를 바꾸.",
			"CLI에서는 --approve file_overwrite 명시 필요.",
		)
	}
	return previewText(prepared), nil
}
