package secretref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// RedactText is the shared redaction entry point for tool output that is not
// part of a prepared session (for example backup diagnostics). It reads only
// referenced sources, never persists a value, and skips unavailable sources.
func TestRedactTextRemovesReferencedEnvironmentAndFileValues(t *testing.T) {
	dir := t.TempDir()
	fileValue := "file-sentinel-value-123"
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte(fileValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: DCRAFT_TEST_SECRET
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: token.txt
`
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(nil, func(name string) string {
		if name == "DCRAFT_TEST_SECRET" {
			return "env-sentinel-value-456"
		}
		return ""
	})
	target := core.Target{ProjectDir: dir}
	text := "failed env-sentinel-value-456 and file-sentinel-value-123 and file-sentinel-value-123\n"
	got := rt.RedactText(target, text)
	for _, forbidden := range []string{"env-sentinel-value-456", "file-sentinel-value-123"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redaction left %q in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, msgRedacted) {
		t.Fatalf("redaction did not mark the value: %q", got)
	}
}

func TestRedactTextSkipsMissingSourcesWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	cfg := `secrets:
  - service: web
    phase: runtime
    delivery: file
    source: file
    ref: missing.txt
`
	if err := os.WriteFile(filepath.Join(dir, ".dcraft.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(nil, func(string) string { return "" })
	text := "plain diagnostic"
	if got := rt.RedactText(core.Target{ProjectDir: dir}, text); got != text {
		t.Fatalf("missing source changed text: %q", got)
	}
}

func TestRedactTextWithNoCatalogIsIdentity(t *testing.T) {
	rt := NewRuntime(nil, func(string) string { return "secret" })
	if got := rt.RedactText(core.Target{ProjectDir: t.TempDir()}, "kept"); got != "kept" {
		t.Fatalf("got %q", got)
	}
}
