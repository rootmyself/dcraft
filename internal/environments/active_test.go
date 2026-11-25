package environments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// writeConfig writes a project config and returns the project folder.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A recorded alias is read back with the list, so the startup screen can start
// on it without a second read.
func TestActiveEnvironmentIsReadWithTheCatalog(t *testing.T) {
	dir := writeConfig(t, `active_environment: dev
environments:
  dev:
    compose_files: [compose.yaml]
  prod:
    compose_files: [compose.yaml]
`)
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "dev" || cat.Active.Invalid {
		t.Fatalf("active = %+v, want dev", cat.Active)
	}
}

// A value the project does not register is reported as invalid instead of
// failing the read: the reader is told and chooses again.
func TestUnknownActiveEnvironmentIsInvalidNotAnError(t *testing.T) {
	dir := writeConfig(t, `active_environment: gone
environments:
  dev:
    compose_files: [compose.yaml]
`)
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "gone" || !cat.Active.Invalid {
		t.Fatalf("active = %+v, want gone marked invalid", cat.Active)
	}
}

// A project with no aliases at all can still carry the key; it is invalid.
func TestActiveEnvironmentWithoutAliasesIsInvalid(t *testing.T) {
	dir := writeConfig(t, "active_environment: dev\n")
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "dev" || !cat.Active.Invalid {
		t.Fatalf("active = %+v, want dev marked invalid", cat.Active)
	}
}

// An absent or empty value is no record at all.
func TestMissingOrEmptyActiveEnvironmentIsNoRecord(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `environments:
  dev:
    compose_files: [compose.yaml]
`,
		"empty": `active_environment: ""
environments:
  dev:
    compose_files: [compose.yaml]
`,
		"null": `active_environment:
environments:
  dev:
    compose_files: [compose.yaml]
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeConfig(t, body)
			cat, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cat.Active.Name != "" {
				t.Fatalf("active = %+v, want no record", cat.Active)
			}
		})
	}
}

// Recording an alias edits only that key: the aliases, comments and unknown keys
// survive, because the write goes through the shared project-config path.
func TestSetActivePreservesTheRestOfTheFile(t *testing.T) {
	dir := writeConfig(t, `# project config
unknown_key: keep-me
environments:
  dev:
    compose_files: [compose.yaml]
`)
	if err := SetActive(dir, "dev"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# project config", "unknown_key: keep-me", "active_environment: dev", "compose_files"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("config lost %q:\n%s", want, data)
		}
	}
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "dev" || cat.Active.Invalid {
		t.Fatalf("active = %+v, want dev", cat.Active)
	}
}

// Clearing removes the key instead of leaving an empty value, so a project that
// never recorded an alias and one that cleared it look the same.
func TestSetActiveEmptyRemovesTheKey(t *testing.T) {
	dir := writeConfig(t, `active_environment: dev
environments:
  dev:
    compose_files: [compose.yaml]
`)
	if err := SetActive(dir, ""); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ActiveEnvironmentKey) {
		t.Fatalf("clearing left the key:\n%s", data)
	}
	if !strings.Contains(string(data), "environments:") {
		t.Fatalf("clearing removed the aliases:\n%s", data)
	}
}

// Clearing twice is not an error: a value that is already absent converges.
func TestSetActiveClearIsIdempotent(t *testing.T) {
	dir := writeConfig(t, `environments:
  dev:
    compose_files: [compose.yaml]
`)
	for i := 0; i < 2; i++ {
		if err := SetActive(dir, ""); err != nil {
			t.Fatalf("clear %d: %v", i, err)
		}
	}
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "" {
		t.Fatalf("active = %+v, want no record", cat.Active)
	}
}

// Recording replaces an existing value in place, so switching twice does not
// append a second key.
func TestSetActiveReplacesTheExistingValue(t *testing.T) {
	dir := writeConfig(t, `active_environment: dev
environments:
  dev:
    compose_files: [compose.yaml]
  prod:
    compose_files: [compose.yaml]
`)
	if err := SetActive(dir, "prod"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), ActiveEnvironmentKey); got != 1 {
		t.Fatalf("config has %d active keys:\n%s", got, data)
	}
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "prod" {
		t.Fatalf("active = %+v, want prod", cat.Active)
	}
}

// Removing an alias does not touch the record: the caller clears it when the
// removed alias was the active one, which keeps the file's meaning explicit.
func TestRemoveLeavesTheActiveKeyForTheCaller(t *testing.T) {
	dir := writeConfig(t, `active_environment: dev
environments:
  dev:
    compose_files: [compose.yaml]
`)
	if _, err := Remove(dir, "dev"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "dev" || !cat.Active.Invalid {
		t.Fatalf("active = %+v, want the stale value reported as invalid", cat.Active)
	}
	if err := SetActive(dir, ""); err != nil {
		t.Fatalf("SetActive clear: %v", err)
	}
	cat, err = Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cat.Active.Name != "" {
		t.Fatalf("active = %+v, want no record", cat.Active)
	}
}

// The Catalog carries the record; a caller that only reads aliases is unaffected.
func TestCatalogStillExposesAliases(t *testing.T) {
	dir := writeConfig(t, `active_environment: dev
environments:
  dev:
    compose_files: [compose.yaml]
`)
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cat.Find("dev"); !ok {
		t.Fatal("dev alias is missing")
	}
	if len(cat.Names()) != 1 {
		t.Fatalf("names = %v", cat.Names())
	}
	var _ core.EnvironmentActive = cat.Active
}
