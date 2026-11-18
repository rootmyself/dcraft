package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	feature "github.com/rootmyself/dcraft/internal/features/hooks"
	hooklib "github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/state"
)

func setup(t *testing.T) (string, *feature.Feature, *hooklib.Service) {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "scripts/pre.sh"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(dir, "scripts/post.sh"), "#!/bin/sh\nexit 0\n")
	svc := hooklib.New(state.New(privateStateDir(t)), os.Getenv)
	return dir, feature.New(svc), svc
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, f *feature.Feature, dir string, options map[string]string) (any, error) {
	t.Helper()
	return f.Run(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}},
		Options: options,
	})
}

func payloadOf(t *testing.T, data any) feature.Report {
	t.Helper()
	payload, ok := data.(feature.Payload)
	if !ok {
		t.Fatalf("payload = %T", data)
	}
	return payload.Hooks
}

func TestListShowsRegistrationsAndTrustStateWithoutWriting(t *testing.T) {
	dir, f, svc := setup(t)
	if _, err := hooklib.Add(dir, hooklib.Registration{Path: "scripts/pre.sh", Stage: hooklib.StagePre}); err != nil {
		t.Fatal(err)
	}
	data, err := run(t, f, dir, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	report := payloadOf(t, data)
	if len(report.Registrations) != 1 {
		t.Fatalf("registrations = %+v", report.Registrations)
	}
	view := report.Registrations[0]
	if view.Path != "scripts/pre.sh" || view.Stage != "pre" || view.Trusted || !view.Present || !view.Executable {
		t.Fatalf("view = %+v", view)
	}
	if view.SHA256 == "" {
		t.Fatal("content identity missing")
	}
	if !strings.Contains(strings.Join(report.Notices, "\n"), "승인") {
		t.Fatalf("trust notice missing: %+v", report.Notices)
	}
	if strings.Contains(strings.Join(report.Notices, "\n"), "value") {
		t.Fatalf("unexpected value notice: %+v", report.Notices)
	}

	// After the engine records approval through the shared service, the same
	// command reports the trusted identity.
	plan, err := svc.Plan(context.Background(), core.Target{ProjectDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(context.Background(), core.Target{ProjectDir: dir}, plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	data, err = run(t, f, dir, map[string]string{"list": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !payloadOf(t, data).Registrations[0].Trusted {
		t.Fatal("trusted registration still reported untrusted")
	}
}

func TestAddAndRemoveUseTheSharedRegistrationRecord(t *testing.T) {
	dir, f, _ := setup(t)
	if _, err := run(t, f, dir, map[string]string{
		"add": "true", "stage": "post", "script": "scripts/post.sh",
	}); err != nil {
		t.Fatal(err)
	}
	cat, err := hooklib.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 1 || cat.Registrations[0].Path != "scripts/post.sh" || cat.Registrations[0].Stage != hooklib.StagePost {
		t.Fatalf("registration = %+v", cat.Registrations)
	}
	if _, err := run(t, f, dir, map[string]string{
		"remove": "true", "stage": "post", "script": "scripts/post.sh",
	}); err != nil {
		t.Fatal(err)
	}
	cat, err = hooklib.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 0 {
		t.Fatalf("remove left = %+v", cat.Registrations)
	}
}

func TestMissingInputsFailWithoutWriting(t *testing.T) {
	dir, f, _ := setup(t)
	_, err := run(t, f, dir, map[string]string{"add": "true"})
	if err == nil {
		t.Fatal("add without stage/script succeeded")
	}
	var ce *core.Error
	if !asCore(err, &ce) || ce.Diagnostics[0].Category != core.CategoryHookInputInvalid {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".dcraft.yaml")); !os.IsNotExist(statErr) {
		t.Fatal("missing input wrote config")
	}
}

func asCore(err error, target **core.Error) bool {
	for err != nil {
		if ce, ok := err.(*core.Error); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestWizardStepsCollectActionStageAndScript(t *testing.T) {
	dir, f, _ := setup(t)
	data, err := run(t, f, dir, map[string]string{"wizard": "steps"})
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := data.(core.WizardSpec)
	if !ok {
		t.Fatalf("spec = %T", data)
	}
	names := []string{}
	for _, step := range spec.Steps {
		names = append(names, step.Name)
	}
	if strings.Join(names, ",") != "action,stage,script" {
		t.Fatalf("steps = %v", names)
	}
	if !spec.Steps[2].AllowCustom {
		t.Fatal("script step must allow typed input")
	}
}

func TestWizardPreviewDescribesTheEffectAndRejectsMissingFiles(t *testing.T) {
	dir, f, _ := setup(t)
	if _, err := run(t, f, dir, map[string]string{"wizard": "steps"}); err != nil {
		t.Fatal(err)
	}
	text, err := f.WizardPreview(map[string]string{"action": "add", "stage": "pre", "script": "scripts/pre.sh"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"스크립트", "사전", "내용 식별자", "실행 위치", "되돌리지 않습니다"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview missing %q:\n%s", want, text)
		}
	}
	if _, err := f.WizardPreview(map[string]string{"action": "add", "stage": "pre", "script": "scripts/nope.sh"}); err == nil {
		t.Fatal("preview accepted a missing script")
	}
	if _, err := f.WizardPreview(map[string]string{"action": "remove", "stage": "pre", "script": "scripts/pre.sh"}); err == nil {
		t.Fatal("preview accepted removing an unregistered script")
	}
}

func TestWizardApplySavesThroughTheSameRecord(t *testing.T) {
	dir, f, _ := setup(t)
	if _, err := run(t, f, dir, map[string]string{"wizard": "steps"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, f, dir, map[string]string{
		"wizard": "apply", "action": "add", "stage": "pre", "script": "scripts/pre.sh",
	}); err != nil {
		t.Fatal(err)
	}
	cat, err := hooklib.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 1 || cat.Registrations[0].Stage != hooklib.StagePre {
		t.Fatalf("registration = %+v", cat.Registrations)
	}
}
