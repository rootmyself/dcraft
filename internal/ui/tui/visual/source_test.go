package visual

import (
	"os"
	"path/filepath"
	"testing"
)

// readSource loads one package source file. The test lives in the package so it
// can read its own directory without an embed.
func readSource(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(".", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
