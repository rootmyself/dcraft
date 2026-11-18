package initconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeValidator struct {
	result  Validation
	err     error
	calls   int
	dir     string
	content []byte
}

func (f *fakeValidator) Validate(ctx context.Context, dir string, content []byte) (Validation, error) {
	f.calls++
	f.dir = dir
	f.content = append([]byte{}, content...)
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	if f.err != nil {
		return Validation{}, f.err
	}
	return f.result, nil
}

type fakeSaver struct {
	err       error
	calls     int
	path      string
	content   []byte
	overwrite bool
}

func (f *fakeSaver) Save(ctx context.Context, path string, content []byte, overwrite bool) error {
	f.calls++
	f.path = path
	f.content = append([]byte{}, content...)
	f.overwrite = overwrite
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.err
}

func fakeValidation(service string) Validation {
	return Validation{
		Checked:        true,
		Command:        validationCommand,
		ProjectName:    "hello-world",
		DefaultNetwork: "hello-world_default",
		Services:       []string{service},
		Volumes:        map[string]string{"cache": "hello-world_cache"},
	}
}

func testInvocation(project string, options map[string]string, lists map[string][]string) core.Invocation {
	return core.Invocation{
		Target:      core.Target{ProjectDir: project},
		Options:     options,
		OptionLists: lists,
	}
}

func imageOptions(service string) map[string]string {
	return map[string]string{
		"from": "image", "service-name": service, "image": "busybox:1.36",
		"port": "8080:80", "volume": "./data:/data,cache:/cache", "env-var": "FOO=bar",
		"restart": "unless-stopped",
	}
}

func TestRunImageModeWritesValidatedFileAndReportsEffects(t *testing.T) {
	project := t.TempDir()
	validator := &fakeValidator{result: fakeValidation("web")}
	saver := &fakeSaver{}
	controller := New(validator, saver)

	payload, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	report := payload.(Payload).Init
	if report.Path != filepath.Join(project, "compose.yaml") {
		t.Fatalf("path = %q", report.Path)
	}
	if report.ProjectName != "hello-world" {
		t.Fatalf("project name = %q", report.ProjectName)
	}
	if !report.Changed || report.Overwritten {
		t.Fatalf("changed=%v overwritten=%v", report.Changed, report.Overwritten)
	}
	if !report.Validation.Checked || report.Validation.Command != validationCommand {
		t.Fatalf("validation = %+v", report.Validation)
	}
	if report.NextCommand != msgNextCommand {
		t.Fatalf("next command = %q", report.NextCommand)
	}
	for _, want := range []string{"image: busybox:1.36", "8080:80", "./data:/data", "cache:/cache", "FOO: bar", "restart: unless-stopped"} {
		if !strings.Contains(report.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, report.Content)
		}
	}
	if validator.calls != 1 || validator.dir != project {
		t.Fatalf("validator calls=%d dir=%q", validator.calls, validator.dir)
	}
	if string(validator.content) != report.Content {
		t.Fatal("validator did not receive the exact saved content")
	}
	if saver.calls != 1 || saver.overwrite {
		t.Fatalf("saver calls=%d overwrite=%v", saver.calls, saver.overwrite)
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "평문") {
		t.Fatalf("plaintext environment notice missing: %v", report.Notices)
	}
	foundNetwork := false
	foundVolume := false
	for _, resource := range report.Resources {
		if resource.Kind == "network" && resource.Name == "hello-world_default" {
			foundNetwork = true
		}
		if resource.Kind == "named_volume" && resource.Name == "hello-world_cache" {
			foundVolume = true
		}
	}
	if !foundNetwork {
		t.Fatalf("network resource missing: %+v", report.Resources)
	}
	if !foundVolume {
		t.Fatalf("resolved named volume missing: %+v", report.Resources)
	}
}

func TestRunDockerfileModeUsesTheSameGenerator(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\n")
	validator := &fakeValidator{result: fakeValidation("app")}
	saver := &fakeSaver{}
	controller := New(validator, saver)

	payload, err := controller.Run(context.Background(), testInvocation(project, map[string]string{
		"from": "dockerfile", "service-name": "app", "port": "127.0.0.1:8081:80",
	}, nil))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	content := payload.(Payload).Init.Content
	for _, want := range []string{"build:", "context: .", "dockerfile: Dockerfile", "127.0.0.1:8081:80"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestRunRefusesExistingComposeWithoutExplicitDestination(t *testing.T) {
	project := t.TempDir()
	existing := filepath.Join(project, "compose.yml")
	writeTestFile(t, existing, "services:\n  old:\n    image: busybox\n")
	validator := &fakeValidator{result: fakeValidation("web")}
	saver := &fakeSaver{}
	controller := New(validator, saver)

	_, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err == nil {
		t.Fatal("existing configuration was replaced")
	}
	ce, ok := err.(*core.Error)
	if !ok || ce.Diagnostics[0].Category != core.CategoryInitExistingConfiguration {
		t.Fatalf("error = %v, want init_existing_configuration", err)
	}
	if !strings.Contains(ce.Diagnostics[0].Message+ce.Diagnostics[0].Detail, existing) {
		t.Fatalf("refusal does not point to %s: %+v", existing, ce.Diagnostics[0])
	}
	if validator.calls != 0 || saver.calls != 0 {
		t.Fatalf("validator=%d saver=%d, want no work", validator.calls, saver.calls)
	}
	data, _ := os.ReadFile(existing)
	if !strings.Contains(string(data), "image: busybox") {
		t.Fatal("existing compose.yml changed")
	}
}

func TestRunReplacesOnlyTheExplicitlyChosenExistingFile(t *testing.T) {
	project := t.TempDir()
	existing := filepath.Join(project, "compose.yml")
	writeTestFile(t, existing, "services:\n  old:\n    image: busybox\n")

	// A different new destination is still refused while a standard Compose
	// file exists: it would silently take over Compose discovery.
	controller := New(&fakeValidator{result: fakeValidation("web")}, &fakeSaver{})
	options := imageOptions("web")
	options["output"] = "compose.yaml"
	_, err := controller.Run(context.Background(), testInvocation(project, options, nil))
	if err == nil {
		t.Fatal("a second standard Compose file was created")
	}

	options["output"] = "compose.yml"
	saver := &fakeSaver{}
	controller = New(&fakeValidator{result: fakeValidation("web")}, saver)
	payload, err := controller.Run(context.Background(), testInvocation(project, options, nil))
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if !payload.(Payload).Init.Overwritten {
		t.Fatal("overwrite was not reported")
	}
	if !saver.overwrite {
		t.Fatal("saver did not receive overwrite=true")
	}
}

func TestRequiredApprovalDependsOnlyOnTheDestinationAndExistingFiles(t *testing.T) {
	project := t.TempDir()
	controller := New(&fakeValidator{}, &fakeSaver{})

	kind, err := controller.RequiredApproval(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err != nil || kind != core.ApprovalNone {
		t.Fatalf("new file approval = %q err=%v", kind, err)
	}

	writeTestFile(t, filepath.Join(project, "compose.yaml"), "services: {}\n")
	_, err = controller.RequiredApproval(context.Background(), testInvocation(project, imageOptions("web"), nil))
	ce, ok := err.(*core.Error)
	if !ok || !strings.Contains(ce.Diagnostics[0].Detail, "--output") {
		t.Fatalf("default existing file error = %v, want an explicit destination", err)
	}
	options := imageOptions("web")
	options["output"] = "compose.yaml"
	kind, err = controller.RequiredApproval(context.Background(), testInvocation(project, options, nil))
	if err != nil || kind != core.ApprovalFileOverwrite {
		t.Fatalf("existing file approval = %q err=%v", kind, err)
	}

	if err := os.Remove(filepath.Join(project, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(project, "compose.yml"), "services: {}\n")
	options = imageOptions("web")
	options["output"] = "compose.yaml"
	kind, err = controller.RequiredApproval(context.Background(), testInvocation(project, options, nil))
	if err != nil || kind != core.ApprovalNone {
		t.Fatalf("mismatched destination approval = %q err=%v", kind, err)
	}
	options["output"] = "compose.yml"
	kind, err = controller.RequiredApproval(context.Background(), testInvocation(project, options, nil))
	if err != nil || kind != core.ApprovalFileOverwrite {
		t.Fatalf("matched destination approval = %q err=%v", kind, err)
	}

	customProject := t.TempDir()
	writeTestFile(t, filepath.Join(customProject, "custom.yaml"), "services: {}\n")
	options = imageOptions("web")
	options["output"] = "custom.yaml"
	kind, err = controller.RequiredApproval(context.Background(), testInvocation(customProject, options, nil))
	if err != nil || kind != core.ApprovalFileOverwrite {
		t.Fatalf("non-standard existing destination approval = %q err=%v", kind, err)
	}
}

func TestRunRejectsInvalidComposeOutputBeforeSaving(t *testing.T) {
	project := t.TempDir()
	validator := &fakeValidator{err: core.Fail(core.Diagnostic{
		Category: core.CategoryInitInvalid, Severity: core.SeverityError, Message: "rejected",
	})}
	saver := &fakeSaver{}
	controller := New(validator, saver)
	_, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err == nil {
		t.Fatal("invalid output was saved")
	}
	if saver.calls != 0 {
		t.Fatal("saver ran after validation failed")
	}
	if _, statErr := os.Stat(filepath.Join(project, "compose.yaml")); !os.IsNotExist(statErr) {
		t.Fatal("a file was created despite validation failure")
	}
}

func TestRunRejectsAValidatedFileWithoutTheRequestedService(t *testing.T) {
	project := t.TempDir()
	validator := &fakeValidator{result: fakeValidation("other")}
	saver := &fakeSaver{}
	controller := New(validator, saver)
	_, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err == nil {
		t.Fatal("a project without the requested service was accepted")
	}
	ce := err.(*core.Error)
	if ce.Diagnostics[0].Category != core.CategoryInitInvalid {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
	if saver.calls != 0 {
		t.Fatal("saver ran")
	}
}

func TestRunMapsSaveFailuresAndNeverClaimsSuccess(t *testing.T) {
	project := t.TempDir()
	cases := []struct {
		err      error
		category core.Category
	}{
		{errors.New("disk full"), core.CategoryInitFailed},
		{ErrExists, core.CategoryInitExistingConfiguration},
		{ErrSymlink, core.CategoryInitInputInvalid},
	}
	for _, tc := range cases {
		saver := &fakeSaver{err: tc.err}
		controller := New(&fakeValidator{result: fakeValidation("web")}, saver)
		_, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
		if err == nil {
			t.Fatalf("save error %v was reported as success", tc.err)
		}
		ce := err.(*core.Error)
		if ce.Diagnostics[0].Category != tc.category {
			t.Fatalf("save error %v category = %q, want %q", tc.err, ce.Diagnostics[0].Category, tc.category)
		}
	}
}

func TestPlanShowsTheExactFileAndDoesNotSave(t *testing.T) {
	project := t.TempDir()
	validator := &fakeValidator{result: fakeValidation("web")}
	saver := &fakeSaver{}
	controller := New(validator, saver)
	plan, err := controller.Plan(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, want := range []string{
		msgPreviewTitle,
		filepath.Join(project, "compose.yaml"),
		"hello-world",
		"image: busybox:1.36",
		"8080:80",
		"dcraft up",
		"저장은 실행이 아닙니다",
	} {
		if !strings.Contains(plan.Text, want) {
			t.Fatalf("plan missing %q:\n%s", want, plan.Text)
		}
	}
	if saver.calls != 0 {
		t.Fatal("plan saved a file")
	}
}

func TestPlanRefusesAnExistingConfigurationBeforeValidation(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "compose.yaml"), "services: {}\n")
	validator := &fakeValidator{result: fakeValidation("web")}
	controller := New(validator, &fakeSaver{})
	options := imageOptions("web")
	options["output"] = "other.yaml"
	_, err := controller.Plan(context.Background(), testInvocation(project, options, nil))
	if err == nil {
		t.Fatal("plan accepted an existing configuration")
	}
	if validator.calls != 0 {
		t.Fatal("plan validated after the existing-file refusal")
	}
}

func TestRunAndPlanPropagateCancellation(t *testing.T) {
	project := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	saver := &fakeSaver{}
	controller := New(&fakeValidator{result: fakeValidation("web")}, saver)
	_, err := controller.Run(ctx, testInvocation(project, imageOptions("web"), nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if saver.calls != 0 {
		t.Fatal("save ran after cancellation")
	}
}

func TestWizardStepsSuggestsFilesWithoutSelectingThemSilently(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\n")
	controller := New(&fakeValidator{}, &fakeSaver{})
	spec, err := controller.WizardSteps(context.Background(), testInvocation(project, nil, nil))
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if spec.Title == "" || len(spec.Steps) == 0 {
		t.Fatalf("spec = %+v", spec)
	}
	steps := map[string]core.WizardStep{}
	for _, step := range spec.Steps {
		steps[step.Name] = step
	}
	for _, name := range []string{"from", "service-name", "context", "dockerfile", "image", "output", "port", "volume", "env-var", "command", "restart"} {
		if _, ok := steps[name]; !ok {
			t.Fatalf("missing step %q in %v", name, steps)
		}
	}
	if steps["from"].Default != "dockerfile" {
		t.Fatalf("mode default = %q, want dockerfile suggestion", steps["from"].Default)
	}
	if steps["service-name"].Default != "app" {
		t.Fatalf("service default = %q", steps["service-name"].Default)
	}
	if steps["context"].DependsOn == nil || steps["context"].DependsOn.Name != "from" {
		t.Fatalf("context step is not conditional: %+v", steps["context"])
	}
	if steps["image"].DependsOn == nil || steps["image"].DependsOn.Values[0] != "image" {
		t.Fatalf("image step is not conditional: %+v", steps["image"])
	}
	for _, name := range []string{"port", "volume", "env-var", "command"} {
		if !steps[name].Optional || !steps[name].AllowCustom {
			t.Fatalf("optional step %q = %+v", name, steps[name])
		}
	}
	if steps["output"].Default != "compose.yaml" {
		t.Fatalf("output default = %q", steps["output"].Default)
	}
	if len(spec.RequiredApprovals) != 0 {
		t.Fatalf("new file approvals = %v", spec.RequiredApprovals)
	}
}

func TestWizardStepsPointsAtTheExistingFileAndRequiresOverwrite(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "compose.yml"), "services: {}\n")
	controller := New(&fakeValidator{}, &fakeSaver{})
	spec, err := controller.WizardSteps(context.Background(), testInvocation(project, nil, nil))
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if len(spec.RequiredApprovals) != 1 || spec.RequiredApprovals[0] != core.ApprovalFileOverwrite {
		t.Fatalf("required approvals = %v", spec.RequiredApprovals)
	}
	for _, step := range spec.Steps {
		if step.Name == "output" && step.Default != "compose.yml" {
			t.Fatalf("output default = %q, want the existing compose.yml", step.Default)
		}
	}
}

func TestWizardStepsWithoutADockerfileSuggestsImageMode(t *testing.T) {
	project := t.TempDir()
	controller := New(&fakeValidator{}, &fakeSaver{})
	spec, err := controller.WizardSteps(context.Background(), testInvocation(project, nil, nil))
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	for _, step := range spec.Steps {
		if step.Name == "from" && step.Default != "image" {
			t.Fatalf("mode default = %q, want image", step.Default)
		}
	}
}

func TestWizardPreviewUsesTheSameGeneratorAndNeverSaves(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\n")
	validator := &fakeValidator{result: fakeValidation("app")}
	saver := &fakeSaver{}
	controller := New(validator, saver)
	if _, err := controller.WizardSteps(context.Background(), testInvocation(project, nil, nil)); err != nil {
		t.Fatalf("steps: %v", err)
	}
	text, err := controller.WizardPreview(map[string]string{
		"from": "dockerfile", "service-name": "app", "context": ".", "dockerfile": "Dockerfile",
		"output": "compose.yaml", "port": "8080:80",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"build:", "dockerfile: Dockerfile", "8080:80", "dcraft up"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview missing %q:\n%s", want, text)
		}
	}
	if saver.calls != 0 {
		t.Fatal("preview saved a file")
	}
}

func TestWizardPreviewExplainsThatAnUnapprovedOverwriteCannotBeConfirmed(t *testing.T) {
	project := t.TempDir()
	validator := &fakeValidator{result: fakeValidation("web")}
	controller := New(validator, &fakeSaver{})
	// The wizard opened with no existing Compose file, so it did not collect
	// file_overwrite. Choosing an existing custom file must be refused before
	// the apply run rather than failing with a generic approval error.
	if _, err := controller.WizardSteps(context.Background(), testInvocation(project, nil, nil)); err != nil {
		t.Fatalf("steps: %v", err)
	}
	writeTestFile(t, filepath.Join(project, "custom.yaml"), "services: {}\n")
	_, err := controller.WizardPreview(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36", "output": "custom.yaml",
	})
	if err == nil {
		t.Fatal("unapproved overwrite preview succeeded")
	}
	if !strings.Contains(err.Error(), "file_overwrite") {
		t.Fatalf("error does not explain the approval: %v", err)
	}
}

func TestWizardPreviewRequiresAStartedWizard(t *testing.T) {
	controller := New(&fakeValidator{}, &fakeSaver{})
	_, err := controller.WizardPreview(map[string]string{"from": "image"})
	if err == nil {
		t.Fatal("preview without a target succeeded")
	}
}

func TestRunWritesAnExplicitOutsideDestinationWithAWarning(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	dest := filepath.Join(outside, "compose.yaml")
	saver := &fakeSaver{}
	controller := New(&fakeValidator{result: fakeValidation("web")}, saver)
	options := imageOptions("web")
	options["output"] = dest
	payload, err := controller.Run(context.Background(), testInvocation(project, options, nil))
	if err != nil {
		t.Fatalf("outside destination: %v", err)
	}
	report := payload.(Payload).Init
	if report.Path != dest {
		t.Fatalf("path = %q, want %q", report.Path, dest)
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "프로젝트 폴더 밖") {
		t.Fatalf("outside warning missing: %v", report.Notices)
	}
	if saver.calls != 1 || saver.path != dest {
		t.Fatalf("saver path = %q calls=%d", saver.path, saver.calls)
	}
}

func TestRunWarnsAboutAnOverrideThatBecomesActiveAfterCreation(t *testing.T) {
	project := t.TempDir()
	override := filepath.Join(project, "compose.override.yaml")
	writeTestFile(t, override, "services:\n  web:\n    environment:\n      FROM_OVERRIDE: \"yes\"\n")
	controller := New(&fakeValidator{result: fakeValidation("web")}, &fakeSaver{})
	payload, err := controller.Run(context.Background(), testInvocation(project, imageOptions("web"), nil))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(strings.Join(payload.(Payload).Init.Notices, "\n"), override) {
		t.Fatalf("override notice missing: %v", payload.(Payload).Init.Notices)
	}
}

func TestPlanNamesACustomDestinationFromItsOwnDirectory(t *testing.T) {
	project := t.TempDir()
	gen := filepath.Join(project, "gen")
	if err := os.Mkdir(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	controller := New(&fakeValidator{result: fakeValidation("web")}, &fakeSaver{})
	options := imageOptions("web")
	options["output"] = "gen/compose.yaml"
	plan, err := controller.Plan(context.Background(), testInvocation(project, options, nil))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := "cd '" + gen + "' && dcraft up -f 'compose.yaml'"
	if !strings.Contains(plan.Text, want) {
		t.Fatalf("plan does not name the custom destination command %q:\n%s", want, plan.Text)
	}
	if strings.Contains(plan.Text, "\n다음 명령: dcraft up\n") {
		t.Fatalf("plan claims the default command for a custom path:\n%s", plan.Text)
	}
}

func TestNextCommandKeepsAnExplicitProjectName(t *testing.T) {
	project := t.TempDir()
	dest, err := destinationPath(project, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := nextCommandFor(dest, project, "my-stack"); got != "dcraft up -p 'my-stack'" {
		t.Fatalf("next command = %q", got)
	}
	if got := nextCommandFor(dest, project, ""); got != "dcraft up" {
		t.Fatalf("default next command = %q", got)
	}
}
