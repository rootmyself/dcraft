package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
)

type fakeBuildModel struct {
	defs []stack.ServiceDefinition
	err  error
}

func (f fakeBuildModel) UpdateModel(context.Context, core.Target) (stack.UpdateModel, error) {
	return stack.UpdateModel{Services: f.defs}, f.err
}

func testBuildFeature(t *testing.T, builds map[string]bool, services ...string) (*Feature, string, *secretref.Runtime) {
	t.Helper()
	f, dir, rt := testFeature(t, services...)
	defs := make([]stack.ServiceDefinition, 0, len(services))
	for _, name := range services {
		defs = append(defs, stack.ServiceDefinition{Name: name, Build: builds[name]})
	}
	f.builds = []BuildModelReader{fakeBuildModel{defs: defs}}
	return f, dir, rt
}

func TestAddBuildPhaseRequiresBuildService(t *testing.T) {
	f, dir, _ := testBuildFeature(t, map[string]bool{"api": true}, "api", "web")

	_, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "phase": "build", "source": "env", "ref": "BUILD_TOKEN",
	}, "web"))
	if err == nil {
		t.Fatal("add accepted a non-build service")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryBuildServiceInvalid {
		t.Fatalf("error = %v, want build_service_invalid", err)
	}

	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "phase": "build", "source": "env", "ref": "BUILD_TOKEN",
	}, "api"))
	if err != nil {
		t.Fatalf("add build: %v", err)
	}
	report := payload.(Payload).Secrets
	if len(report.References) != 1 {
		t.Fatalf("references = %+v", report.References)
	}
	ref := report.References[0]
	if ref.Phase != "build" || ref.Delivery != "file" || ref.Target != "BUILD_TOKEN" {
		t.Fatalf("reference = %+v", ref)
	}
	if !strings.Contains(ref.Effect, "BuildKit") || !strings.Contains(ref.Effect, "BUILD_TOKEN") {
		t.Fatalf("effect = %q", ref.Effect)
	}
	if report.BuildOverrideFile == "" {
		t.Fatalf("build override path missing: %+v", report)
	}
	if _, err := os.Stat(report.BuildOverrideFile); err != nil {
		t.Fatalf("build override not written: %v", err)
	}
	notices := strings.Join(report.Notices, "\n")
	for _, want := range []string{"캐시", "--no-cache", "Dockerfile", "자동으로 수정하지 않음"} {
		if !strings.Contains(notices, want) {
			t.Fatalf("notices missing %q:\n%s", want, notices)
		}
	}
	cat, err := secretref.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.References) != 1 || cat.References[0].Phase != secretref.PhaseBuild || cat.References[0].Delivery != secretref.DeliveryFile {
		t.Fatalf("saved = %+v", cat.References)
	}
}

func TestAddBuildPhaseRejectsEnvironmentDelivery(t *testing.T) {
	f, dir, _ := testBuildFeature(t, map[string]bool{"api": true}, "api")
	_, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "phase": "build", "delivery": "environment", "source": "env", "ref": "BUILD_TOKEN",
	}, "api"))
	if err == nil {
		t.Fatal("add accepted environment delivery for the build phase")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretInputInvalid {
		t.Fatalf("error = %v, want secret_input_invalid", err)
	}
	if !strings.Contains(ce.Diagnostics[0].Message, "BuildKit") {
		t.Fatalf("message = %q", ce.Diagnostics[0].Message)
	}
}

func TestWizardOffersBuildAndFixesDeliveryToBuildKit(t *testing.T) {
	f, dir, _ := testBuildFeature(t, map[string]bool{"api": true}, "api")
	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{"wizard": "steps"}))
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	spec := payload.(core.WizardSpec)
	var phaseOptions []string
	var deliveryPrompt string
	for _, step := range spec.Steps {
		if step.Name == "phase" {
			for _, option := range step.Options {
				phaseOptions = append(phaseOptions, option.Value)
			}
		}
		if step.Name == "delivery" {
			deliveryPrompt = step.Prompt
		}
	}
	if !contains(phaseOptions, "build") || !contains(phaseOptions, "runtime") {
		t.Fatalf("phase options = %v", phaseOptions)
	}
	if !strings.Contains(deliveryPrompt, "BuildKit") {
		t.Fatalf("delivery prompt does not explain the fixed build delivery: %q", deliveryPrompt)
	}

	answers := map[string]string{
		"service": "api", "phase": "build", "delivery": "environment",
		"source": "env", "ref": "BUILD_TOKEN",
	}
	preview, err := f.WizardPreview(answers)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"api", "BuildKit", "BUILD_TOKEN", "캐시", "--no-cache", "자동으로 수정하지 않음"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	payload, err = f.Run(context.Background(), invocation(dir, map[string]string{
		"wizard": "apply", "service": "api", "phase": "build",
		"delivery": "environment", "source": "env", "ref": "BUILD_TOKEN",
	}))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	report := payload.(Payload).Secrets
	if len(report.References) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.References[0].Phase != "build" || report.References[0].Delivery != "file" {
		t.Fatalf("wizard did not fix build delivery to the BuildKit mount: %+v", report.References[0])
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "BuildKit") {
		t.Fatalf("fixed-delivery notice missing: %v", report.Notices)
	}
}

func TestBuildListReportsManagedFilesAndNoValues(t *testing.T) {
	f, dir, _ := testBuildFeature(t, map[string]bool{"api": true}, "api")
	if _, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "phase": "build", "source": "env", "ref": "BUILD_TOKEN",
	}, "api")); err != nil {
		t.Fatalf("add: %v", err)
	}
	payload, err := f.Run(context.Background(), invocation(dir, nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	report := payload.(Payload).Secrets
	if report.BuildOverrideFile == "" {
		t.Fatalf("list did not report the build override: %+v", report)
	}
	text := report.BuildOverrideFile + "\n" + strings.Join(report.Notices, "\n")
	if strings.Contains(text, "value") || strings.Contains(text, "BUILD_TOKEN=") {
		t.Fatalf("list output contains a value: %s", text)
	}
}
