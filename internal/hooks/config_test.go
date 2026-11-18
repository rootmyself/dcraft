package hooks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/hooks"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func script(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	write(t, path, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasCategory(t *testing.T, err error, want core.Category) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	var ce *core.Error
	if !coreAs(err, &ce) {
		t.Fatalf("error is not a core error: %v", err)
	}
	for _, d := range ce.Diagnostics {
		if d.Category == want {
			return
		}
	}
	t.Fatalf("categories %+v do not include %s", ce.Diagnostics, want)
}

func coreAs(err error, target **core.Error) bool {
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

func TestLoadMissingConfigIsEmpty(t *testing.T) {
	cat, err := hooks.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 0 {
		t.Fatalf("registrations = %+v", cat.Registrations)
	}
}

func TestLoadReadsOnlyTheHooksSectionInOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".dcraft.yaml"), `# project config
environments:
  dev:
    compose_files: [compose.yaml]
hooks:
  - path: scripts/pre-deploy.sh   # before
    stage: pre
  - path: scripts/post-deploy.sh
    stage: post
secrets: []
`)
	cat, err := hooks.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 2 {
		t.Fatalf("registrations = %+v", cat.Registrations)
	}
	if cat.Registrations[0].Path != "scripts/pre-deploy.sh" || cat.Registrations[0].Stage != hooks.StagePre {
		t.Fatalf("first = %+v", cat.Registrations[0])
	}
	if cat.Registrations[1].Path != "scripts/post-deploy.sh" || cat.Registrations[1].Stage != hooks.StagePost {
		t.Fatalf("second = %+v", cat.Registrations[1])
	}
}

func TestLoadRejectsInvalidRegistrations(t *testing.T) {
	cases := map[string]string{
		"unknown stage": `hooks:
  - path: scripts/a.sh
    stage: during
`,
		"missing stage": `hooks:
  - path: scripts/a.sh
`,
		"missing path": `hooks:
  - stage: pre
`,
		"unknown field": `hooks:
  - path: scripts/a.sh
    stage: pre
    command: rm -rf /
`,
		"not a list": `hooks:
  path: scripts/a.sh
`,
		"empty path": `hooks:
  - path: "  "
    stage: pre
`,
		"duplicate": `hooks:
  - path: scripts/a.sh
    stage: pre
  - path: ./scripts/a.sh
    stage: pre
`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, ".dcraft.yaml"), content)
			_, err := hooks.Load(dir)
			hasCategory(t, err, core.CategoryHookInvalid)
		})
	}
}

func TestAddPreservesUnknownSectionsAndComments(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "scripts/pre.sh")
	write(t, filepath.Join(dir, ".dcraft.yaml"), `# keep this comment
environments:
  dev:
    # compose list
    compose_files: [compose.yaml]
custom_unknown:
  nested: true
`)
	cat, err := hooks.Add(dir, hooks.Registration{Path: "scripts/pre.sh", Stage: hooks.StagePre})
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 1 {
		t.Fatalf("catalog = %+v", cat)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# keep this comment", "# compose list", "custom_unknown", "nested: true", "hooks:", "stage: pre", "scripts/pre.sh"} {
		if !strings.Contains(text, want) {
			t.Fatalf("written config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret") || strings.Contains(text, "value:") {
		t.Fatalf("config persisted unexpected values:\n%s", text)
	}
}

func TestAddValidatesTheScriptBeforeSaving(t *testing.T) {
	dir := t.TempDir()
	if _, err := hooks.Add(dir, hooks.Registration{Path: "scripts/missing.sh", Stage: hooks.StagePre}); err == nil {
		t.Fatal("missing script was registered")
	}
	hasCategory(t, func() error {
		_, err := hooks.Add(dir, hooks.Registration{Path: "scripts/missing.sh", Stage: hooks.StagePre})
		return err
	}(), core.CategoryHookInvalid)

	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := hooks.Add(dir, hooks.Registration{Path: "adir", Stage: hooks.StagePre})
	hasCategory(t, err, core.CategoryHookInvalid)

	if _, err := os.Stat(filepath.Join(dir, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("failed add wrote config: %v", err)
	}
}

func TestAddIsIdempotentForTheSameRegistration(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "scripts/pre.sh")
	reg := hooks.Registration{Path: "scripts/pre.sh", Stage: hooks.StagePre}
	if _, err := hooks.Add(dir, reg); err != nil {
		t.Fatal(err)
	}
	cat, err := hooks.Add(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 1 {
		t.Fatalf("duplicate registration: %+v", cat.Registrations)
	}
}

func TestRemoveDeletesOnlyTheMatchingRegistration(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "scripts/pre.sh")
	script(t, dir, "scripts/post.sh")
	if _, err := hooks.Add(dir, hooks.Registration{Path: "scripts/pre.sh", Stage: hooks.StagePre}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Add(dir, hooks.Registration{Path: "scripts/post.sh", Stage: hooks.StagePost}); err != nil {
		t.Fatal(err)
	}
	cat, err := hooks.Remove(dir, "scripts/pre.sh", hooks.StagePre)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Registrations) != 1 || cat.Registrations[0].Path != "scripts/post.sh" {
		t.Fatalf("catalog = %+v", cat.Registrations)
	}
	_, err = hooks.Remove(dir, "scripts/pre.sh", hooks.StagePre)
	hasCategory(t, err, core.CategoryHookInvalid)
}

func TestResolvePathRejectsEscapingRelativePaths(t *testing.T) {
	dir := t.TempDir()
	if _, err := hooks.ResolvePath(dir, "../outside.sh"); err == nil {
		t.Fatal("relative path escaping the project was accepted")
	}
	abs := script(t, t.TempDir(), "elsewhere.sh")
	if _, err := hooks.ResolvePath(dir, abs); err != nil {
		t.Fatalf("absolute local script rejected: %v", err)
	}
}
