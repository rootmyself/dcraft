package environments_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
)

func writeCompose(t *testing.T, dir, name string) string {
	t.Helper()
	return writeFile(t, dir, name, "services: {}\n")
}

func inputFailure(t *testing.T, err error) *environments.InputError {
	t.Helper()
	var ie *environments.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("error %v is not *environments.InputError", err)
	}
	return ie
}

func TestAddCreatesEnvironmentAndLoadsIt(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	override := writeCompose(t, dir, "compose.dev.yaml")
	envFile := writeFile(t, dir, ".env.dev", "TAG=dev\n")

	cat, err := environments.Add(dir, core.Environment{
		Name:     "dev",
		Files:    []string{base, override},
		EnvFiles: []string{envFile},
		Profiles: []string{"dev", "tools"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(cat.Environments) != 1 || cat.Environments[0].Name != "dev" {
		t.Fatalf("catalog = %+v", cat.Environments)
	}
	if got := cat.Environments[0].Files; strings.Join(got, ",") != strings.Join([]string{base, override}, ",") {
		t.Fatalf("Files = %v", got)
	}
	if got := cat.Environments[0].EnvFiles; strings.Join(got, ",") != envFile {
		t.Fatalf("EnvFiles = %v", got)
	}
	if got := cat.Environments[0].Profiles; strings.Join(got, ",") != "dev,tools" {
		t.Fatalf("Profiles = %v", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, environments.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"environments:", "dev:", "compose.yaml", "compose.dev.yaml", ".env.dev", "dev", "tools"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("written config missing %q:\n%s", want, data)
		}
	}
	info, err := os.Stat(filepath.Join(dir, environments.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("new config mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, ".dcraft.lock")); err != nil {
		t.Fatalf("config lock file was not created: %v", err)
	}
}

func TestAddPreservesSectionsCommentsAndMode(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	writeConfig(t, dir, `# top comment kept
secrets:
  db_password:
    source: file
    ref: .secrets/db_password
hooks:
  - path: scripts/pre.sh
    stage: pre
environments:
  # existing alias comment
  prod:
    compose_files: [compose.yaml]
unknown_future:
  keep: true
`)
	configPath := filepath.Join(dir, environments.FileName)
	if err := os.Chmod(configPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := environments.Add(dir, core.Environment{Name: "dev", Files: []string{base}, Profiles: []string{"dev"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"# top comment kept", "# existing alias comment",
		"secrets:", "db_password", ".secrets/db_password",
		"hooks:", "scripts/pre.sh", "stage: pre",
		"unknown_future:", "keep: true",
		"prod:", "dev:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("written config missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %o, want 640", info.Mode().Perm())
	}
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load after Add: %v", err)
	}
	if len(cat.Environments) != 2 {
		t.Fatalf("environments = %+v", cat.Environments)
	}
}

func TestAddRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	envFile := writeFile(t, dir, ".env.dev", "A=1\n")
	if _, err := environments.Add(dir, core.Environment{Name: "dev", Files: []string{base}}); err != nil {
		t.Fatal(err)
	}
	longName := strings.Repeat("a", 33)
	cases := []struct {
		name string
		env  core.Environment
		want string
	}{
		{"empty name", core.Environment{Files: []string{base}}, "별칭"},
		{"invalid name", core.Environment{Name: "Dev", Files: []string{base}}, "별칭"},
		{"long name", core.Environment{Name: longName, Files: []string{base}}, "별칭"},
		{"duplicate", core.Environment{Name: "dev", Files: []string{base}}, "이미"},
		{"missing compose", core.Environment{Name: "staging", Files: []string{filepath.Join(dir, "missing.yaml")}}, "찾을 수 없음"},
		{"missing env file", core.Environment{Name: "staging", EnvFiles: []string{filepath.Join(dir, "missing.env")}}, "찾을 수 없음"},
		{"compose is directory", core.Environment{Name: "staging", Files: []string{dir}}, "일반 파일"},
		{"empty profile", core.Environment{Name: "staging", Profiles: []string{""}}, "빈 값"},
		{"space profile", core.Environment{Name: "staging", Profiles: []string{"dev tools"}}, "공백"},
		{"duplicate profile", core.Environment{Name: "staging", Profiles: []string{"dev", "dev"}}, "중복"},
		{"duplicate file", core.Environment{Name: "staging", Files: []string{base, "./compose.yaml"}}, "중복"},
		{"no inputs", core.Environment{Name: "staging"}, "모두 없음"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := environments.Add(dir, tc.env)
			if err == nil {
				t.Fatalf("Add(%+v) succeeded", tc.env)
			}
			var ce *core.Error
			if errors.As(err, &ce) {
				t.Fatalf("input error was a core operation error: %v", err)
			}
			ie := inputFailure(t, err)
			if !strings.Contains(ie.Error(), tc.want) {
				t.Fatalf("error %q missing %q", ie.Error(), tc.want)
			}
		})
	}
	if _, err := environments.Add(dir, core.Environment{Name: "ok", EnvFiles: []string{envFile}, Profiles: []string{"tools"}}); err != nil {
		t.Fatalf("env-file-only alias: %v", err)
	}
}

func TestAddConvertsPathsInsideProjectToRelative(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	if _, err := environments.Add(dir, core.Environment{Name: "dev", Files: []string{base}, Profiles: []string{"dev"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, environments.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), dir) {
		t.Fatalf("config stored the absolute project path:\n%s", data)
	}
}

func TestRemoveDeletesOnlyTheAliasAndKeepsOtherSections(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	writeConfig(t, dir, `# keep me
secrets: {db: {source: file, ref: .secrets/db}}
environments:
  dev:
    compose_files: [compose.yaml]
  prod:
    compose_files: [compose.yaml]
hooks: []
`)
	cat, err := environments.Remove(dir, "dev")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(cat.Environments) != 1 || cat.Environments[0].Name != "prod" {
		t.Fatalf("catalog = %+v", cat.Environments)
	}
	data, err := os.ReadFile(filepath.Join(dir, environments.FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# keep me", "secrets:", "hooks:", "prod:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("written config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "dev:") {
		t.Fatalf("removed alias remains:\n%s", text)
	}
	if _, err := environments.Remove(dir, "missing"); err == nil {
		t.Fatal("removing an unknown alias succeeded")
	} else {
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryEnvironmentUnknown {
			t.Fatalf("unknown remove error = %v", err)
		}
	}
	_ = base
}

func TestFailedWriteLeavesPreviousFileIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced for root")
	}
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	writeConfig(t, dir, "environments:\n  dev:\n    compose_files: [compose.yaml]\n")
	configPath := filepath.Join(dir, environments.FileName)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err = environments.Add(dir, core.Environment{Name: "prod", Files: []string{base}})
	if err == nil {
		t.Fatal("Add succeeded in a read-only directory")
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(before) != string(after) {
		t.Fatalf("failed write changed the old config:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("interrupted write left a temp file: %s", entry.Name())
		}
	}
}

func TestConcurrentAddsAllSurvive(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	writeConfig(t, dir, "# seeded\nsecrets:\n  db:\n    source: file\n    ref: .secrets/db\n")
	const count = 8
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("env-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := environments.Add(dir, core.Environment{
				Name:     name,
				Files:    []string{base},
				Profiles: []string{name},
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent add: %v", err)
		}
	}
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Environments) != count {
		t.Fatalf("environments = %d, want %d (lost update): %+v", len(cat.Environments), count, cat.Environments)
	}
	seen := map[string]bool{}
	for _, env := range cat.Environments {
		seen[env.Name] = true
	}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("env-%d", i)
		if !seen[name] {
			t.Fatalf("alias %q was lost: %+v", name, cat.Names())
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, environments.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# seeded") || !strings.Contains(string(data), "secrets:") {
		t.Fatalf("concurrent writes dropped preserved sections:\n%s", data)
	}
}

func TestRenderAddPreviewsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	base := writeCompose(t, dir, "compose.yaml")
	envFile := writeFile(t, dir, ".env.dev", "SECRET_VALUE=do-not-print\n")
	text, err := environments.RenderAdd(dir, core.Environment{
		Name: "dev", Files: []string{base}, EnvFiles: []string{envFile}, Profiles: []string{"dev"},
	})
	if err != nil {
		t.Fatalf("RenderAdd: %v", err)
	}
	for _, want := range []string{"environments:", "dev:", "compose.yaml", ".env.dev", "dev"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET_VALUE") || strings.Contains(text, "do-not-print") {
		t.Fatalf("preview read env file values:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, environments.FileName)); !os.IsNotExist(err) {
		t.Fatalf("RenderAdd created the config file: %v", err)
	}
}
