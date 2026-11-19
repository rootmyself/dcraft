package target_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/target"
)

// A target-optional feature ignores aliases, but asking it to combine an
// explicit alias selection with explicit Compose inputs would create two
// competing answers. It is rejected as usage input, never resolved silently.
func TestOptionalTargetRejectsEnvironmentWithExplicitComposeInputs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "compose.yaml", composeDoc("web"))

	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		Environment:               "dev",
		Files:                     []string{"compose.yaml"},
		AllowMissingConfiguration: true,
	}, dir)
	if err == nil {
		t.Fatal("optional target accepted --env with -f")
	}
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryEnvironmentConflict {
		t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, core.CategoryEnvironmentConflict)
	}

	// Without an explicit alias the same inputs stay valid.
	if _, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		Files:                     []string{"compose.yaml"},
		AllowMissingConfiguration: true,
	}, dir); err != nil {
		t.Fatalf("optional target without --env: %v", err)
	}
}
