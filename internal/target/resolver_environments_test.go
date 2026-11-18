package target_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
	"github.com/rootmyself/dcraft/internal/state"
	"github.com/rootmyself/dcraft/internal/target"
)

func writeEnvironmentConfig(t *testing.T, dir, body string) {
	t.Helper()
	writeFile(t, dir, environments.FileName, body)
}

func categories(diags []core.Diagnostic) []core.Category {
	out := make([]core.Category, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Category)
	}
	return out
}

func hasCategory(diags []core.Diagnostic, cat core.Category) bool {
	for _, d := range diags {
		if d.Category == cat {
			return true
		}
	}
	return false
}

func environmentConfig() string {
	return `
environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
    env_files: [.env.dev]
    profiles: [dev, tools]
  prod:
    compose_files: [compose.yaml, compose.prod.yaml]
    env_files: [.env.prod]
    profiles: [prod]
`
}

func prepareEnvironmentProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))
	writeFile(t, dir, "compose.dev.yaml", composeDoc("debug"))
	writeFile(t, dir, "compose.prod.yaml", composeDoc("release"))
	writeFile(t, dir, ".env.dev", "TAG=dev\n")
	writeFile(t, dir, ".env.prod", "TAG=prod\n")
	writeEnvironmentConfig(t, dir, environmentConfig())
	return dir
}

func TestResolveRequiresExplicitEnvironmentWhenAliasesAreRegistered(t *testing.T) {
	dir := prepareEnvironmentProject(t)

	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	if err == nil {
		t.Fatalf("Resolve succeeded without an environment: target=%+v", got)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	ce := failure(t, err)
	if len(ce.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", ce.Diagnostics)
	}
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryEnvironmentRequired {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEnvironmentRequired)
	}
	if d.Severity != core.SeverityError {
		t.Fatalf("severity = %q", d.Severity)
	}
	for _, want := range []string{"dev", "prod", "선택"} {
		if !strings.Contains(d.Message, want) {
			t.Fatalf("message %q missing %q", d.Message, want)
		}
	}
	if !strings.Contains(strings.Join(d.NextActions, " "), "--env") {
		t.Fatalf("next actions should name --env: %v", d.NextActions)
	}
	// No implicit first/prod/last choice: no files and no environment id.
	if got.Environment != "" || len(got.Files) != 0 {
		t.Fatalf("target must not be partially selected: %+v", got)
	}
}

func TestResolveExpandsSelectedEnvironmentInputsInOrder(t *testing.T) {
	dir := prepareEnvironmentProject(t)
	req := core.TargetRequest{Path: dir, Environment: "dev"}

	got, warnings, err := target.New().Resolve(context.Background(), req, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(warnings) != 1 || warnings[0].Category != core.CategoryEnvironmentLimits {
		t.Fatalf("warnings = %+v, want one isolation-limits info", warnings)
	}
	if got.Environment != "dev" {
		t.Fatalf("Environment = %q", got.Environment)
	}
	wantFiles := []string{filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "compose.dev.yaml")}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("Files = %v, want %v", got.Files, wantFiles)
	}
	wantEnv := []string{filepath.Join(dir, ".env.dev")}
	if !reflect.DeepEqual(got.EnvFiles, wantEnv) {
		t.Fatalf("EnvFiles = %v, want %v", got.EnvFiles, wantEnv)
	}
	if !reflect.DeepEqual(got.Profiles, []string{"dev", "tools"}) {
		t.Fatalf("Profiles = %v", got.Profiles)
	}
	if got.DefaultEnvFile != "" {
		t.Fatalf("DefaultEnvFile = %q, explicit env files suppress .env", got.DefaultEnvFile)
	}
	wantArgs := []string{"compose",
		"-f", wantFiles[0], "-f", wantFiles[1],
		"--env-file", wantEnv[0],
		"--profile", "dev", "--profile", "tools"}
	if !reflect.DeepEqual(got.ComposeArgs(), wantArgs) {
		t.Fatalf("ComposeArgs = %v, want %v", got.ComposeArgs(), wantArgs)
	}
}

func TestResolveEnvironmentWithoutComposeFilesUsesDiscovery(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	override := writeFile(t, dir, "compose.override.yaml", composeDoc("debug"))
	env := writeFile(t, dir, ".env", "TAG=dev\n")
	writeFile(t, dir, ".env.tools", "TOOLS=1\n")
	writeEnvironmentConfig(t, dir, `
environments:
  tools:
    env_files: [.env.tools]
    profiles: [tools]
`)

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "tools"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got.Files, []string{base, override}) {
		t.Fatalf("Files = %v, want discovered base+override", got.Files)
	}
	if !reflect.DeepEqual(got.EnvFiles, []string{filepath.Join(dir, ".env.tools")}) {
		t.Fatalf("EnvFiles = %v", got.EnvFiles)
	}
	if got.DefaultEnvFile != "" {
		t.Fatalf("DefaultEnvFile = %q", got.DefaultEnvFile)
	}
	_ = env
}

func TestResolveEnvironmentWithoutEnvFilesReportsDefaultEnvFile(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	env := writeFile(t, dir, ".env", "TAG=1\n")
	writeEnvironmentConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.yaml]
    profiles: [dev]
`)
	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got.Files, []string{base}) {
		t.Fatalf("Files = %v", got.Files)
	}
	if got.DefaultEnvFile != env {
		t.Fatalf("DefaultEnvFile = %q, want %q", got.DefaultEnvFile, env)
	}
}

func TestResolveUnknownEnvironmentFails(t *testing.T) {
	dir := prepareEnvironmentProject(t)
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "staging"}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded for an unknown alias")
	}
	ce := failure(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryEnvironmentUnknown {
		t.Fatalf("category = %q", d.Category)
	}
	for _, want := range []string{"staging", "dev", "prod"} {
		if !strings.Contains(d.Message, want) {
			t.Fatalf("message %q missing %q", d.Message, want)
		}
	}
}

func TestResolveEnvironmentFlagWithoutAliasesFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err == nil {
		t.Fatal("Resolve accepted --env without registered aliases")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryEnvironmentUnknown {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestResolveRejectsExplicitInputsTogetherWithEnvironment(t *testing.T) {
	dir := prepareEnvironmentProject(t)
	requests := []core.TargetRequest{
		{Path: dir, Environment: "dev", Files: []string{"compose.yaml"}},
		{Path: dir, Environment: "dev", EnvFiles: []string{".env.dev"}},
		{Path: dir, Environment: "dev", Profiles: []string{"tools"}},
	}
	for _, req := range requests {
		_, _, err := target.New().Resolve(context.Background(), req, dir)
		if err == nil {
			t.Fatalf("Resolve accepted %+v together with --env", req)
		}
		ce := failure(t, err)
		d := ce.Diagnostics[0]
		if d.Category != core.CategoryEnvironmentConflict {
			t.Fatalf("category = %q, want %q", d.Category, core.CategoryEnvironmentConflict)
		}
		if !strings.Contains(d.Message, "실행환경") {
			t.Fatalf("message = %q", d.Message)
		}
	}
}

func TestResolveEnvironmentPathsAreRelativeToProjectFolderNotProcessCWD(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeFile(t, project, "compose.yaml", composeDoc("web"))
	writeFile(t, project, "compose.dev.yaml", composeDoc("debug"))
	writeEnvironmentConfig(t, project, `
environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
`)
	otherCWD := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(otherCWD, 0o755); err != nil {
		t.Fatal(err)
	}

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: project, Environment: "dev"}, otherCWD)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{filepath.Join(project, "compose.yaml"), filepath.Join(project, "compose.dev.yaml")}
	if !reflect.DeepEqual(got.Files, want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
}

func TestEnvironmentSelectionSeparatesStateAndHistory(t *testing.T) {
	dir := prepareEnvironmentProject(t)
	root := t.TempDir()
	t.Setenv(state.EnvStateDir, root)
	resolvedRoot, err := state.Root(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	store := state.New(resolvedRoot)

	dev, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve dev: %v", err)
	}
	prod, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "prod"}, dir)
	if err != nil {
		t.Fatalf("Resolve prod: %v", err)
	}
	devDir, err := store.StackDir(dev)
	if err != nil {
		t.Fatal(err)
	}
	prodDir, err := store.StackDir(prod)
	if err != nil {
		t.Fatal(err)
	}
	if devDir == prodDir {
		t.Fatalf("dev and prod share helper state: %q", devDir)
	}
	if !strings.HasPrefix(devDir, root) || !strings.HasPrefix(prodDir, root) {
		t.Fatalf("state dirs not under DCRAFT_STATE_DIR: %q %q", devDir, prodDir)
	}
	again, _, _ := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	againDir, _ := store.StackDir(again)
	if againDir != devDir {
		t.Fatalf("same environment changed state dir: %q vs %q", againDir, devDir)
	}
}

func TestResolveEnvironmentKeepsNoAliasBehaviorUnchanged(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	override := writeFile(t, dir, "compose.override.yaml", composeDoc("debug"))
	writeEnvironmentConfig(t, dir, "environments: {}\n")

	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if !reflect.DeepEqual(got.Files, []string{base, override}) {
		t.Fatalf("Files = %v", got.Files)
	}
	if len(got.Profiles) != 0 || len(got.EnvFiles) != 0 {
		t.Fatalf("unexpected environment inputs: %+v", got)
	}
	if got.Environment != "" {
		t.Fatalf("Environment = %q", got.Environment)
	}
}

func TestResolveWarnsWhenExplicitProjectNameIsSharedByEnvironments(t *testing.T) {
	dir := prepareEnvironmentProject(t)
	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path: dir, Environment: "dev", ProjectName: "shared-app",
	}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ProjectName != "shared-app" {
		t.Fatalf("explicit project name must be respected, got %q", got.ProjectName)
	}
	if !hasCategory(warnings, core.CategoryProjectNameShared) {
		t.Fatalf("warnings = %+v, want project_name_shared", warnings)
	}
	for _, d := range warnings {
		if d.Category != core.CategoryProjectNameShared {
			continue
		}
		if d.Severity != core.SeverityWarning {
			t.Fatalf("severity = %q", d.Severity)
		}
		for _, want := range []string{"dev", "prod", "shared-app"} {
			if !strings.Contains(d.Message+d.Detail, want) {
				t.Fatalf("diagnostic %+v missing %q", d, want)
			}
		}
		if !strings.Contains(strings.Join(d.NextActions, " "), "프로젝트 이름") {
			t.Fatalf("next actions should explain project names: %v", d.NextActions)
		}
	}
}

func TestResolveWarnsWhenComposeFilesDeclareTheSameName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", "services:\n  web:\n    image: alpine:3.20\n")
	writeFile(t, dir, "compose.dev.yaml", "name: same-stack\nservices:\n  debug:\n    image: alpine:3.20\n")
	writeFile(t, dir, "compose.prod.yaml", "name: same-stack\nservices:\n  release:\n    image: alpine:3.20\n")
	writeEnvironmentConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
  prod:
    compose_files: [compose.yaml, compose.prod.yaml]
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !hasCategory(warnings, core.CategoryProjectNameShared) {
		t.Fatalf("warnings = %+v, want project_name_shared for declared names", warnings)
	}
}

func TestResolveDoesNotWarnForDistinctDeclaredNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", "name: app-dev\nservices: {web: {image: alpine:3.20}}\n")
	writeFile(t, dir, "compose.prod.yaml", "name: app-prod\nservices: {web: {image: alpine:3.20}}\n")
	writeEnvironmentConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.dev.yaml]
  prod:
    compose_files: [compose.prod.yaml]
`)
	for _, alias := range []string{"dev", "prod"} {
		_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: alias}, dir)
		if err != nil {
			t.Fatalf("Resolve %s: %v", alias, err)
		}
		if hasCategory(warnings, core.CategoryProjectNameShared) {
			t.Fatalf("%s warnings = %+v, want no project_name_shared", alias, warnings)
		}
	}
}

func TestResolveWarnsAboutDuplicatePublishedHostPortsAcrossEnvironments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", `name: app-dev
services:
  web:
    image: alpine:3.20
    ports:
      - "8080:80"
`)
	writeFile(t, dir, "compose.prod.yaml", `name: app-prod
services:
  web:
    image: alpine:3.20
    ports:
      - "8080:80"
`)
	writeEnvironmentConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.dev.yaml]
  prod:
    compose_files: [compose.prod.yaml]
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	overlap := overlapDiagnostic(t, warnings)
	for _, want := range []string{"8080", "dev", "prod", "web"} {
		if !strings.Contains(overlap.Message+overlap.Detail, want) {
			t.Fatalf("overlap %+v missing %q", overlap, want)
		}
	}
	if !strings.Contains(strings.Join(overlap.NextActions, " "), "격리") {
		t.Fatalf("overlap next actions should be honest about isolation: %v", overlap.NextActions)
	}
}

func TestResolveDoesNotWarnForDistinctPublishedPorts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", "name: app-dev\nservices:\n  web:\n    image: alpine:3.20\n    ports: [\"8080:80\"]\n")
	writeFile(t, dir, "compose.prod.yaml", "name: app-prod\nservices:\n  web:\n    image: alpine:3.20\n    ports: [\"8081:80\"]\n")
	writeEnvironmentConfig(t, dir, `
environments:
  dev: {compose_files: [compose.dev.yaml]}
  prod: {compose_files: [compose.prod.yaml]}
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if hasCategory(warnings, core.CategoryEnvironmentOverlap) {
		t.Fatalf("warnings = %+v, want no port overlap", warnings)
	}
}

func TestResolveWarnsAboutSharedAbsoluteBindSource(t *testing.T) {
	dir := t.TempDir()
	shared := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", `name: app-dev
services:
  db:
    image: alpine:3.20
    volumes:
      - `+shared+`:/var/lib/data
`)
	writeFile(t, dir, "compose.prod.yaml", `name: app-prod
services:
  db:
    image: alpine:3.20
    volumes:
      - `+shared+`:/srv/data
`)
	writeEnvironmentConfig(t, dir, `
environments:
  dev: {compose_files: [compose.dev.yaml]}
  prod: {compose_files: [compose.prod.yaml]}
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	overlap := overlapDiagnostic(t, warnings)
	if !strings.Contains(overlap.Detail, shared) || !strings.Contains(overlap.Detail, "db") {
		t.Fatalf("overlap %+v should name the shared bind source and services", overlap)
	}
}

func TestResolveWarnsAboutSharedExternalVolumeAndNetwork(t *testing.T) {
	dir := t.TempDir()
	compose := func(service string) string {
		return `name: ` + service + `
services:
  app:
    image: alpine:3.20
    volumes: [shared-data:/data]
    networks: [shared-net]
volumes:
  shared-data:
    external: true
    name: team-shared-data
networks:
  shared-net:
    external: true
    name: team-shared-net
`
	}
	writeFile(t, dir, "compose.dev.yaml", compose("app-dev"))
	writeFile(t, dir, "compose.prod.yaml", compose("app-prod"))
	writeEnvironmentConfig(t, dir, `
environments:
  dev: {compose_files: [compose.dev.yaml]}
  prod: {compose_files: [compose.prod.yaml]}
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	overlap := overlapDiagnostic(t, warnings)
	for _, want := range []string{"team-shared-data", "team-shared-net"} {
		if !strings.Contains(overlap.Detail, want) {
			t.Fatalf("overlap %+v missing %q", overlap, want)
		}
	}
}

func TestResolveUnderstandsLegacyExternalScopeSyntax(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", `name: app-dev
services:
  app:
    image: alpine:3.20
    volumes: [shared-data:/data]
volumes:
  shared-data:
    external: true
    name: team-shared-data
`)
	writeFile(t, dir, "compose.prod.yaml", `name: app-prod
services:
  app:
    image: alpine:3.20
    volumes: [shared-data:/data]
volumes:
  shared-data:
    external:
      name: team-shared-data
`)
	writeEnvironmentConfig(t, dir, `
environments:
  dev: {compose_files: [compose.dev.yaml]}
  prod: {compose_files: [compose.prod.yaml]}
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	overlap := overlapDiagnostic(t, warnings)
	if !strings.Contains(overlap.Detail, "team-shared-data") {
		t.Fatalf("overlap %+v missing legacy external volume", overlap)
	}
}

func TestResolveIgnoresRelativeBindsAndDistinctHostIPs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.dev.yaml", `name: app-dev
services:
  web:
    image: alpine:3.20
    ports: ["127.0.0.1:8080:80"]
    volumes: ["./data:/data"]
`)
	writeFile(t, dir, "compose.prod.yaml", `name: app-prod
services:
  web:
    image: alpine:3.20
    ports: ["192.0.2.10:8080:80"]
    volumes: ["./data:/data"]
`)
	writeEnvironmentConfig(t, dir, `
environments:
  dev: {compose_files: [compose.dev.yaml]}
  prod: {compose_files: [compose.prod.yaml]}
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if hasCategory(warnings, core.CategoryEnvironmentOverlap) {
		t.Fatalf("warnings = %+v, want no overlap for distinct host IPs and relative binds", warnings)
	}
}

func TestResolveSingleEnvironmentHasNoCrossEnvironmentWarnings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", "name: solo\nservices: {web: {image: alpine:3.20, ports: [\"8080:80\"]}}\n")
	writeEnvironmentConfig(t, dir, `
environments:
  only:
    compose_files: [compose.yaml]
`)
	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "only", ProjectName: "solo"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ProjectName != "solo" {
		t.Fatalf("project name = %q", got.ProjectName)
	}
	if hasCategory(warnings, core.CategoryProjectNameShared) || hasCategory(warnings, core.CategoryEnvironmentOverlap) {
		t.Fatalf("warnings = %+v, want none for a single alias", warnings)
	}
}

func TestResolveOverlapComparesDiscoveredFilesOfOtherAlias(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", "name: app-base\nservices:\n  base:\n    image: alpine:3.20\n    ports: [\"8080:80\"]\n")
	writeFile(t, dir, "compose.dev.yaml", "name: app-dev\nservices:\n  dev:\n    image: alpine:3.20\n    ports: [\"8080:80\"]\n")
	writeEnvironmentConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.dev.yaml]
  discovered:
    profiles: [tools]
`)
	_, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir, Environment: "dev"}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	overlap := overlapDiagnostic(t, warnings)
	if !strings.Contains(overlap.Message, "discovered") || !strings.Contains(overlap.Detail, "8080") {
		t.Fatalf("overlap = %+v, want comparison against the discovered alias files", overlap)
	}
}

func overlapDiagnostic(t *testing.T, diags []core.Diagnostic) core.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.Category == core.CategoryEnvironmentOverlap {
			return d
		}
	}
	t.Fatalf("no environment_overlap warning in %+v", diags)
	return core.Diagnostic{}
}

// categories is used by later environment conflict tests in this package.
var _ = categories
