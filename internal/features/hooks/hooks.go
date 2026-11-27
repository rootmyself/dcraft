// Package hooks registers the flat `dcraft hooks` command. It owns only the
// explicit project registration UI; the shared internal/hooks package owns the
// record, trust and execution contract consumed by the engine and generators.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	hooklib "github.com/rootmyself/dcraft/internal/hooks"
)

// Feature is the registered hooks command.
type Feature struct {
	service *hooklib.Service

	// projectDir is captured by the wizard "steps" run so the pure
	// WizardPreview can resolve relative paths without a target argument.
	projectDir string
}

func New(service *hooklib.Service) *Feature { return &Feature{service: service} }

// Payload is the machine-readable result of the hooks command, published as
// data.hooks. It contains no secret values.
type Payload struct {
	Hooks Report `json:"hooks"`
}

// Report lists the project registrations and their trust state.
type Report struct {
	Action        string             `json:"action"`
	ConfigFile    string             `json:"config_file"`
	Registrations []RegistrationView `json:"registrations"`
	Notices       []string           `json:"notices"`
}

// RegistrationView is one registration plus its current content identity.
type RegistrationView struct {
	Path          string `json:"path"`
	Stage         string `json:"stage"`
	EffectivePath string `json:"effective_path"`
	SHA256        string `json:"sha256,omitempty"`
	Trusted       bool   `json:"trusted"`
	Changed       bool   `json:"changed"`
	Present       bool   `json:"present"`
	Executable    bool   `json:"executable"`
	Problem       string `json:"problem,omitempty"`
}

// Descriptor implements core.Feature.
func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:    "hooks",
		Title:   msgTitle,
		Summary: msgSummary,
		Usage:   msgUsage,
		Section: core.SectionConfig,
		// Registration is a local project-config edit. It asks only for the
		// Docker CLI presence (the smallest published preflight set) and does
		// not require a running engine or Compose.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
		Wizard:         true,
		// The menu entry is drawn as a heading so the two generated-script
		// features appear as its children: Git, S3, then registering an
		// existing script.
		Menu: []core.MenuEntry{
			{Kind: core.MenuGroup, Feature: "hooks", Label: msgMenuGroup, Summary: msgMenuGroupSummary, Order: 2},
			{Kind: core.MenuRun, Feature: "hooks", Label: msgMenuCustom, Summary: msgMenuCustomSummary, Parent: "hooks", Order: 2},
		},
		Flags: []core.Flag{
			{Name: "list", Kind: core.FlagBool, Usage: msgFlagList},
			{Name: "add", Kind: core.FlagBool, Usage: msgFlagAdd},
			{Name: "remove", Kind: core.FlagBool, Usage: msgFlagRemove},
			{Name: "stage", Kind: core.FlagString, Usage: msgFlagStage},
			{Name: "script", Kind: core.FlagString, Usage: msgFlagScript},
		},
	}
}

// Run implements core.Feature. Options["wizard"]="steps" returns the guided
// flow; "apply" saves the collected answers; otherwise explicit flags choose
// list/add/remove.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.projectDir = in.Target.ProjectDir
	switch in.Option("wizard") {
	case "steps":
		return f.wizardSteps(ctx, in)
	case "apply":
		return f.applyWizard(ctx, in)
	}
	return f.runFlags(ctx, in)
}

func (f *Feature) runFlags(ctx context.Context, in core.Invocation) (any, error) {
	add := in.BoolOption("add")
	remove := in.BoolOption("remove")
	list := in.BoolOption("list")
	if countTrue(add, remove, list) > 1 {
		return nil, inputError(msgActionConflict, "")
	}
	switch {
	case add:
		return f.change(ctx, in, "add", in.Option("script"), in.Option("stage"))
	case remove:
		return f.change(ctx, in, "remove", in.Option("script"), in.Option("stage"))
	default:
		return f.list(ctx, in)
	}
}

func (f *Feature) change(ctx context.Context, in core.Invocation, action, script, stage string) (any, error) {
	if f.service == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoService,
			NextActions: []string{msgNoServiceAction},
		})
	}
	script = strings.TrimSpace(script)
	stage = strings.TrimSpace(stage)
	var missing []string
	if script == "" {
		missing = append(missing, "script")
	}
	if stage == "" {
		missing = append(missing, "stage")
	}
	if len(missing) > 0 {
		return nil, inputError(fmt.Sprintf(msgMissingInput, strings.Join(missing, ", ")), strings.Join(missing, ","))
	}
	parsed, err := parseStage(stage)
	if err != nil {
		return nil, err
	}
	reg := hooklib.Registration{Path: script, Stage: parsed}
	switch action {
	case "add":
		_, err = core.RunStageValue(in.Reporter, "register", msgStageRegister, func() (hooklib.Catalog, error) {
			return hooklib.Add(in.Target.ProjectDir, reg)
		})
	case "remove":
		_, err = core.RunStageValue(in.Reporter, "remove", msgStageRemove, func() (hooklib.Catalog, error) {
			return hooklib.Remove(in.Target.ProjectDir, script, parsed)
		})
	default:
		return nil, inputError(msgActionConflict, action)
	}
	if err != nil {
		return nil, err
	}
	return f.buildReport(ctx, in, action, action)
}

func (f *Feature) list(ctx context.Context, in core.Invocation) (any, error) {
	return f.buildReport(ctx, in, "list", "")
}

func (f *Feature) buildReport(ctx context.Context, in core.Invocation, action, change string) (Payload, error) {
	if f.service == nil {
		return Payload{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoService,
			NextActions: []string{msgNoServiceAction},
		})
	}
	views, err := core.RunStageValue(in.Reporter, "list", msgStageList, func() ([]hooklib.View, error) {
		return f.service.Describe(ctx, in.Target)
	})
	if err != nil {
		return Payload{}, err
	}
	report := Report{
		Action:        action,
		ConfigFile:    filepath.Join(in.Target.ProjectDir, hooklib.FileName),
		Registrations: make([]RegistrationView, 0, len(views)),
		Notices:       []string{},
	}
	needsTrust := false
	for _, view := range views {
		report.Registrations = append(report.Registrations, RegistrationView{
			Path:          view.Path,
			Stage:         string(view.Stage),
			EffectivePath: view.EffectivePath,
			SHA256:        view.SHA256,
			Trusted:       view.Trusted,
			Changed:       view.Changed,
			Present:       view.Present,
			Executable:    view.Executable,
			Problem:       view.Problem,
		})
		if !view.Trusted {
			needsTrust = true
		}
	}
	if change == "add" {
		report.Notices = append(report.Notices, msgAdded)
	}
	if change == "remove" {
		report.Notices = append(report.Notices, msgRemoved)
	}
	if len(views) == 0 {
		report.Notices = append(report.Notices, msgNoHooks)
	} else if needsTrust {
		report.Notices = append(report.Notices, msgTrustPending)
	}
	report.Notices = append(report.Notices, msgDisclosure)
	return Payload{Hooks: report}, nil
}

func (f *Feature) wizardSteps(ctx context.Context, in core.Invocation) (any, error) {
	if f.service == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoService,
			NextActions: []string{msgNoServiceAction},
		})
	}
	spec := core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{
				Name: "action", Title: msgWizardActionTitle, Prompt: msgWizardActionPrompt,
				Default: "add",
				Options: []core.WizardOption{
					{Value: "add", Label: msgWizardAddLabel},
					{Value: "remove", Label: msgWizardRemoveLabel},
				},
			},
			{
				Name: "stage", Title: msgWizardStageTitle, Prompt: msgWizardStagePrompt,
				Default: string(hooklib.StagePre),
				Options: []core.WizardOption{
					{Value: string(hooklib.StagePre), Label: msgWizardPreLabel},
					{Value: string(hooklib.StagePost), Label: msgWizardPostLabel},
				},
			},
			{
				Name: "script", Title: msgWizardScriptTitle, Prompt: msgWizardScriptPrompt,
				AllowCustom: true, List: true, CustomHint: msgWizardScriptHint,
			},
		},
	}
	return spec, nil
}

// WizardPreview implements core.Wizard. It is a pure preview: no file is
// written and no script is run.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	if f.service == nil {
		return "", core.Fail(core.Diagnostic{
			Category: core.CategoryInternal, Severity: core.SeverityError, Message: msgNoService,
		})
	}
	action := strings.TrimSpace(answers["action"])
	if action != "add" && action != "remove" {
		return "", inputError(msgActionConflict, action)
	}
	stage, err := parseStage(answers["stage"])
	if err != nil {
		return "", err
	}
	scripts := answerScripts(answers["script"])
	if len(scripts) == 0 {
		return "", inputError(msgMissingInput, "script")
	}
	var b strings.Builder
	b.WriteString(msgWizardPreviewTitle)
	b.WriteString("\n")
	fmt.Fprintf(&b, "동작: %s\n", actionLabel(action))
	fmt.Fprintf(&b, "시점: %s\n", stageLabel(stage))
	b.WriteString(msgWizardWorkdirPrefix + msgWizardWorkdir)
	b.WriteString("\n")
	if action == "add" {
		for _, script := range scripts {
			resolved, err := hooklib.ResolvePath(f.lastProjectDir(answers), script)
			if err != nil {
				return "", err
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", inputError(fmt.Sprintf(msgWizardMissing, script), resolved)
			}
			if info.IsDir() {
				return "", inputError(fmt.Sprintf(msgWizardDirectory, script), resolved)
			}
			fmt.Fprintf(&b, "스크립트: %s\n", script)
			fmt.Fprintf(&b, "실제 경로: %s\n", resolved)
			if sha, hashErr := hooklib.ContentSHA256(resolved); hashErr == nil {
				fmt.Fprintf(&b, "내용 식별자: sha256:%s\n", sha)
			}
			if info.Mode().Perm()&0o111 == 0 {
				b.WriteString(msgWizardNotExecutable)
				b.WriteString("\n")
			}
		}
	} else {
		cat, err := hooklib.Load(f.lastProjectDir(answers))
		if err != nil {
			return "", err
		}
		for _, script := range scripts {
			found := false
			for _, reg := range cat.Registrations {
				regPath, _ := hooklib.ResolvePath(cat.ProjectDir, reg.Path)
				wantPath, _ := hooklib.ResolvePath(f.lastProjectDir(answers), script)
				if regPath == wantPath && reg.Stage == stage {
					found = true
				}
			}
			if !found {
				return "", inputError(fmt.Sprintf(msgWizardNotRegistered, script, stage), "")
			}
			fmt.Fprintf(&b, "스크립트: %s\n", script)
		}
	}
	b.WriteString(msgDisclosure)
	return strings.TrimRight(b.String(), "\n"), nil
}

// lastProjectDir is set by the engine through the invocation before the TUI
// asks for a preview. The wizard interface only receives answers, so the
// feature keeps the last target folder it was prepared for.
func (f *Feature) lastProjectDir(answers map[string]string) string {
	return f.projectDir
}

func (f *Feature) applyWizard(ctx context.Context, in core.Invocation) (any, error) {
	action := strings.TrimSpace(in.Option("action"))
	scripts := answerScripts(in.Option("script"))
	if action != "add" && action != "remove" {
		return nil, inputError(msgActionConflict, action)
	}
	if len(scripts) == 0 {
		return nil, inputError(msgMissingInput, "script")
	}
	// One registration per answer, in the order the reader entered them. A
	// failure partway stops the rest: the error names the script that failed,
	// because earlier items are already saved and the reader has to know which
	// ones landed.
	for i, script := range scripts {
		payload, err := f.change(ctx, in, action, script, in.Option("stage"))
		if err != nil {
			if i == 0 {
				return nil, err
			}
			return nil, partialScriptsError(err, scripts[:i], scripts[i])
		}
		if i == len(scripts)-1 {
			return payload, nil
		}
	}
	return nil, inputError(msgMissingInput, "script")
}

// partialScriptsError states which scripts were saved before one failed. The
// underlying diagnostic is kept so the cause is not replaced by the count.
func partialScriptsError(cause error, saved []string, failed string) error {
	var ce *core.Error
	diags := []core.Diagnostic{{
		Category:    core.CategoryHookInputInvalid,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgPartialScripts, len(saved), failed),
		NextActions: []string{msgInputActionOne},
	}}
	if errors.As(cause, &ce) && len(ce.Diagnostics) > 0 {
		diags[0].Detail = ce.Diagnostics[0].Message
	}
	return core.FailCause(cause, diags[0])
}

// answerScripts decodes the script answer. The step is a list, so each item is
// one script path; a single answer decodes to one item.
func answerScripts(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseStage(value string) (hooklib.Stage, error) {
	switch strings.TrimSpace(value) {
	case string(hooklib.StagePre):
		return hooklib.StagePre, nil
	case string(hooklib.StagePost):
		return hooklib.StagePost, nil
	default:
		return "", inputError(fmt.Sprintf(msgUnknownStage, value), "")
	}
}

func stageLabel(stage hooklib.Stage) string {
	if stage == hooklib.StagePost {
		return msgStagePostLabel
	}
	return msgStagePreLabel
}

func actionLabel(action string) string {
	switch action {
	case "remove":
		return msgWizardRemoveLabel
	case "list":
		return msgPresentActionList
	default:
		return msgWizardAddLabel
	}
}

func countTrue(values ...bool) int {
	n := 0
	for _, v := range values {
		if v {
			n++
		}
	}
	return n
}

func inputError(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryHookInputInvalid,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgInputActionOne, msgInputActionTwo},
	})
}
