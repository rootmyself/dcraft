package visual

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestDocumentWrapsLongPairWithoutLosingValue(t *testing.T) {
	value := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	doc := core.Document{Blocks: []core.Block{{Kind: core.BlockKeyValues, Pairs: []core.Pair{{Key: "path", Value: value}}}}}
	lines := RenderDocument(Plain(), doc, 40)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped value: %q", lines)
	}
	if !strings.HasPrefix(lines[1], "      ") {
		t.Fatalf("continuation is not aligned with value: %q", lines)
	}
	if strings.ReplaceAll(strings.Join(lines, ""), " ", "") != "path"+value {
		t.Fatalf("value lost while wrapping: %q", lines)
	}
}

func TestDocumentWrapsHeadingsAndANSISpans(t *testing.T) {
	text := strings.Repeat("가", 30)
	doc := core.Document{Title: text, Blocks: []core.Block{{Kind: core.BlockHeading, Text: text}}}
	lines := RenderDocument(Plain(), doc, 40)
	if len(lines) != 4 {
		t.Fatalf("heading rows = %q", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "…") || Width(line) > 40 {
			t.Fatalf("heading was truncated: %q", line)
		}
	}
	colored := "\x1b[31m" + strings.Repeat("x", 45) + "\x1b[0m"
	wrapped := Wrap(colored, 20)
	if strings.ReplaceAll(strings.Join(wrapped, ""), "\n", "") != colored {
		t.Fatalf("ANSI span damaged: %q", wrapped)
	}
	for _, line := range wrapped {
		if Width(line) > 20 {
			t.Fatalf("ANSI line too wide: %q", line)
		}
	}
}

func TestDocumentWrapsAtBodyWidths(t *testing.T) {
	for _, width := range []int{40, 80, 120, 200} {
		value := strings.Repeat("abcdefghij", 24)
		doc := core.Document{Blocks: []core.Block{
			{Kind: core.BlockParagraph, Text: value},
			{Kind: core.BlockKeyValues, Pairs: []core.Pair{{Key: "path", Value: value}}},
			{Kind: core.BlockCallout, Text: value},
		}}
		layout := RenderDocumentLayout(Plain(), doc, width)
		for _, line := range layout.Lines {
			if Width(line) > width || strings.Contains(line, "…") {
				t.Fatalf("width %d: truncated or overflowing line %q", width, line)
			}
		}
		if len(layout.Band()) != len(layout.Lines) {
			t.Fatalf("width %d: wrapped rows not counted in band", width)
		}
	}
}

func TestNarrowPairUsesHangingIndent(t *testing.T) {
	doc := core.Document{Blocks: []core.Block{{
		Kind: core.BlockKeyValues, Pairs: []core.Pair{{Key: "long label", Value: "abcdefghijklmnopqrst"}},
	}}}
	lines := RenderDocument(Plain(), doc, 15)
	if len(lines) < 3 || !strings.HasPrefix(lines[1], "  ") || !strings.HasPrefix(lines[2], "  ") {
		t.Fatalf("narrow pair does not use two-cell indent: %q", lines)
	}
}

// The table header is fixed while its rows scroll; the summary above and the
// callout below never scroll at all.
func TestDocumentLayoutPinsTheTableHeader(t *testing.T) {
	th := Plain()
	doc := core.Document{Blocks: []core.Block{
		{Kind: core.BlockBadges, Badges: []core.Badge{{Kind: core.BadgeRunning, Text: "실행 중 25"}}},
		{Kind: core.BlockTable, Table: warningTable(25)},
		{Kind: core.BlockCallout, Callout: core.CalloutInfo, Text: "다음 행동"},
	}}
	layout := RenderDocumentLayout(th, doc, 80)

	fixed := layout.Fixed()
	if len(fixed) == 0 || !strings.Contains(fixed[0], "실행 중 25") {
		t.Fatalf("summary is not fixed: %q", fixed)
	}
	if !strings.Contains(fixed[len(fixed)-1], "서비스") {
		t.Fatalf("table header is not the last fixed line: %q", fixed)
	}
	if got := len(layout.Band()); got != 25 {
		t.Fatalf("band rows = %d, want 25", got)
	}
	if tail := layout.Tail(); len(tail) == 0 || !strings.Contains(strings.Join(tail, "\n"), "다음 행동") {
		t.Fatalf("callout is not in the tail: %q", tail)
	}
	if !layout.Pinned {
		t.Fatal("layout does not report a pinned header")
	}
	for _, line := range layout.Lines {
		if Width(line) > 80 {
			t.Fatalf("line too wide: %q", line)
		}
	}
}

// Without a table there is no header to pin, so the whole body scrolls as one
// band and nothing is silently fixed.
func TestDocumentLayoutWithoutTableScrollsEverything(t *testing.T) {
	th := Plain()
	doc := core.Document{Blocks: []core.Block{
		{Kind: core.BlockParagraph, Text: "가나다라마바사"},
		{Kind: core.BlockParagraph, Text: "아자차카타파하"},
	}}
	layout := RenderDocumentLayout(th, doc, 80)
	if layout.FixedEnd != 0 || layout.Pinned {
		t.Fatalf("fixed = %d pinned = %v", layout.FixedEnd, layout.Pinned)
	}
	if len(layout.Band()) != len(layout.Lines) {
		t.Fatalf("band = %d of %d lines", len(layout.Band()), len(layout.Lines))
	}
}

// Only the first table becomes the scroll band; a table used as a small inline
// summary keeps rendering normally.
func TestDocumentLayoutOnlyBandsTheFirstTable(t *testing.T) {
	th := Plain()
	doc := core.Document{Blocks: []core.Block{
		{Kind: core.BlockTable, Table: warningTable(3)},
		{Kind: core.BlockTable, Table: warningTable(2)},
	}}
	layout := RenderDocumentLayout(th, doc, 80)
	if got := len(layout.Band()); got != 3 {
		t.Fatalf("band rows = %d, want 3", got)
	}
	joined := strings.Join(layout.Tail(), "\n")
	if strings.Count(joined, "서비스") < 1 {
		t.Fatalf("second table was lost:\n%s", joined)
	}
}

// RenderDocument and RenderDocumentLayout agree line for line: the split is a
// view of one render, not a second renderer.
func TestDocumentLayoutMatchesRenderDocument(t *testing.T) {
	th := Plain()
	doc := allBlocksDocument()
	flat := RenderDocument(th, doc, 80)
	layout := RenderDocumentLayout(th, doc, 80)
	if strings.Join(flat, "\n") != strings.Join(layout.Lines, "\n") {
		t.Fatalf("layout differs from render:\n%v\nvs\n%v", flat, layout.Lines)
	}
}

// The fallback panel gives a plain string the same structured surface, and the
// compact variant drops the border but keeps the text and the exact width.
func TestPanelFramesAndFits(t *testing.T) {
	th := Plain()
	body := []string{"첫 줄", strings.Repeat("아주 긴 줄 ", 10), ""}
	for _, width := range []int{40, 80, 120} {
		lines := Panel(th, "로그 보기", body, width, true)
		if len(lines) < 4 {
			t.Fatalf("width %d: %d lines", width, len(lines))
		}
		if !strings.Contains(lines[0], "로그 보기") {
			t.Fatalf("width %d: title missing: %q", width, lines[0])
		}
		for i, line := range lines {
			if got := Width(line); got != width {
				t.Fatalf("width %d: row %d is %d cells: %q", width, i, got, line)
			}
		}

		compact := Panel(th, "로그 보기", body, width, false)
		for i, line := range compact {
			if got := Width(line); got != width {
				t.Fatalf("width %d compact: row %d is %d cells: %q", width, i, got, line)
			}
		}
		if strings.Contains(compact[0], th.Glyphs.TL) {
			t.Fatalf("compact panel still draws a border: %q", compact[0])
		}
	}
}

// The panel follows the injected ASCII theme: no UTF-8 box glyph can leak from a
// shared renderer.
func TestPanelUsesInjectedGlyphs(t *testing.T) {
	ascii := Resolve(Inputs{ThemeSetting: "dark", NoColor: true, Profile: ProfileNone, DarkBackground: true, UTF8: false})
	lines := Panel(ascii, "본문", []string{"내용"}, 40, true)
	joined := strings.Join(lines, "\n")
	for _, glyph := range []string{"╭", "╮", "╰", "╯", "─", "│"} {
		if strings.Contains(joined, glyph) {
			t.Fatalf("ASCII panel leaked %q:\n%s", glyph, joined)
		}
	}
	if !strings.Contains(lines[0], "+") {
		t.Fatalf("ASCII panel has no corner: %q", lines[0])
	}
}

// An empty body still produces a closed panel rather than a dangling title.
func TestPanelWithEmptyBody(t *testing.T) {
	th := Plain()
	lines := Panel(th, "제목", nil, 40, true)
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.Contains(lines[1], th.Glyphs.BL) {
		t.Fatalf("panel is not closed: %q", lines[1])
	}
}
