package initconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	initfeature "github.com/rootmyself/dcraft/internal/features/initconfig"
	initimpl "github.com/rootmyself/dcraft/internal/initconfig"
)

type fakeResolver struct {
	target core.Target
}

func (f fakeResolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	return f.target, nil, nil
}

type fakePreflight struct{}

func (fakePreflight) Check(ctx context.Context, requires core.Requirements) core.PreflightReport {
	return core.PreflightReport{Diagnostics: []core.Diagnostic{}}
}

type fakeState struct{}

func (fakeState) StackDir(target core.Target) (string, error) { return "/state", nil }
func (fakeState) AppendOperation(target core.Target, rec core.OperationRecord) error {
	return nil
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }

type fakeValidator struct {
	service string
	calls   int
}

func (f *fakeValidator) Validate(ctx context.Context, dir string, content []byte) (initimpl.Validation, error) {
	f.calls++
	return initimpl.Validation{
		Checked:        true,
		Command:        "docker compose -f - config --format json",
		ProjectName:    "hello-world",
		DefaultNetwork: "hello-world_default",
		Services:       []string{f.service},
	}, nil
}

type harness struct {
	engine  *core.Engine
	project string
}

func newHarness(t *testing.T, service string) *harness {
	t.Helper()
	return newHarnessWithValidator(t, t.TempDir(), &fakeValidator{service: service})
}

func newHarnessWithValidator(t *testing.T, project string, validator initimpl.Validator) *harness {
	t.Helper()
	feature := initfeature.New(initimpl.New(validator, initimpl.FileSaver{}))
	reg := core.NewRegistry()
	if err := reg.Register(feature); err != nil {
		t.Fatalf("register: %v", err)
	}
	engine := core.NewEngine(reg, fakeResolver{target: core.Target{ProjectDir: project, Files: []string{}, EnvFiles: []string{}, Profiles: []string{}}}, fakePreflight{}, fakeState{}, fakeClock{})
	engine.CWD = func() (string, error) { return project, nil }
	engine.NewOperationID = func() string { return "op_init" }
	return &harness{engine: engine, project: project}
}

// creatingValidator simulates another process creating the destination after
// the engine's approval inspection and prepare have both seen it absent.
type creatingValidator struct {
	path    string
	content string
	written bool
}

func (v *creatingValidator) Validate(ctx context.Context, dir string, content []byte) (initimpl.Validation, error) {
	v.path = filepath.Join(dir, "compose.yaml")
	v.content = "services:\n  intruder:\n    image: busybox:1.36\n"
	if err := os.WriteFile(v.path, []byte(v.content), 0o644); err != nil {
		return initimpl.Validation{}, err
	}
	v.written = true
	return initimpl.Validation{
		Checked:     true,
		Command:     "docker compose -f - config --format json",
		ProjectName: "hello-world",
		Services:    []string{"web"},
	}, nil
}

func (h *harness) run(t *testing.T, req core.Request) core.Result {
	t.Helper()
	return h.engine.Run(context.Background(), req, nil)
}

func imageRequest(project string) core.Request {
	return core.Request{
		Feature: "init",
		Target:  core.TargetRequest{Path: project},
		Options: map[string]string{
			"from": "image", "service-name": "web", "image": "busybox:1.36",
			"port": "127.0.0.1:8080:80",
		},
	}
}

func TestDescriptorIsAFlatTargetOptionalWizardWithoutASecondApprovalRule(t *testing.T) {
	feature := initfeature.New(nil)
	d := feature.Descriptor()
	if d.Name != "init" || !d.Wizard || !d.TargetOptional {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.Approval != core.ApprovalNone || d.ReadOnly || d.RecordsHistory {
		t.Fatalf("descriptor safety flags = %+v", d)
	}
	if !d.Requires.Has(core.RequirementDockerCLI) || d.Requires.Has(core.RequirementEngine) || d.Requires.Has(core.RequirementCompose) {
		t.Fatalf("requirements = %v, want docker_cli only (config validation needs no engine)", d.Requires)
	}
	flags := map[string]core.Flag{}
	for _, flag := range d.Flags {
		flags[flag.Name] = flag
	}
	for _, name := range []string{"from", "service-name", "context", "dockerfile", "image", "output", "port", "volume", "env-var", "command", "restart"} {
		if _, ok := flags[name]; !ok {
			t.Fatalf("missing flag %q", name)
		}
	}
}

func TestEngineRequiresFileOverwriteForAnExistingDestination(t *testing.T) {
	h := newHarness(t, "web")
	existing := filepath.Join(h.project, "compose.yaml")
	if err := os.WriteFile(existing, []byte("services:\n  old:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(existing)

	defaultReq := imageRequest(h.project)
	defaultRes := h.run(t, defaultReq)
	if defaultRes.Outcome != core.OutcomeFailure || !hasCategory(defaultRes, core.CategoryInitInputInvalid) {
		t.Fatalf("default existing destination outcome=%q diagnostics=%+v", defaultRes.Outcome, defaultRes.Diagnostics)
	}

	req := imageRequest(h.project)
	req.Options["output"] = "compose.yaml"
	res := h.run(t, req)
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if res.RequiredApproval != core.ApprovalFileOverwrite {
		t.Fatalf("required approval = %q", res.RequiredApproval)
	}
	if !hasCategory(res, core.CategoryApprovalRequired) || res.ExitCode() != 2 {
		t.Fatalf("diagnostics = %+v exit=%d", res.Diagnostics, res.ExitCode())
	}
	after, _ := os.ReadFile(existing)
	if string(before) != string(after) {
		t.Fatal("existing file changed without approval")
	}

	req = imageRequest(h.project)
	req.Options["output"] = "compose.yaml"
	req.Approvals = []core.ApprovalKind{core.ApprovalFileOverwrite}
	res = h.run(t, req)
	if res.Outcome != core.OutcomeSuccess {
		t.Fatalf("approved outcome = %q diagnostics=%+v", res.Outcome, res.Diagnostics)
	}
	payload, ok := res.Data.(initimpl.Payload)
	if !ok {
		t.Fatalf("payload = %T", res.Data)
	}
	if !payload.Init.Overwritten || !payload.Init.Changed {
		t.Fatalf("report = %+v", payload.Init)
	}
	data, _ := os.ReadFile(existing)
	if !strings.Contains(string(data), "image: busybox:1.36") {
		t.Fatalf("file not replaced:\n%s", data)
	}
	if strings.Contains(string(data), "dcraft") {
		t.Fatalf("generated file contains a dcraft-only key:\n%s", data)
	}
}

func TestEnginePlanShowsTheRequiredOverwriteApprovalAndWritesNothing(t *testing.T) {
	h := newHarness(t, "web")
	existing := filepath.Join(h.project, "compose.yaml")
	if err := os.WriteFile(existing, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := imageRequest(h.project)
	req.Options["output"] = "compose.yaml"
	report := h.engine.Plan(context.Background(), req, nil)
	if report.Failed() {
		t.Fatalf("plan failed: %+v", report.Diagnostics)
	}
	if len(report.RequiredApprovals) != 1 || report.RequiredApprovals[0] != core.ApprovalFileOverwrite {
		t.Fatalf("required approvals = %v", report.RequiredApprovals)
	}
	if !strings.Contains(report.Text, "file_overwrite") || !strings.Contains(report.Text, "image: busybox:1.36") {
		t.Fatalf("plan text missing the approval or content:\n%s", report.Text)
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "OLD\n" {
		t.Fatal("plan wrote a file")
	}
}

func TestMissingInputFailsWithoutPromptingOrWriting(t *testing.T) {
	h := newHarness(t, "web")
	res := h.run(t, core.Request{Feature: "init", Target: core.TargetRequest{Path: h.project}})
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome=%q exit=%d diagnostics=%+v", res.Outcome, res.ExitCode(), res.Diagnostics)
	}
	if !hasCategory(res, core.CategoryInitInputInvalid) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(h.project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("a file was created from missing input")
	}
}

func TestMissingDockerfileFailsWithoutWriting(t *testing.T) {
	h := newHarness(t, "app")
	res := h.run(t, core.Request{
		Feature: "init",
		Target:  core.TargetRequest{Path: h.project},
		Options: map[string]string{"from": "dockerfile", "service-name": "app"},
	})
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome=%q exit=%d diagnostics=%+v", res.Outcome, res.ExitCode(), res.Diagnostics)
	}
	if !hasCategory(res, core.CategoryInitInputInvalid) {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(h.project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("a file was created without a Dockerfile")
	}
}

func hasCategory(res core.Result, category core.Category) bool {
	for _, d := range res.Diagnostics {
		if d.Category == category {
			return true
		}
	}
	return false
}

func TestCanceledRunUsesTheInitSpecificPartialEffectMessage(t *testing.T) {
	h := newHarness(t, "web")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := h.engine.Run(ctx, imageRequest(h.project), nil)
	if res.Outcome != core.OutcomeCanceled || res.ExitCode() != 130 {
		t.Fatalf("outcome=%q exit=%d diagnostics=%+v", res.Outcome, res.ExitCode(), res.Diagnostics)
	}
	// A cancellation after the feature starts is rendered by the engine
	// through CanceledReporter; assert the init-specific message directly.
	if d := initimpl.CanceledDiagnostic(); d.Category != core.CategoryCanceled || !strings.Contains(d.Message, "저장 단계") {
		t.Fatalf("canceled diagnostic = %+v", d)
	}
	if _, err := os.Stat(filepath.Join(h.project, "compose.yaml")); !os.IsNotExist(err) {
		t.Fatal("canceled run created a file")
	}
}

func TestRunRefusesAFileCreatedDuringValidation(t *testing.T) {
	project := t.TempDir()
	validator := &creatingValidator{}
	h := newHarnessWithValidator(t, project, validator)
	res := h.run(t, imageRequest(project))
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome=%q exit=%d diagnostics=%+v", res.Outcome, res.ExitCode(), res.Diagnostics)
	}
	if !hasCategory(res, core.CategoryInitDestinationChanged) {
		t.Fatalf("diagnostics = %+v, want init_destination_changed", res.Diagnostics)
	}
	if !validator.written {
		t.Fatal("test validator did not create the concurrent file")
	}
	data, err := os.ReadFile(validator.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != validator.content {
		t.Fatalf("concurrent file was overwritten:\n%s", data)
	}
}
