package environments_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	return writeFile(t, dir, environments.FileName, body)
}

func failure(t *testing.T, err error) *core.Error {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	if len(ce.Diagnostics) == 0 {
		t.Fatalf("error %v has no diagnostics", err)
	}
	return ce
}

func TestLoadMissingConfigMeansNoAliases(t *testing.T) {
	dir := t.TempDir()
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Environments) != 0 {
		t.Fatalf("environments = %+v, want none", cat.Environments)
	}
	if cat.Path != filepath.Join(dir, environments.FileName) {
		t.Fatalf("Path = %q", cat.Path)
	}
}

func TestLoadPreservesAliasOrderAndResolvesPathsAgainstProjectFolder(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
environments:
  prod:
    compose_files:
      - compose.yaml
      - compose.prod.yaml
    env_files:
      - .env.prod
    profiles:
      - prod
  dev:
    compose_files:
      - compose.yaml
      - compose.dev.yaml
    env_files:
      - .env.dev
    profiles:
      - dev
      - tools
`)
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Environments) != 2 {
		t.Fatalf("environments = %+v", cat.Environments)
	}
	prod, dev := cat.Environments[0], cat.Environments[1]
	if prod.Name != "prod" || dev.Name != "dev" {
		t.Fatalf("order = %q, %q; want file order prod, dev", prod.Name, dev.Name)
	}
	wantFiles := []string{filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "compose.prod.yaml")}
	if strings.Join(prod.Files, ",") != strings.Join(wantFiles, ",") {
		t.Fatalf("prod files = %v, want %v", prod.Files, wantFiles)
	}
	if strings.Join(prod.EnvFiles, ",") != filepath.Join(dir, ".env.prod") {
		t.Fatalf("prod env files = %v", prod.EnvFiles)
	}
	if strings.Join(prod.Profiles, ",") != "prod" {
		t.Fatalf("prod profiles = %v", prod.Profiles)
	}
	if strings.Join(dev.Profiles, ",") != "dev,tools" {
		t.Fatalf("dev profiles = %v", dev.Profiles)
	}
}

func TestLoadEmptyConfigMeansNoAliases(t *testing.T) {
	for _, body := range []string{"", "environments:\n", "environments: {}\n", "# only a comment\n"} {
		dir := t.TempDir()
		writeConfig(t, dir, body)
		cat, err := environments.Load(dir)
		if err != nil {
			t.Fatalf("Load(%q): %v", body, err)
		}
		if len(cat.Environments) != 0 {
			t.Fatalf("Load(%q) environments = %+v, want none", body, cat.Environments)
		}
	}
}

func TestLoadAllowsEnvironmentWithoutComposeFiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
environments:
  only-env:
    env_files:
      - .env.only
    profiles:
      - tools
`)
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Environments) != 1 || len(cat.Environments[0].Files) != 0 {
		t.Fatalf("environments = %+v, want empty Compose file list", cat.Environments)
	}
}

func TestExpandChecksReferencedFilesExist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", "services: {}\n")
	writeFile(t, dir, "compose.dev.yaml", "services: {}\n")
	writeConfig(t, dir, `
environments:
  dev:
    compose_files: [compose.yaml, compose.dev.yaml]
  broken:
    compose_files: [compose.yaml, missing.yaml]
`)
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := cat.Expand("dev"); err != nil {
		t.Fatalf("Expand dev: %v", err)
	}
	_, err = cat.Expand("broken")
	if err == nil {
		t.Fatal("Expand broken succeeded")
	}
	ce := failure(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryEnvironmentInvalid {
		t.Fatalf("category = %q, want %q", d.Category, core.CategoryEnvironmentInvalid)
	}
	if !strings.Contains(d.Message, "broken") || !strings.Contains(d.Detail, "missing.yaml") {
		t.Fatalf("diagnostic should name the alias and missing file: %+v", d)
	}
}

func TestLoadRejectsInvalidConfigs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"malformed yaml", "environments: [\n", "형식"},
		{"duplicate alias", "environments:\n  dev: {profiles: [dev]}\n  dev: {profiles: [prod]}\n", "중복"},
		{"unknown field", "environments:\n  dev:\n    compose_file: compose.yaml\n", "알 수 없는 항목"},
		{"empty alias", "environments:\n  dev: {}\n", "모두 없음"},
		{"null alias", "environments:\n  dev:\n", "모두 없음"},
		{"alias not mapping", "environments:\n  dev: compose.yaml\n", "항목 묶음"},
		{"compose_files not list", "environments:\n  dev:\n    compose_files: compose.yaml\n", "목록"},
		{"list item not string", "environments:\n  dev:\n    compose_files: [compose.yaml, {x: 1}]\n", "문자열"},
		{"list item number", "environments:\n  dev:\n    compose_files: [123]\n", "문자열"},
		{"numeric alias", "environments:\n  123: {profiles: [dev]}\n", "별칭"},
		{"empty list item", "environments:\n  dev:\n    compose_files: [\"\"]\n", "빈 값"},
		{"duplicate file", "environments:\n  dev:\n    compose_files: [compose.yaml, ./compose.yaml]\n", "중복"},
		{"duplicate profile", "environments:\n  dev:\n    profiles: [dev, dev]\n", "중복"},
		{"duplicate field", "environments:\n  dev:\n    profiles: [dev]\n    profiles: [prod]\n", "중복"},
		{"invalid alias name", "environments:\n  Dev: {profiles: [dev]}\n", "별칭"},
		{"environments not mapping", "environments: [dev]\n", "별칭 묶음"},
		{"root not mapping", "- dev\n", "최상위"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.body)
			_, err := environments.Load(dir)
			if err == nil {
				t.Fatalf("Load(%q) succeeded, want error", tc.body)
			}
			ce := failure(t, err)
			d := ce.Diagnostics[0]
			if d.Category != core.CategoryEnvironmentInvalid {
				t.Fatalf("category = %q, want %q", d.Category, core.CategoryEnvironmentInvalid)
			}
			if !strings.Contains(d.Message, tc.want) {
				t.Fatalf("message %q should contain %q", d.Message, tc.want)
			}
			if len(d.NextActions) == 0 {
				t.Fatal("no Korean next actions")
			}
		})
	}
}

func TestLoadIgnoresUnknownTopLevelKeysForFutureTickets(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
# other sections are owned by later tickets; this ticket only reads environments
environments:
  dev:
    profiles: [dev]
future_section:
  whatever: true
`)
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Environments) != 1 || cat.Environments[0].Name != "dev" {
		t.Fatalf("environments = %+v", cat.Environments)
	}
}

func TestExpandUnknownAliasListsAvailableEnvironments(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
environments:
  dev: {profiles: [dev]}
  prod: {profiles: [prod]}
`)
	cat, err := environments.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = cat.Expand("staging")
	if err == nil {
		t.Fatal("Expand staging succeeded")
	}
	ce := failure(t, err)
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryEnvironmentUnknown {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "dev") || !strings.Contains(d.Message, "prod") {
		t.Fatalf("message should list aliases: %+v", d)
	}
}
