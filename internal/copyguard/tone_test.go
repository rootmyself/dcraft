package copyguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestProductCopyTone guards the tone rule in docs/copy-tone.md: product copy
// ends in a fragment, never in a formal sentence ending. It reads production Go
// sources only, so tests and comments are out of scope.
func TestProductCopyTone(t *testing.T) {
	root := repoRoot(t)
	violations := scanRoot(t, root)
	if len(violations) == 0 {
		return
	}
	for _, v := range violations {
		t.Errorf("%s:%d formal sentence ending in product copy: %s", v.file, v.line, v.text)
	}
	t.Fatalf("%d product copy strings still use a formal ending; see docs/copy-tone.md", len(violations))
}

// formalEnding matches the endings the guide forbids. "세요" covers ~하세요,
// ~으세요, and ~세요; "십시오" covers the honorific imperative; the question
// endings keep a new prompt from becoming a sentence again.
var formalEnding = regexp.MustCompile(`(니다|세요|십시오|까요|나요)`)

// toneAllowlist names strings that may keep a formal ending because they quote
// another tool's output or show an exact command/example. Keep each entry next
// to the file it excuses and state why.
var toneAllowlist = map[string][]string{}

type violation struct {
	file string
	line int
	text string
}

func scanRoot(t *testing.T, root string) []violation {
	t.Helper()
	var out []violation
	for _, dir := range []string{"internal", "cmd"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, scanFile(t, path, filepath.ToSlash(rel))...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	return out
}

func scanFile(t *testing.T, path, rel string) []violation {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var out []violation
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if allowed(rel, text) {
			return true
		}
		isScript := strings.Contains(text, "#!/usr/bin/env bash")
		start := fset.Position(lit.Pos()).Line
		for i, line := range strings.Split(text, "\n") {
			// A generated script's header comments document the artifact the way
			// a README does; only its printed output follows the tone rule.
			if isScript && strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if formalEnding.MatchString(line) {
				out = append(out, violation{file: rel, line: start + i, text: strings.TrimSpace(line)})
			}
		}
		return true
	})
	return out
}

func allowed(rel, text string) bool {
	for _, entry := range toneAllowlist[rel] {
		if strings.Contains(text, entry) {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate guard source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
