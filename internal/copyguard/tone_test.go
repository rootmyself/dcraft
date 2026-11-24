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
	requireNoMatch(t, formalEnding, "formal sentence ending")
}

// TestProductCopyDetailsAreComplete guards the second half of rule 1: a
// parenthesised detail is a fragment, so it must end with a word. A space
// before the closing parenthesis means an ending was cut out and left a bare
// particle behind (`…닫아야 )`), which reads as a broken sentence instead of a
// detail. The rule is one character wide on purpose: every such string is a
// lost word, never a deliberate space.
func TestProductCopyDetailsAreComplete(t *testing.T) {
	requireNoMatch(t, danglingDetail, "parenthesised detail ending in a space")
}

// TestProductCopyHasNoTranslationese guards the phrases the copy pass removed.
// Each one is a habit rather than a fact: it appears whenever the text was
// assembled from clauses instead of written as a sentence. The list is short on
// purpose, so every entry can be justified when a legitimate use appears.
func TestProductCopyHasNoTranslationese(t *testing.T) {
	requireNoMatch(t, translationese, "translationese or stock connective")
}

// formalEnding matches the endings the guide forbids. "세요" covers ~하세요,
// ~으세요, and ~세요; "십시오" covers the honorific imperative; the question
// endings keep a new prompt from becoming a sentence again.
var formalEnding = regexp.MustCompile(`(니다|세요|십시오|까요|나요)`)

// danglingDetail matches a parenthesised detail that ends with whitespace
// before `)`, the shape a deleted ending leaves behind: `(…닫아야 )` and
// `(…어야 )` are the observed cases. A detail is a fragment, so it never
// deliberately ends in a space.
var danglingDetail = regexp.MustCompile(`[ \t]\)`)

// translationese matches the stock connectives the copy pass removed. They read
// as translation rather than Korean: a calque of "through this", an essay
// opener that announces its own point, and the noun-compounding "about X".
// "에 대한" is matched only as a whole phrase because "안에 있어" (located
// inside) and "PATH에 있어야" are literal Korean and are legitimate.
var translationese = regexp.MustCompile(`(이를 통해|핵심은|결론적으로|중요한 것은|관점에서|에 대한 [가-힣])`)

// toneAllowlist names strings that may keep a formal ending because they quote
// another tool's output or show an exact command/example. Keep each entry next
// to the file it excuses and state why.
var toneAllowlist = map[string][]string{}

// translationeseAllowlist names strings that legitimately contain a phrase the
// guard matches. An exact path, a quoted product name or another tool's wording
// is data, not prose. Add an entry only with the reason next to it.
var translationeseAllowlist = map[string][]string{}

type violation struct {
	file string
	line int
	text string
}

// requireNoMatch fails the build when any production string matches rule. Every
// violation is reported with its file, line and the line of the literal, so the
// fix is local.
func requireNoMatch(t *testing.T, rule *regexp.Regexp, what string) {
	t.Helper()
	violations := scanRoot(t, repoRoot(t), rule)
	if len(violations) == 0 {
		return
	}
	for _, v := range violations {
		t.Errorf("%s:%d %s in product copy: %s", v.file, v.line, what, v.text)
	}
	t.Fatalf("%d product copy strings have a %s; see docs/copy-tone.md", len(violations), what)
}

func scanRoot(t *testing.T, root string, rule *regexp.Regexp) []violation {
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
			out = append(out, scanFile(t, path, filepath.ToSlash(rel), rule)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	return out
}

func scanFile(t *testing.T, path, rel string, rule *regexp.Regexp) []violation {
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
			if rule.MatchString(line) {
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
	for _, entry := range translationeseAllowlist[rel] {
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
