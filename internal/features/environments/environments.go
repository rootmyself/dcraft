// Package environments registers the flat `dcraft environments` command. It
// creates, lists and removes named environment aliases (Compose file list,
// explicit env files, profiles) in the project's .dcraft.yaml. Selection rules
// live in internal/target and are unchanged: this feature never selects an
// alias and never touches Compose files or Docker.
package environments

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	envconfig "github.com/rootmyself/dcraft/internal/environments"
)

// Feature is the registered environments command.
type Feature struct {
	// projectDir is captured by the wizard "steps" run so the pure
	// WizardPreview can resolve relative paths. The wizard interface only
	// receives answers.
	projectDir string
}

// New builds the registered command.
func New() *Feature { return &Feature{} }

// Payload is the machine-readable result, published as data.environments. It
// contains alias configuration only: no env-file path contents, values or
// resolved Compose configuration.
type Payload struct {
	Environments Report `json:"environments"`
}

// Report is the list of registered aliases after the requested action.
type Report struct {
	Action     string      `json:"action"`
	ConfigFile string      `json:"config_file"`
	Aliases    []AliasView `json:"aliases"`
}

// AliasView is one alias exactly as configured (paths resolved against the
// project folder for stable machine comparison).
type AliasView struct {
	Name     string   `json:"name"`
	Files    []string `json:"files"`
	EnvFiles []string `json:"env_files"`
	Profiles []string `json:"profiles"`
}

// Descriptor implements core.Feature.
func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:    "environments",
		Title:   msgTitle,
		Summary: msgSummary,
		Usage:   msgUsage,
		// Alias editing is local project-config work. It needs no engine or
		// Compose, so it declares the smallest published preflight set.
		Requires:       core.Requirements{core.RequirementDockerCLI},
		TargetOptional: true,
		Wizard:         true,
		Flags: []core.Flag{
			{Name: "add", Kind: core.FlagBool, Usage: msgFlagAdd},
			{Name: "list", Kind: core.FlagBool, Usage: msgFlagList},
			{Name: "remove", Kind: core.FlagBool, Usage: msgFlagRemove},
			{Name: "name", Kind: core.FlagString, Usage: msgFlagName},
		},
	}
}

// Run implements core.Feature. Options["wizard"]="steps" returns the guided
// flow; "apply" saves the collected answers. Otherwise explicit flags choose
// add/list/remove. Nothing is inferred or selected implicitly.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	f.projectDir = in.Target.ProjectDir
	switch in.Option("wizard") {
	case "steps":
		return f.wizardSteps(in), nil
	case "apply":
		return f.applyWizard(in)
	}
	return f.runFlags(in)
}

func (f *Feature) runFlags(in core.Invocation) (any, error) {
	add := in.BoolOption("add")
	list := in.BoolOption("list")
	remove := in.BoolOption("remove")
	switch {
	case countTrue(add, list, remove) == 0:
		return nil, inputError(msgActionRequired, "")
	case countTrue(add, list, remove) > 1:
		return nil, inputError(msgActionConflict, "")
	case add:
		name, err := requiredName(in)
		if err != nil {
			return nil, err
		}
		return f.change(in, "add", core.Environment{
			Name:     name,
			Files:    append([]string{}, in.Target.Files...),
			EnvFiles: append([]string{}, in.Target.EnvFiles...),
			Profiles: append([]string{}, in.Target.Profiles...),
		})
	case remove:
		name, err := requiredName(in)
		if err != nil {
			return nil, err
		}
		return f.change(in, "remove", core.Environment{Name: name})
	default:
		return f.list(in)
	}
}

func requiredName(in core.Invocation) (string, error) {
	name := strings.TrimSpace(in.Option("name"))
	if name == "" {
		return "", inputError(msgMissingName, "")
	}
	return name, nil
}

func (f *Feature) change(in core.Invocation, action string, env core.Environment) (Payload, error) {
	stage := msgStageSave
	if action == "remove" {
		stage = msgStageRemove
	}
	cat, err := core.RunStageValue(in.Reporter, action, stage, func() (envconfig.Catalog, error) {
		switch action {
		case "add":
			return envconfig.Add(in.Target.ProjectDir, env)
		case "remove":
			return envconfig.Remove(in.Target.ProjectDir, env.Name)
		default:
			return envconfig.Catalog{}, inputError(msgActionConflict, action)
		}
	})
	if err != nil {
		return Payload{}, mapError(err)
	}
	return payloadFrom(cat, action), nil
}

func (f *Feature) list(in core.Invocation) (Payload, error) {
	cat, err := core.RunStageValue(in.Reporter, "list", msgStageList, func() (envconfig.Catalog, error) {
		return envconfig.Load(in.Target.ProjectDir)
	})
	if err != nil {
		return Payload{}, mapError(err)
	}
	return payloadFrom(cat, "list"), nil
}

func payloadFrom(cat envconfig.Catalog, action string) Payload {
	report := Report{
		Action:     action,
		ConfigFile: cat.Path,
		Aliases:    make([]AliasView, 0, len(cat.Environments)),
	}
	for _, env := range cat.Environments {
		report.Aliases = append(report.Aliases, AliasView{
			Name:     env.Name,
			Files:    nonNil(env.Files),
			EnvFiles: nonNil(env.EnvFiles),
			Profiles: nonNil(env.Profiles),
		})
	}
	return Payload{Environments: report}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func (f *Feature) wizardSteps(in core.Invocation) core.WizardSpec {
	return core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{
				Name: "name", Title: msgWizardNameTitle, Prompt: msgWizardNamePrompt,
				AllowCustom: true, CustomHint: msgWizardNameHint,
			},
			{
				Name: "files", Title: msgWizardFilesTitle, Prompt: msgWizardFilesPrompt,
				AllowCustom: true, CustomHint: msgWizardFilesHint,
				Default: strings.Join(in.Target.Files, ", "),
			},
			{
				Name: "env_files", Title: msgWizardEnvFilesTitle, Prompt: msgWizardEnvFilesPrompt,
				AllowCustom: true, CustomHint: msgWizardEnvFilesHint, Optional: true,
			},
			{
				Name: "profiles", Title: msgWizardProfilesTitle, Prompt: msgWizardProfilesPrompt,
				AllowCustom: true, CustomHint: msgWizardProfilesHint, Optional: true,
			},
		},
	}
}

// WizardPreview implements core.Wizard. It validates the collected answers and
// renders the exact .dcraft.yaml section; nothing is written. It never reads
// env-file values.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	env, err := environmentFromAnswers(answers)
	if err != nil {
		return "", err
	}
	section, err := envconfig.RenderAdd(f.projectDir, env)
	if err != nil {
		return "", mapError(err)
	}
	var b strings.Builder
	b.WriteString(msgWizardPreviewTitle)
	b.WriteString("\n\n")
	b.WriteString(section)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s%s\n", msgWizardConfigPrefix, filepath.Join(f.projectDir, envconfig.FileName))
	b.WriteString(msgWizardPreviewConfirm)
	b.WriteString("\n")
	b.WriteString(msgDisclosure)
	return strings.TrimRight(b.String(), "\n"), nil
}

func (f *Feature) applyWizard(in core.Invocation) (Payload, error) {
	env, err := environmentFromAnswers(in.Options)
	if err != nil {
		return Payload{}, err
	}
	return f.change(in, "add", env)
}

func environmentFromAnswers(answers map[string]string) (core.Environment, error) {
	name := strings.TrimSpace(answers["name"])
	if name == "" {
		return core.Environment{}, inputError(msgMissingName, "")
	}
	return core.Environment{
		Name:     name,
		Files:    splitAnswers(answers["files"]),
		EnvFiles: splitAnswers(answers["env_files"]),
		Profiles: splitAnswers(answers["profiles"]),
	}, nil
}

// splitAnswers accepts the wizard's single-line comma-separated input and the
// multi-select newline form. Empty tokens are ignored; explicit empty entries
// inside a list are rejected later by the storage validation when the user
// typed a stray separator at the end.
func splitAnswers(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func mapError(err error) error {
	var ie *envconfig.InputError
	if errors.As(err, &ie) {
		return inputError(ie.Message, ie.Detail)
	}
	return err
}

func inputError(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInvalidOption,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgInputActionOne, msgInputActionTwo},
	})
}

func countTrue(values ...bool) int {
	n := 0
	for _, value := range values {
		if value {
			n++
		}
	}
	return n
}
