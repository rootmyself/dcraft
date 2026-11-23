package visual

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/rootmyself/dcraft/internal/core"
)

func TestValueColorsAndPlainStructure(t *testing.T) {
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockKeyValues, Pairs: []core.Pair{
		{Key: "path", Value: "same", Kind: core.ValuePath},
		{Key: "url", Value: "same", Kind: core.ValueURL},
		{Key: "image", Value: "same", Kind: core.ValueImage},
		{Key: "command", Value: "same", Kind: core.ValueCommand},
		{Key: "service", Value: "same", Kind: core.ValueService},
		{Key: "project", Value: "same", Kind: core.ValueProject},
	}}}}
	for _, dark := range []bool{false, true} {
		for _, profile := range []Profile{Profile16, ProfileTrueColor} {
			th := Resolve(Inputs{Profile: profile, DarkBackground: dark, UTF8: true})
			colored := RenderDocument(th, doc, 80)
			plain := RenderDocument(Plain(), doc, 80)
			if len(colored) != len(plain) {
				t.Fatal("color changed line count")
			}
			var values []string
			for i, line := range colored {
				if ansi.Strip(line) != plain[i] {
					t.Fatalf("color changed text: %q != %q", ansi.Strip(line), plain[i])
				}
				if pos := strings.Index(line, "same"); pos >= 0 {
					values = append(values, line[strings.LastIndex(line[:pos], "\x1b["):pos])
				}
			}
			if len(values) != 6 {
				t.Fatalf("got %d values", len(values))
			}
			for i := 0; i < len(values); i += 2 {
				if values[i] == values[i+1] {
					t.Errorf("dark=%v profile=%v: value kinds share color %q", dark, profile, values[i])
				}
			}
			if profile == Profile16 && strings.Contains(strings.Join(colored, ""), "38;2") {
				t.Error("16-color terminal received truecolor")
			}
		}
	}
}

func TestTableBadgeAndDetailColors(t *testing.T) {
	th := Resolve(Inputs{Profile: ProfileTrueColor, DarkBackground: true, UTF8: true})
	table := core.Table{Columns: []core.Column{{Header: "name", Fixed: 8}, {Header: "state", Fixed: 12}},
		Rows: []core.Row{{Selectable: true, Cells: []core.Cell{
			{Text: "long-service-name", Kind: core.ValueService},
			{Text: "running", Badge: core.BadgeRunning},
		}}}}
	layout := LayoutTable(th, table, 80)
	if strings.Contains(layout.Rows[0], th.Paint(TokenName, "long")) {
		t.Error("table painted a value kind")
	}
	if !strings.Contains(layout.Rows[0], th.Paint(TokenSuccess, "✓ running   ")) {
		t.Errorf("badge has no success color: %q", layout.Rows[0])
	}
	details := strings.Join(RenderDetails(th, layout.Truncations, 80), "\n")
	if !strings.Contains(details, th.Paint(TokenName, "long-service-name")) {
		t.Errorf("detail lost value kind: %q", details)
	}
}
