package visual

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rootmyself/dcraft/internal/core"
)

func valueLineDoc() core.Document {
	return core.Document{Blocks: []core.Block{{Kind: core.BlockValueLines, Lines: []core.ValueLine{
		{{Text: "출처: "}, {Text: "https://example.test/source", Kind: core.ValueURL}},
		{{Text: "경로: "}, {Text: "/tmp/output", Kind: core.ValuePath}},
		{{Text: "명령: "}, {Text: "dcraft up", Kind: core.ValueCommand}},
		{{Text: "  "}, {Text: "dcraft status", Kind: core.ValueCommand}},
	}}}}
}

// A value-lines block keeps the CLI text intact and paints only the declared
// runs: the URL, path and command are distinguishable, labels stay plain.
func TestValueLinesPaintDeclaredRunsOnly(t *testing.T) {
	th := Resolve(Inputs{Profile: ProfileTrueColor, DarkBackground: true, UTF8: true})
	doc := valueLineDoc()
	colored := RenderDocument(th, doc, 80)
	plain := RenderDocument(Plain(), doc, 80)
	if len(colored) != len(plain) {
		t.Fatalf("color changed the line count: %d != %d", len(colored), len(plain))
	}
	for i := range colored {
		if got, want := ansi.Strip(colored[i]), plain[i]; got != want {
			t.Fatalf("line %d text changed: %q != %q", i, got, want)
		}
	}
	for _, want := range []string{
		th.Paint(TokenURL, "https://example.test/source"),
		th.Paint(TokenInfo, "/tmp/output"),
		th.Paint(TokenCommand, "dcraft up"),
		th.Paint(TokenCommand, "dcraft status"),
	} {
		if !strings.Contains(strings.Join(colored, "\n"), want) {
			t.Fatalf("missing painted run %q:\n%s", want, strings.Join(colored, "\n"))
		}
	}
	if strings.Contains(colored[0], th.Paint(TokenURL, "출처: ")) || !strings.Contains(colored[0], "출처: ") {
		t.Fatalf("the label did not stay plain: %q", colored[0])
	}
}

// The 16-color fallback never leaks a 256-color or truecolor sequence, and the
// text is still exactly the plain text.
func TestValueLinesUseNearest16Colors(t *testing.T) {
	th := Resolve(Inputs{Profile: Profile16, DarkBackground: true, UTF8: true})
	if !th.Color {
		t.Fatal("16-color terminal lost color")
	}
	out := strings.Join(RenderDocument(th, valueLineDoc(), 80), "\n")
	for _, bad := range []string{"38;2", "38;5", "48;2", "48;5"} {
		if strings.Contains(out, bad) {
			t.Fatalf("16-color output contains %q: %q", bad, out)
		}
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("16-color output has no SGR: %q", out)
	}
	if got, want := ansi.Strip(out), strings.Join(RenderDocument(Plain(), valueLineDoc(), 80), "\n"); got != want {
		t.Fatalf("16-color text changed: %q != %q", got, want)
	}
}

// A document with no known width renders the same text and does not panic.
func TestValueLinesRenderWithoutWidth(t *testing.T) {
	out := RenderDocument(Plain(), valueLineDoc(), 0)
	if len(out) != 4 || !strings.Contains(out[0], "https://example.test/source") {
		t.Fatalf("sizeless value lines = %q", out)
	}
}
