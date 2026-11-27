package environments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	envconfig "github.com/rootmyself/dcraft/internal/environments"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, dir string, target core.Target, options map[string]string) (any, error) {
	t.Helper()
	if target.ProjectDir == "" {
		target.ProjectDir = dir
	}
	return New().Run(context.Background(), core.Invocation{Target: target, Options: options})
}

func inputDiagnostic(t *testing.T, err error) core.Diagnostic {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) || len(ce.Diagnostics) == 0 {
		t.Fatalf("error %v is not a core error", err)
	}
	if ce.Diagnostics[0].Category != core.CategoryInvalidOption {
		t.Fatalf("category = %q, want %q (%+v)", ce.Diagnostics[0].Category, core.CategoryInvalidOption, ce.Diagnostics[0])
	}
	return ce.Diagnostics[0]
}

func TestAddReportsOnlyAliasConfiguration(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services:\n  web:\n    image: nginx:alpine\n")
	envFile := write(t, dir, ".env.dev", "TOP_SECRET=do-not-print-me\n")

	payload, err := run(t, dir, core.Target{
		Files:    []string{base},
		EnvFiles: []string{envFile},
		Profiles: []string{"dev"},
	}, map[string]string{"add": "true", "name": "dev"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report, ok := payload.(Payload)
	if !ok {
		t.Fatalf("payload = %T", payload)
	}
	if report.Environments.Action != "add" || len(report.Environments.Aliases) != 1 {
		t.Fatalf("report = %+v", report.Environments)
	}
	alias := report.Environments.Aliases[0]
	if alias.Name != "dev" || strings.Join(alias.Files, ",") != base || strings.Join(alias.EnvFiles, ",") != envFile || strings.Join(alias.Profiles, ",") != "dev" {
		t.Fatalf("alias = %+v", alias)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"TOP_SECRET", "do-not-print-me"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("payload leaked env-file content %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{`"name":"dev"`, `"files"`, `"env_files"`, `"profiles"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %q: %s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, envconfig.FileName)); err != nil {
		t.Fatalf("config was not written: %v", err)
	}
}

func TestListWorksWithoutWrites(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	if _, err := envconfig.Add(dir, core.Environment{Name: "dev", Files: []string{base}, Profiles: []string{"dev"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, envconfig.FileName))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := run(t, dir, core.Target{}, map[string]string{"list": "true"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Environments
	if report.Action != "list" || len(report.Aliases) != 1 || report.Aliases[0].Name != "dev" {
		t.Fatalf("report = %+v", report)
	}
	after, err := os.ReadFile(filepath.Join(dir, envconfig.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("list changed the config file")
	}
}

func TestRemoveKeepsRunningConfigurationAndOtherAliases(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	for _, name := range []string{"dev", "prod"} {
		if _, err := envconfig.Add(dir, core.Environment{Name: name, Files: []string{base}}); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := run(t, dir, core.Target{}, map[string]string{"remove": "true", "name": "dev"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	report := payload.(Payload).Environments
	if report.Action != "remove" || len(report.Aliases) != 1 || report.Aliases[0].Name != "prod" {
		t.Fatalf("report = %+v", report)
	}
	cat, err := envconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Environments) != 1 || cat.Environments[0].Name != "prod" {
		t.Fatalf("catalog = %+v", cat.Environments)
	}
	_, err = run(t, dir, core.Target{}, map[string]string{"remove": "true", "name": "missing"})
	if err == nil {
		t.Fatal("removing an unknown alias succeeded")
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryEnvironmentUnknown {
		t.Fatalf("unknown remove error = %v", err)
	}
}

func TestExplicitInputIsRequired(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	cases := []struct {
		name    string
		options map[string]string
		target  core.Target
		want    string
	}{
		{"no action", map[string]string{}, core.Target{}, "동작"},
		{"two actions", map[string]string{"add": "true", "list": "true", "name": "dev"}, core.Target{Files: []string{base}}, "하나"},
		{"missing name", map[string]string{"add": "true"}, core.Target{Files: []string{base}}, "별칭"},
		{"invalid name", map[string]string{"add": "true", "name": "Dev"}, core.Target{Files: []string{base}}, "별칭"},
		{"missing inputs", map[string]string{"add": "true", "name": "dev"}, core.Target{}, "모두 없음"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, dir, tc.target, tc.options)
			if err == nil {
				t.Fatal("Run succeeded, want input error")
			}
			d := inputDiagnostic(t, err)
			if !strings.Contains(d.Message, tc.want) {
				t.Fatalf("message %q missing %q", d.Message, tc.want)
			}
			if len(d.NextActions) == 0 {
				t.Fatal("no Korean next actions")
			}
		})
	}
}

func TestWizardCollectsAndPreviewsCreation(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	write(t, dir, "compose.dev.yaml", "services: {}\n")
	write(t, dir, "compose,extra.yaml", "services: {}\n")
	write(t, dir, ".env.dev", "TOP_SECRET=hidden\n")
	f := New()
	payload, err := f.Run(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: dir, Files: []string{base}},
		Options: map[string]string{"wizard": "steps"},
	})
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	spec := payload.(core.WizardSpec)
	for _, step := range spec.Steps {
		if !strings.Contains(step.CustomHint, "예: ") || !strings.Contains(step.CustomHint, " · ") {
			t.Errorf("%s: expected multiple formatted examples in hint, got %q", step.Name, step.CustomHint)
		}
		if step.Name != "name" && !strings.Contains(step.CustomHint, "한 줄에 하나") {
			t.Errorf("%s: list hint should explain one item per line: %q", step.Name, step.CustomHint)
		}
	}
	names := make([]string, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		names = append(names, step.Name)
	}
	if strings.Join(names, ",") != "name,files,env_files,profiles" {
		t.Fatalf("steps = %v", names)
	}
	// A successful apply issues the alias this step collects, and the step name
	// is what the TUI reads, so the declaration must name a real step.
	if spec.EnvironmentAnswer != "name" {
		t.Fatalf("environment answer step = %q, want name", spec.EnvironmentAnswer)
	}
	if !slices.Contains(names, spec.EnvironmentAnswer) {
		t.Fatalf("environment answer step %q is not a step: %v", spec.EnvironmentAnswer, names)
	}
	if spec.Steps[0].Default != "" {
		t.Fatalf("alias name has an implicit default: %q", spec.Steps[0].Default)
	}
	if !strings.Contains(spec.Steps[1].Default, "compose.yaml") {
		t.Fatalf("files default does not use discovery: %q", spec.Steps[1].Default)
	}
	for _, step := range spec.Steps[1:] {
		if !step.List {
			t.Fatalf("step %s is not a list", step.Name)
		}
	}

	preview, err := f.WizardPreview(map[string]string{
		"name":      "dev",
		"files":     "compose.yaml\ncompose,extra.yaml\ncompose.dev.yaml",
		"env_files": ".env.dev",
		"profiles":  "dev\ntools",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"environments:", "dev:", "compose.yaml", "compose,extra.yaml", "compose.dev.yaml", ".env.dev", "profiles", "공유될 수 있음"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "TOP_SECRET") || strings.Contains(preview, "hidden") {
		t.Fatalf("preview leaked env-file content:\n%s", preview)
	}
	if _, err := os.Stat(filepath.Join(dir, envconfig.FileName)); !os.IsNotExist(err) {
		t.Fatalf("preview wrote the config file: %v", err)
	}

	if _, err := f.WizardPreview(map[string]string{"name": "dev", "files": "missing.yaml"}); err == nil {
		t.Fatal("preview accepted a missing Compose file")
	}

	payload, err = f.Run(context.Background(), core.Invocation{
		Target: core.Target{ProjectDir: dir},
		Options: map[string]string{
			"wizard":    "apply",
			"name":      "dev",
			"files":     "compose.yaml",
			"env_files": ".env.dev",
			"profiles":  "dev",
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := payload.(Payload).Environments.Aliases; len(got) != 1 || got[0].Name != "dev" {
		t.Fatalf("applied aliases = %+v", got)
	}
}

func TestWizardPreservesCommasInFileNames(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose,local.yaml", "services: {}\n")
	write(t, dir, "compose.yaml", "services: {}\n")
	f := New()
	_, err := f.Run(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: dir},
		Options: map[string]string{"wizard": "steps"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text, err := f.WizardPreview(map[string]string{"name": "dev", "files": "compose,local.yaml\ncompose.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "compose,local.yaml") || strings.Index(text, "compose,local.yaml") > strings.Index(text, "compose.yaml") {
		t.Fatalf("list order or comma lost:\n%s", text)
	}
}

func TestWizardRejectsDuplicateBeforeSaving(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "compose.yaml", "services: {}\n")
	if _, err := envconfig.Add(dir, core.Environment{Name: "dev", Files: []string{base}}); err != nil {
		t.Fatal(err)
	}
	f := New()
	_, err := f.Run(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: dir},
		Options: map[string]string{"wizard": "steps"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WizardPreview(map[string]string{"name": "dev", "files": "compose.yaml"}); err == nil {
		t.Fatal("preview accepted a duplicate alias")
	}
	cat, err := envconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Environments) != 1 {
		t.Fatalf("preview changed the catalog: %+v", cat.Environments)
	}
}

func TestDescriptorIsATargetOptionalWizard(t *testing.T) {
	d := New().Descriptor()
	if d.Name != "environments" || !d.TargetOptional || !d.Wizard {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.ReadOnly || d.RecordsHistory || d.Approval != core.ApprovalNone {
		t.Fatalf("alias editing must not be a stack operation: %+v", d)
	}
	flags := map[string]bool{}
	for _, flag := range d.Flags {
		flags[flag.Name] = true
	}
	for _, want := range []string{"add", "list", "remove", "name"} {
		if !flags[want] {
			t.Fatalf("missing flag %q: %+v", want, d.Flags)
		}
	}
	for _, reserved := range []string{"file", "env-file", "profile", "env"} {
		if flags[reserved] {
			t.Fatalf("feature redefines persistent flag %q", reserved)
		}
	}
}
