package target_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/target"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func composeDoc(service string) string {
	return "services:\n  " + service + ":\n    image: alpine:3.20\n"
}

func diagnosticCategories(diags []core.Diagnostic) []core.Category {
	cats := make([]core.Category, 0, len(diags))
	for _, d := range diags {
		cats = append(cats, d.Category)
	}
	return cats
}

func failure(t *testing.T, err error) *core.Error {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	return ce
}

func TestResolveDefaultComposeFileWithOverride(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	override := writeFile(t, dir, "compose.override.yaml", composeDoc("debug"))

	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	wantFiles := []string{base, override}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("Files = %v, want %v", got.Files, wantFiles)
	}
	if got.ProjectDir != dir {
		t.Fatalf("ProjectDir = %q, want %q", got.ProjectDir, dir)
	}
	if got.Environment != "" {
		t.Fatalf("Environment = %q, want empty", got.Environment)
	}
}

func TestResolveDockerComposeNamesAndMatchingOverride(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "docker-compose.yml", composeDoc("web"))
	override := writeFile(t, dir, "docker-compose.override.yml", composeDoc("debug"))
	writeFile(t, dir, "compose.override.yaml", composeDoc("must-not-be-used"))

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{base, override}
	if !reflect.DeepEqual(got.Files, want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
}

func TestResolveMultipleBaseFilesPrefersComposeYamlAndWarns(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("preferred"))
	writeFile(t, dir, "compose.yml", composeDoc("second"))
	writeFile(t, dir, "docker-compose.yml", composeDoc("third"))

	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got.Files, []string{base}) {
		t.Fatalf("Files = %v, want only %q", got.Files, base)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one warning", warnings)
	}
	w := warnings[0]
	if w.Category != core.CategoryMultipleConfigFiles {
		t.Fatalf("warning category = %q, want %q", w.Category, core.CategoryMultipleConfigFiles)
	}
	if w.Severity != core.SeverityWarning {
		t.Fatalf("warning severity = %q, want warning", w.Severity)
	}
	if !strings.Contains(w.Message, "여러") {
		t.Fatalf("warning message %q should explain multiple files in Korean", w.Message)
	}
	if !strings.Contains(w.Detail, filepath.Join(dir, "compose.yml")) {
		t.Fatalf("warning detail %q should name the ignored file", w.Detail)
	}
}

func TestResolveNeverWalksUpToParent(t *testing.T) {
	parent := t.TempDir()
	writeFile(t, parent, "compose.yaml", composeDoc("parent"))
	child := filepath.Join(parent, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{}, child)
	if err == nil {
		t.Fatalf("Resolve succeeded with files %v, want no_configuration", got.Files)
	}
	ce := failure(t, err)
	if len(ce.Diagnostics) == 0 || ce.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("diagnostics = %v, want no_configuration", ce.Diagnostics)
	}
	if got.ProjectDir != child {
		t.Fatalf("ProjectDir = %q, want %q", got.ProjectDir, child)
	}
	if len(ce.Diagnostics[0].NextActions) == 0 {
		t.Fatalf("no next actions provided")
	}
}

func TestResolveNoConfigurationExplainsNextActions(t *testing.T) {
	dir := t.TempDir()
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want error")
	}
	ce := failure(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryNoConfiguration {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryNoConfiguration)
	}
	if d.Severity != core.SeverityError {
		t.Fatalf("severity = %q, want error", d.Severity)
	}
	joined := d.Message + " " + strings.Join(d.NextActions, " ")
	for _, want := range []string{"Compose", "compose.yaml", "--path"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostic %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "상위") == false {
		t.Fatalf("diagnostic should state that parent folders are not searched: %q", joined)
	}
}

func TestResolveExplicitInputsPreserveOrderAndRelativeBase(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeFile(t, project, "compose.yaml", composeDoc("ignored-default"))
	first := writeFile(t, project, "first.yaml", composeDoc("web"))
	second := writeFile(t, project, "second.yaml", composeDoc("debug"))
	env := writeFile(t, project, "custom.env", "TAG=1\n")

	req := core.TargetRequest{
		Path:        project,
		Files:       []string{"first.yaml", "second.yaml"},
		EnvFiles:    []string{"custom.env"},
		Profiles:    []string{"dev", "tools"},
		ProjectName: "hello-world",
		Environment: "staging",
	}
	// Relative explicit paths follow the process working directory, like Compose.
	got, warnings, err := target.New().Resolve(context.Background(), req, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if want := []string{first, second}; !reflect.DeepEqual(got.Files, want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
	if want := []string{env}; !reflect.DeepEqual(got.EnvFiles, want) {
		t.Fatalf("EnvFiles = %v, want %v", got.EnvFiles, want)
	}
	if want := []string{"dev", "tools"}; !reflect.DeepEqual(got.Profiles, want) {
		t.Fatalf("Profiles = %v, want %v", got.Profiles, want)
	}
	if got.ProjectName != "hello-world" {
		t.Fatalf("ProjectName = %q, want hello-world", got.ProjectName)
	}
	if got.Environment != "staging" {
		t.Fatalf("Environment = %q, want staging", got.Environment)
	}
}

func TestResolveExplicitFilesDoNotAddDefaultOverride(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	writeFile(t, dir, "compose.override.yaml", composeDoc("debug"))

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Files: []string{base}}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got.Files, []string{base}) {
		t.Fatalf("Files = %v, want only %q", got.Files, base)
	}
}

func TestResolveMissingExplicitFileIsTargetInvalid(t *testing.T) {
	dir := t.TempDir()
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:  dir,
		Files: []string{"nope.yaml"},
	}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want error")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, core.CategoryTargetInvalid)
	}
	if !strings.Contains(strings.Join(ce.Diagnostics[0].NextActions, " "), "-f") {
		t.Fatalf("next actions should mention -f: %v", ce.Diagnostics[0].NextActions)
	}
}

func TestResolveMissingPathIsTargetInvalid(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: missing}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want error")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, core.CategoryTargetInvalid)
	}
}

func TestResolvePathThatIsAFileIsTargetInvalid(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: file}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want error")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, core.CategoryTargetInvalid)
	}
}

func TestResolveRelativePathUsesWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeFile(t, project, "compose.yaml", composeDoc("web"))

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: "project"}, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ProjectDir != project {
		t.Fatalf("ProjectDir = %q, want %q", got.ProjectDir, project)
	}
}

func TestResolveExplicitDirectoryCannotSelectComposeFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Files: []string{dir}}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want error")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, core.CategoryTargetInvalid)
	}
}

func TestResolveReportsDefaultEnvFileOnlyWhenUsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))
	envPath := writeFile(t, dir, ".env", "TAG=3.20\n")
	explicit := writeFile(t, dir, "custom.env", "TAG=3.19\n")

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DefaultEnvFile != envPath {
		t.Fatalf("DefaultEnvFile = %q, want %q", got.DefaultEnvFile, envPath)
	}
	if len(got.EnvFiles) != 0 {
		t.Fatalf("EnvFiles = %v, default .env must not be passed explicitly", got.EnvFiles)
	}

	got, _, err = target.New().Resolve(context.Background(), core.TargetRequest{EnvFiles: []string{"custom.env"}}, dir)
	if err != nil {
		t.Fatalf("Resolve explicit: %v", err)
	}
	if got.DefaultEnvFile != "" {
		t.Fatalf("DefaultEnvFile = %q, explicit env files suppress .env", got.DefaultEnvFile)
	}
	if len(got.EnvFiles) != 1 || got.EnvFiles[0] != explicit {
		t.Fatalf("EnvFiles = %v, want %q", got.EnvFiles, explicit)
	}
}

func TestResolveNoDefaultEnvFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))
	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DefaultEnvFile != "" {
		t.Fatalf("DefaultEnvFile = %q, want empty", got.DefaultEnvFile)
	}
}

func TestDiagnosticCategoriesAreStable(t *testing.T) {
	// Machine-readable values are part of the published contract.
	if core.CategoryNoConfiguration != "no_configuration" ||
		core.CategoryInvalidConfiguration != "invalid_configuration" ||
		core.CategoryTargetInvalid != "target_invalid" ||
		core.CategoryMultipleConfigFiles != "multiple_configuration_files" {
		t.Fatalf("diagnostic category values changed: %q %q %q %q",
			core.CategoryNoConfiguration, core.CategoryInvalidConfiguration,
			core.CategoryTargetInvalid, core.CategoryMultipleConfigFiles)
	}
}
