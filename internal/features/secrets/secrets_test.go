package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/state"
)

type fakeIdentity struct {
	cfg stack.ConfigInfo
	err error
}

func (f fakeIdentity) ConfigInfo(ctx context.Context, target core.Target) (stack.ConfigInfo, error) {
	return f.cfg, f.err
}
func (f fakeIdentity) DerivedProjectName(ctx context.Context, target core.Target) (string, error) {
	return "demo", nil
}
func (f fakeIdentity) Projects(ctx context.Context) ([]stack.ProjectInfo, error) { return nil, nil }

func testFeature(t *testing.T, services ...string) (*Feature, string, *secretref.Runtime) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: busybox\n  worker:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte("environments:\n  dev:\n    compose_files: [compose.yaml]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := secretref.NewRuntime(state.New(t.TempDir()), nil)
	return New(fakeIdentity{cfg: stack.ConfigInfo{Services: services}}, rt), dir, rt
}

func invocation(dir string, options map[string]string, services ...string) core.Invocation {
	if options == nil {
		options = map[string]string{}
	}
	return core.Invocation{
		Target:   core.Target{ProjectDir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}},
		Services: services,
		Options:  options,
	}
}

func TestAddListRemoveReferences(t *testing.T) {
	f, dir, _ := testFeature(t, "web", "worker")

	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "phase": "runtime", "delivery": "environment",
		"source": "env", "ref": "DB_PASSWORD",
	}, "web"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	report := payload.(Payload).Secrets
	if report.Action != "add" || len(report.References) != 1 {
		t.Fatalf("report = %+v", report)
	}
	ref := report.References[0]
	if ref.Service != "web" || ref.Target != "DB_PASSWORD" || ref.Effect == "" {
		t.Fatalf("reference = %+v", ref)
	}
	if !strings.Contains(ref.Effect, "환경변수") {
		t.Fatalf("effect does not explain environment delivery: %q", ref.Effect)
	}
	if len(report.Notices) == 0 || !strings.Contains(strings.Join(report.Notices, " "), "Docker 조회 권한") {
		t.Fatalf("missing environment-visibility disclosure: %v", report.Notices)
	}

	payload, err = f.Run(context.Background(), invocation(dir, nil, "web"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	list := payload.(Payload).Secrets
	if list.Action != "list" || len(list.References) != 1 || list.References[0].Target != "DB_PASSWORD" {
		t.Fatalf("list = %+v", list)
	}

	payload, err = f.Run(context.Background(), invocation(dir, map[string]string{
		"remove": "true", "delivery": "environment", "target": "DB_PASSWORD",
	}, "web"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := payload.(Payload).Secrets
	if after.Action != "remove" || len(after.References) != 0 {
		t.Fatalf("after remove = %+v", after)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "environments:") {
		t.Fatalf("environments section lost:\n%s", data)
	}
	if strings.Contains(string(data), "value:") {
		t.Fatalf("config contains a value field:\n%s", data)
	}
}

func TestAddRejectsUnknownService(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	_, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "delivery": "environment", "source": "env", "ref": "X",
	}, "nope"))
	if err == nil {
		t.Fatal("add accepted an unknown service")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryServiceUnknown {
		t.Fatalf("error = %v, want service_unknown", err)
	}
}

func TestAddRejectsMissingInputAndUnsupportedValues(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	cases := []struct {
		name string
		opts map[string]string
		want core.Category
	}{
		{"missing delivery", map[string]string{"add": "true", "source": "env", "ref": "X"}, core.CategorySecretInputInvalid},
		{"missing ref", map[string]string{"add": "true", "delivery": "environment", "source": "env"}, core.CategorySecretInputInvalid},
		{"build phase", map[string]string{"add": "true", "phase": "build", "delivery": "environment", "source": "env", "ref": "X"}, core.CategorySecretUnsupported},
		{"aws source", map[string]string{"add": "true", "delivery": "environment", "source": "aws", "ref": "X"}, core.CategorySecretUnsupported},
		{"conflicting action", map[string]string{"add": "true", "remove": "true"}, core.CategorySecretInputInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Run(context.Background(), invocation(dir, tc.opts, "web"))
			if err == nil {
				t.Fatal("Run succeeded, want error")
			}
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Diagnostics[0].Category != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWizardStepsAndPreview(t *testing.T) {
	f, dir, _ := testFeature(t, "web", "worker")
	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{"wizard": "steps"}))
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	spec, ok := payload.(core.WizardSpec)
	if !ok {
		t.Fatalf("payload = %T, want WizardSpec", payload)
	}
	names := make([]string, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		names = append(names, step.Name)
	}
	for _, want := range []string{"service", "phase", "delivery", "source", "ref"} {
		if !contains(names, want) {
			t.Fatalf("steps = %v, missing %q", names, want)
		}
	}
	if len(spec.Steps[0].Options) != 2 {
		t.Fatalf("service options = %v", spec.Steps[0].Options)
	}

	answers := map[string]string{
		"service": "web", "phase": "runtime", "delivery": "file",
		"source": "file", "ref": "secrets/token.txt",
	}
	preview, err := f.WizardPreview(answers)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"web", "/run/secrets/token", "로컬 파일", "직접 읽어야"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	if _, err := f.WizardPreview(map[string]string{"service": "web", "delivery": "environment", "source": "env", "ref": "BAD-NAME"}); err == nil {
		t.Fatal("preview accepted an unsafe reference")
	}
}

func TestWizardApplyUsesSameValidationAndStore(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"wizard": "apply", "service": "web", "phase": "runtime",
		"delivery": "environment", "source": "env", "ref": "WIZARD_SECRET",
	}))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	report := payload.(Payload).Secrets
	if len(report.References) != 1 || report.References[0].Ref != "WIZARD_SECRET" {
		t.Fatalf("report = %+v", report)
	}
	cat, err := secretref.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.References) != 1 {
		t.Fatalf("saved = %+v", cat.References)
	}
}

func TestListJSONHasNoValueField(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	if _, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"add": "true", "delivery": "file", "source": "file", "ref": "secrets/token.txt",
	}, "web")); err != nil {
		t.Fatalf("add: %v", err)
	}
	payload, err := f.Run(context.Background(), invocation(dir, nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"value"`) {
		t.Fatalf("JSON contains a value field: %s", data)
	}
	var decoded struct {
		Secrets struct {
			References []map[string]any `json:"references"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Secrets.References) != 1 {
		t.Fatalf("references = %+v", decoded.Secrets.References)
	}
	for key := range decoded.Secrets.References[0] {
		if key == "value" || strings.Contains(strings.ToLower(key), "secret_value") {
			t.Fatalf("unexpected value key %q", key)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRemoveFileReferenceCleansMaterializedFileAndOverride(t *testing.T) {
	f, dir, rt := testFeature(t, "web")
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, "secrets", name), []byte("value-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := f.Run(context.Background(), invocation(dir, map[string]string{
			"add": "true", "delivery": "file", "source": "file", "ref": "secrets/" + name,
		}, "web")); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	target := core.Target{ProjectDir: dir, Files: []string{filepath.Join(dir, "compose.yaml")}}
	session, err := rt.Prepare(context.Background(), target, nil, secretref.ModeCreate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	session.Commit()
	aPath, _ := rt.MaterializedPath(target, secretref.Reference{Service: "web", Delivery: secretref.DeliveryFile, Target: "a"})
	bPath, _ := rt.MaterializedPath(target, secretref.Reference{Service: "web", Delivery: secretref.DeliveryFile, Target: "b"})
	for _, path := range []string{aPath, bPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized %s missing: %v", path, err)
		}
	}

	payload, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"remove": "true", "delivery": "file", "target": "a",
	}, "web"))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	report := payload.(Payload).Secrets
	if len(report.References) != 1 || report.References[0].Target != "b" {
		t.Fatalf("report after remove = %+v", report)
	}
	// Removal must not break a running container's restart. The file stays
	// until a safe point: a successful up that recreates the service, or down.
	if _, err := os.Stat(aPath); err != nil {
		t.Fatalf("removal deleted a file a running container may still restart with: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("live reference lost its materialized file: %v", err)
	}
	override, _ := rt.OverridePath(target)
	if _, err := os.Stat(override); err != nil {
		t.Fatalf("override missing while a reference remains: %v", err)
	}
	overrideText := string(mustReadFile(t, override))
	if strings.Contains(overrideText, "target: a") || !strings.Contains(overrideText, "target: b") {
		t.Fatalf("override does not reflect the removed entry:\n%s", overrideText)
	}

	// A successful up for the whole stack is the safe cleanup point.
	session, err = rt.Prepare(context.Background(), target, nil, secretref.ModeCreate)
	if err != nil {
		t.Fatalf("Prepare after remove: %v", err)
	}
	session.Commit()
	if _, err := os.Stat(aPath); !os.IsNotExist(err) {
		t.Fatalf("successful up did not clean the removed file: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("successful up removed a live file: %v", err)
	}

	// Removing the last reference cleans the override as well.
	if _, err := f.Run(context.Background(), invocation(dir, map[string]string{
		"remove": "true", "delivery": "file", "target": "b",
	}, "web")); err != nil {
		t.Fatalf("remove last: %v", err)
	}
	if _, err := os.Stat(override); !os.IsNotExist(err) {
		t.Fatalf("override remains after removing the last reference: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Fatalf("removing the last reference deleted the restart file before a safe point: %v", err)
	}
	if err := rt.CleanupStack(target); err != nil {
		t.Fatalf("CleanupStack: %v", err)
	}
	if _, err := os.Stat(bPath); !os.IsNotExist(err) {
		t.Fatalf("stack teardown did not clean the last file: %v", err)
	}
}

func TestRemoveMissingInputFailsWithInputError(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	cases := []map[string]string{
		{"remove": "true"},
		{"remove": "true", "delivery": "file"},
		{"remove": "true", "target": "token"},
	}
	for _, options := range cases {
		_, err := f.Run(context.Background(), invocation(dir, options, "web"))
		if err == nil {
			t.Fatalf("remove %v succeeded, want error", options)
		}
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretInputInvalid {
			t.Fatalf("remove %v error = %v, want secret_input_invalid", options, err)
		}
	}
}

func TestAddIsIdempotentThroughFeature(t *testing.T) {
	f, dir, _ := testFeature(t, "web")
	options := map[string]string{
		"add": "true", "delivery": "environment", "source": "env", "ref": "DB_PASSWORD",
	}
	first, err := f.Run(context.Background(), invocation(dir, options, "web"))
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	second, err := f.Run(context.Background(), invocation(dir, options, "web"))
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if len(first.(Payload).Secrets.References) != 1 || len(second.(Payload).Secrets.References) != 1 {
		t.Fatalf("re-add duplicated: first=%+v second=%+v", first, second)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
