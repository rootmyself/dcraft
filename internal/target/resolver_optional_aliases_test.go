package target_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/target"
)

func writeDcraftConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A target-optional feature does not operate on a Compose project, so a
// registered environment catalog must not become a mandatory selection.
func TestOptionalTargetIgnoresRegisteredEnvironmentAliases(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	writeDcraftConfig(t, dir, `environments:
  dev:
    compose_files: [compose.yaml]
  prod:
    compose_files: [compose.yaml]
`)

	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		AllowMissingConfiguration: true,
	}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Environment != "" {
		t.Fatalf("Environment = %q, want plain defaults", got.Environment)
	}
	if len(got.Files) != 1 || got.Files[0] != base {
		t.Fatalf("Files = %v, want plain folder discovery %q", got.Files, base)
	}
	for _, w := range warnings {
		if w.Category == core.CategoryEnvironmentRequired || w.Category == core.CategoryEnvironmentInvalid {
			t.Fatalf("target-optional resolution surfaced an environment requirement: %+v", w)
		}
	}

	// The same folder must still require an explicit alias for a feature that
	// operates on the stack.
	_, _, err = target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	if err == nil {
		t.Fatal("non-optional resolution accepted a missing environment alias")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryEnvironmentRequired {
		t.Fatalf("non-optional category = %q", ce.Diagnostics[0].Category)
	}
}

// A broken project config cannot block a feature that never reads it.
func TestOptionalTargetIgnoresBrokenEnvironmentCatalog(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	writeDcraftConfig(t, dir, "environments: [broken\n")

	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		AllowMissingConfiguration: true,
	}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Environment != "" {
		t.Fatalf("Environment = %q, want plain defaults", got.Environment)
	}
	if len(got.Files) != 1 || got.Files[0] != base {
		t.Fatalf("Files = %v, want plain folder discovery %q", got.Files, base)
	}

	_, _, err = target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	if err == nil {
		t.Fatal("non-optional resolution accepted a broken environment catalog")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryEnvironmentInvalid {
		t.Fatalf("non-optional category = %q", ce.Diagnostics[0].Category)
	}
}
