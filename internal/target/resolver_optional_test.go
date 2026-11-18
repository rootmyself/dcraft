package target_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/target"
)

func TestOptionalTargetAllowsFolderWithoutComposeConfiguration(t *testing.T) {
	dir := t.TempDir()
	got, warnings, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		AllowMissingConfiguration: true,
	}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got.ProjectDir != dir {
		t.Fatalf("ProjectDir = %q, want %q", got.ProjectDir, dir)
	}
	if len(got.Files) != 0 {
		t.Fatalf("Files = %v, want empty", got.Files)
	}
}

func TestOptionalTargetStillRequiresAValidFolder(t *testing.T) {
	missing := t.TempDir() + "/nope"
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      missing,
		AllowMissingConfiguration: true,
	}, missing)
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestOptionalTargetStillValidatesExplicitFiles(t *testing.T) {
	dir := t.TempDir()
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		Files:                     []string{"missing.yaml"},
		AllowMissingConfiguration: true,
	}, dir)
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryTargetInvalid {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestDefaultTargetStillFailsWithoutComposeConfiguration(t *testing.T) {
	dir := t.TempDir()
	_, _, err := target.New().Resolve(context.Background(), core.TargetRequest{Path: dir}, dir)
	ce := failure(t, err)
	if ce.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}

func TestOptionalTargetStillDiscoversExistingComposeFile(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "compose.yaml", composeDoc("web"))
	got, _, err := target.New().Resolve(context.Background(), core.TargetRequest{
		Path:                      dir,
		AllowMissingConfiguration: true,
	}, dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Files) != 1 || got.Files[0] != base {
		t.Fatalf("Files = %v, want %q", got.Files, base)
	}
}
