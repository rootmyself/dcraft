package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Korean user-facing text belongs in this package's messages.go. This guard
// catches a new inline literal before it reaches a snapshot, so the relocation
// cannot silently regress into a second vocabulary.
//
// It scans the production sources only and ignores comments; a Korean comment is
// documentation, not output.
func TestNoInlineKoreanOutsideMessages(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "messages.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := stripLineComment(line)
			if containsHangul(code) {
				t.Fatalf("%s:%d has an inline Korean literal; move it to messages.go:\n\t%s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// stripLineComment cuts a trailing line comment. Renderer literals never contain
// "//", so a plain cut cannot hide one.
func stripLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func containsHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}
