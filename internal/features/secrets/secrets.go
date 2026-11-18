package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Feature manages project-local runtime secret references. It stores only
// references and shares all validation with the CLI action path and the TUI
// wizard's apply path.
type Feature struct {
	identity stack.Identity
	runtime  *secretref.Runtime
}

// New builds the feature. identity validates service names against the
// resolved Compose configuration; runtime owns per-stack materialization.
func New(identity stack.Identity, runtime *secretref.Runtime) *Feature {
	return &Feature{identity: identity, runtime: runtime}
}

// Payload is the machine-readable result of the secrets command.
type Payload struct {
	Secrets Report `json:"secrets"`
}

// Report describes the saved references and the delivery effect. It contains
// no resolved values.
type Report struct {
	Action       string          `json:"action"`
	ConfigFile   string          `json:"config_file"`
	OverrideFile string          `json:"override_file,omitempty"`
	References   []ReferenceView `json:"references"`
	Notices      []string        `json:"notices"`
}

// ReferenceView is one saved reference plus its user-visible effect.
type ReferenceView struct {
	Service    string `json:"service"`
	Phase      string `json:"phase"`
	Delivery   string `json:"delivery"`
	Source     string `json:"source"`
	Ref        string `json:"ref"`
	Target     string `json:"target"`
	TargetPath string `json:"target_path,omitempty"`
	Effect     string `json:"effect"`
}

// Descriptor implements core.Feature.
func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:             "secrets",
		Title:            msgTitle,
		Summary:          msgSummary,
		Usage:            msgUsage,
		SupportsServices: true,
		Wizard:           true,
		Flags: []core.Flag{
			{Name: "add", Kind: core.FlagBool, Usage: msgFlagAdd},
			{Name: "remove", Kind: core.FlagBool, Usage: msgFlagRemove},
			{Name: "list", Kind: core.FlagBool, Usage: msgFlagList},
			{Name: "phase", Kind: core.FlagString, Default: string(secretref.PhaseRuntime), Usage: msgFlagPhase},
			{Name: "delivery", Kind: core.FlagString, Usage: msgFlagDelivery},
			{Name: "source", Kind: core.FlagString, Usage: msgFlagSource},
			{Name: "ref", Kind: core.FlagString, Usage: msgFlagRef},
			{Name: "target", Kind: core.FlagString, Usage: msgFlagTarget},
		},
	}
}

// Run implements core.Feature. Options["wizard"]="steps" returns a WizardSpec;
// "apply" saves the answers collected by the TUI; otherwise the explicit CLI
// flags choose list/add/remove.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
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
		return f.add(ctx, in, referenceFromOptions(in, in.Services))
	case remove:
		return f.remove(ctx, in, in.Services)
	default:
		return f.list(in, "list", nil)
	}
}

func (f *Feature) applyWizard(ctx context.Context, in core.Invocation) (any, error) {
	service := strings.TrimSpace(in.Option("service"))
	ref := secretref.Reference{
		Service:  service,
		Phase:    secretref.Phase(strings.TrimSpace(in.Option("phase"))),
		Delivery: secretref.Delivery(strings.TrimSpace(in.Option("delivery"))),
		Source:   secretref.SourceType(strings.TrimSpace(in.Option("source"))),
		Ref:      strings.TrimSpace(in.Option("ref")),
		Target:   strings.TrimSpace(in.Option("target")),
	}
	return f.add(ctx, in, ref)
}

func (f *Feature) add(ctx context.Context, in core.Invocation, ref secretref.Reference) (any, error) {
	service, err := singleService(in, ref.Service)
	if err != nil {
		return nil, err
	}
	ref.Service = service
	if ref.Phase == "" {
		ref.Phase = secretref.PhaseRuntime
	}
	if err := requireAddInput(ref); err != nil {
		return nil, err
	}
	err = core.RunStage(in.Reporter, "validate", msgStageValidate, func() error {
		if f.identity == nil {
			return core.Fail(core.Diagnostic{
				Category:    core.CategoryInternal,
				Severity:    core.SeverityError,
				Message:     msgNoCompose,
				NextActions: []string{msgNoComposeAction},
			})
		}
		cfg, err := f.identity.ConfigInfo(ctx, in.Target)
		if err != nil {
			return err
		}
		newCatalog := secretref.Catalog{References: []secretref.Reference{ref}}
		return secretref.ValidateServices(newCatalog, cfg.Services)
	})
	if err != nil {
		return nil, err
	}
	cat, err := core.RunStageValue(in.Reporter, "save", msgStageSave, func() (secretref.Catalog, error) {
		return secretref.Add(in.Target.ProjectDir, ref)
	})
	if err != nil {
		return nil, err
	}
	if err := core.RunStage(in.Reporter, "regenerate", msgStageRegenerate, func() error {
		return f.regenerate(in.Target)
	}); err != nil {
		return nil, err
	}
	return f.buildReport(in, "add", cat, []string{msgAdded})
}

func (f *Feature) remove(ctx context.Context, in core.Invocation, services []string) (any, error) {
	service, err := singleService(in, "")
	if err != nil {
		return nil, err
	}
	delivery := secretref.Delivery(strings.TrimSpace(in.Option("delivery")))
	target := strings.TrimSpace(in.Option("target"))
	if delivery == "" || target == "" {
		return nil, inputError(msgRemoveNeeds, "delivery,target")
	}
	before, err := secretref.Load(in.Target.ProjectDir)
	if err != nil {
		return nil, err
	}
	var removed *secretref.Reference
	for i := range before.References {
		ref := before.References[i]
		if ref.Service == service && ref.Delivery == delivery && ref.EffectiveTarget() == target {
			removed = &ref
			break
		}
	}
	if removed == nil {
		return nil, inputError(fmt.Sprintf(msgRemoveMissing, service, string(delivery), target), "")
	}
	cat, err := core.RunStageValue(in.Reporter, "remove", msgStageRemove, func() (secretref.Catalog, error) {
		return secretref.Remove(in.Target.ProjectDir, service, delivery, target)
	})
	if err != nil {
		return nil, err
	}
	if err := core.RunStage(in.Reporter, "regenerate", msgStageRegenerate, func() error {
		return f.regenerate(in.Target)
	}); err != nil {
		return nil, err
	}
	notices := []string{msgRemoved}
	if removed.Delivery == secretref.DeliveryFile {
		notices = append(notices, msgRemovedFileNotice)
	}
	return f.buildReport(in, "remove", cat, notices)
}

func (f *Feature) list(in core.Invocation, action string, notices []string) (any, error) {
	cat, err := core.RunStageValue(in.Reporter, "list", msgStageList, func() (secretref.Catalog, error) {
		return secretref.Load(in.Target.ProjectDir)
	})
	if err != nil {
		return nil, err
	}
	return f.buildReport(in, action, cat, notices)
}

func (f *Feature) regenerate(target core.Target) error {
	if f.runtime == nil {
		return nil
	}
	if err := f.runtime.RegenerateOverride(target); err != nil {
		return core.FailCause(err, core.Diagnostic{
			Category:    core.CategorySecretApplyFailed,
			Severity:    core.SeverityError,
			Message:     msgOverrideFailed,
			NextActions: []string{msgOverrideAction},
		})
	}
	return nil
}

func (f *Feature) buildReport(in core.Invocation, action string, cat secretref.Catalog, notices []string) (Payload, error) {
	views := make([]ReferenceView, 0, len(cat.References))
	for _, ref := range cat.References {
		views = append(views, ReferenceView{
			Service:    ref.Service,
			Phase:      string(ref.Phase),
			Delivery:   string(ref.Delivery),
			Source:     string(ref.Source),
			Ref:        ref.Ref,
			Target:     ref.EffectiveTarget(),
			TargetPath: ref.TargetPath(),
			Effect:     effectText(ref),
		})
	}
	report := Report{
		Action:     action,
		ConfigFile: cat.Path,
		References: views,
		Notices:    append([]string{}, notices...),
	}
	if f.runtime != nil {
		if path, err := f.runtime.OverridePath(in.Target); err == nil {
			if _, statErr := os.Stat(path); statErr == nil {
				report.OverrideFile = path
			}
		}
	}
	if len(views) > 0 {
		report.Notices = append(report.Notices, disclosureMessages(views)...)
	}
	report.Notices = append(report.Notices, msgMigrationGuide)
	if report.Notices == nil {
		report.Notices = []string{}
	}
	return Payload{Secrets: report}, nil
}

func (f *Feature) wizardSteps(ctx context.Context, in core.Invocation) (any, error) {
	cfg, err := core.RunStageValue(in.Reporter, "prepare_wizard", msgStageWizard, func() (stack.ConfigInfo, error) {
		if f.identity == nil {
			return stack.ConfigInfo{}, core.Fail(core.Diagnostic{
				Category:    core.CategoryInternal,
				Severity:    core.SeverityError,
				Message:     msgNoCompose,
				NextActions: []string{msgNoComposeAction},
			})
		}
		return f.identity.ConfigInfo(ctx, in.Target)
	})
	if err != nil {
		return nil, err
	}
	if len(cfg.Services) == 0 {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryServiceUnknown,
			Severity:    core.SeverityError,
			Message:     msgNoServices,
			NextActions: []string{msgNoServicesAction},
		})
	}
	serviceOptions := make([]core.WizardOption, 0, len(cfg.Services))
	for _, name := range cfg.Services {
		serviceOptions = append(serviceOptions, core.WizardOption{Value: name, Label: name})
	}
	spec := core.WizardSpec{
		Title: msgWizardTitle,
		Steps: []core.WizardStep{
			{
				Name: "service", Title: msgWizardServiceTitle,
				Prompt: msgWizardServicePrompt, Options: serviceOptions,
			},
			{
				Name: "phase", Title: msgWizardPhaseTitle,
				Prompt: msgWizardPhasePrompt, Default: string(secretref.PhaseRuntime),
				Options: []core.WizardOption{
					{Value: string(secretref.PhaseRuntime), Label: msgWizardPhaseRuntime},
				},
			},
			{
				Name: "delivery", Title: msgWizardDeliveryTitle,
				Prompt: msgWizardDeliveryPrompt, Default: string(secretref.DeliveryEnvironment),
				Options: []core.WizardOption{
					{Value: string(secretref.DeliveryEnvironment), Label: msgWizardDeliveryEnv},
					{Value: string(secretref.DeliveryFile), Label: msgWizardDeliveryFile},
				},
			},
			{
				Name: "source", Title: msgWizardSourceTitle,
				Prompt: msgWizardSourcePrompt, Default: string(secretref.SourceEnv),
				Options: []core.WizardOption{
					{Value: string(secretref.SourceEnv), Label: msgWizardSourceEnv},
					{Value: string(secretref.SourceFile), Label: msgWizardSourceFile},
				},
			},
			{
				Name: "ref", Title: msgWizardRefTitle,
				Prompt: msgWizardRefPrompt, AllowCustom: true,
				CustomHint: msgWizardRefHint,
			},
		},
	}
	return spec, nil
}

// WizardPreview implements core.Wizard.
func (f *Feature) WizardPreview(answers map[string]string) (string, error) {
	ref := secretref.Reference{
		Service:  strings.TrimSpace(answers["service"]),
		Phase:    secretref.Phase(strings.TrimSpace(answers["phase"])),
		Delivery: secretref.Delivery(strings.TrimSpace(answers["delivery"])),
		Source:   secretref.SourceType(strings.TrimSpace(answers["source"])),
		Ref:      strings.TrimSpace(answers["ref"]),
	}
	if ref.Phase == "" {
		ref.Phase = secretref.PhaseRuntime
	}
	if err := secretref.Validate(ref); err != nil {
		return "", inputError(msgPreviewInvalid+" "+err.Error(), "")
	}
	var b strings.Builder
	b.WriteString(msgPreviewTitle)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewService, ref.Service)
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewPhase, phaseLabel(ref.Phase))
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewDelivery, deliveryLabel(ref.Delivery, ref.EffectiveTarget()))
	fmt.Fprintf(&b, "%s: %s (%s)\n", msgPreviewSource, sourceLabel(ref.Source), ref.Ref)
	fmt.Fprintf(&b, "%s: %s\n", msgPreviewEffect, effectText(ref))
	b.WriteString(msgPreviewNote)
	if ref.Delivery == secretref.DeliveryEnvironment {
		b.WriteString("\n")
		b.WriteString(msgPreviewEnvVisible)
	} else {
		b.WriteString("\n")
		b.WriteString(msgPreviewFileApp)
	}
	return b.String(), nil
}

func referenceFromOptions(in core.Invocation, services []string) secretref.Reference {
	service := strings.TrimSpace(in.Option("service"))
	if service == "" && len(services) == 1 {
		service = strings.TrimSpace(services[0])
	}
	return secretref.Reference{
		Service:  service,
		Phase:    secretref.Phase(strings.TrimSpace(in.Option("phase"))),
		Delivery: secretref.Delivery(strings.TrimSpace(in.Option("delivery"))),
		Source:   secretref.SourceType(strings.TrimSpace(in.Option("source"))),
		Ref:      strings.TrimSpace(in.Option("ref")),
		Target:   strings.TrimSpace(in.Option("target")),
	}
}

func singleService(in core.Invocation, fromOption string) (string, error) {
	services := make([]string, 0, len(in.Services))
	for _, name := range in.Services {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			services = append(services, trimmed)
		}
	}
	if fromOption != "" {
		if len(services) > 0 && (len(services) > 1 || services[0] != fromOption) {
			return "", inputError(msgOneService, "")
		}
		return fromOption, nil
	}
	if len(services) != 1 {
		return "", inputError(msgOneService, "")
	}
	return services[0], nil
}

func requireAddInput(ref secretref.Reference) error {
	var missing []string
	if ref.Delivery == "" {
		missing = append(missing, "delivery")
	}
	if ref.Source == "" {
		missing = append(missing, "source")
	}
	if ref.Ref == "" {
		missing = append(missing, "ref")
	}
	if len(missing) > 0 {
		return inputError(fmt.Sprintf(msgMissingInput, strings.Join(missing, ", ")), strings.Join(missing, ","))
	}
	return nil
}

func effectText(ref secretref.Reference) string {
	target := ref.EffectiveTarget()
	switch ref.Delivery {
	case secretref.DeliveryEnvironment:
		return fmt.Sprintf(msgEffectEnv, target)
	case secretref.DeliveryFile:
		return fmt.Sprintf(msgEffectFile, ref.TargetPath())
	default:
		return msgEffectUnknown
	}
}

func disclosureMessages(views []ReferenceView) []string {
	var notices []string
	hasEnv, hasFile := false, false
	for _, view := range views {
		switch secretref.Delivery(view.Delivery) {
		case secretref.DeliveryEnvironment:
			hasEnv = true
		case secretref.DeliveryFile:
			hasFile = true
		}
	}
	if hasEnv {
		notices = append(notices, msgEnvVisible)
	}
	if hasFile {
		notices = append(notices, msgFileApp)
	}
	return notices
}

func phaseLabel(phase secretref.Phase) string {
	if phase == secretref.PhaseRuntime {
		return msgPhaseRuntime
	}
	return string(phase)
}

func deliveryLabel(delivery secretref.Delivery, target string) string {
	switch delivery {
	case secretref.DeliveryEnvironment:
		return fmt.Sprintf(msgDeliveryEnvLabel, target)
	case secretref.DeliveryFile:
		return fmt.Sprintf(msgDeliveryFileLabel, "/run/secrets/"+target)
	default:
		return string(delivery)
	}
}

func sourceLabel(source secretref.SourceType) string {
	switch source {
	case secretref.SourceEnv:
		return msgSourceEnv
	case secretref.SourceFile:
		return msgSourceFile
	default:
		return string(source)
	}
}

func inputError(message, detail string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategorySecretInputInvalid,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      detail,
		NextActions: []string{msgInputActionOne, msgInputActionTwo},
	})
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
