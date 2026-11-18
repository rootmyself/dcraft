package data_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

func TestHelperTokenIsStablePerStackAndSeparatesStacks(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "a-link")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tokenA := data.HelperToken(core.Target{ProjectDir: a})
	if tokenA == "" || len(tokenA) != 12 {
		t.Fatalf("token = %q, want 12 hex characters", tokenA)
	}
	if tokenA != data.HelperToken(core.Target{ProjectDir: link}) {
		t.Fatal("symlinked spelling produced a different helper token")
	}
	if tokenA != data.HelperToken(core.Target{ProjectDir: a + string(filepath.Separator) + "."}) {
		t.Fatal("dot spelling produced a different helper token")
	}
	if tokenA == data.HelperToken(core.Target{ProjectDir: b}) {
		t.Fatal("different folders share a helper token")
	}
	if tokenA == data.HelperToken(core.Target{ProjectDir: a, Environment: "prod"}) {
		t.Fatal("environments share a helper token")
	}
}
