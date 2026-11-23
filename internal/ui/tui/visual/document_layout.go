package visual

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// DocumentLayout is a rendered document split into what stays and what scrolls.
//
// Lines holds the whole body in reading order. FixedEnd is the line count
// before the scroll band and BandEnd is the line count up to the end of the
// band, so:
//
//	Fixed() → never scrolls   (summary rows and the table header)
//	Band()  → scrolls         (table rows)
//	Tail()  → never scrolls   (next-action guidance)
//
// Without a table the whole body is the band: there is no header to pin, so
// everything may scroll. That is the right trade for a plain result body, and
// it keeps one mechanism for both kinds of screen.
type DocumentLayout struct {
	Lines    []string
	FixedEnd int
	BandEnd  int
	// Pinned reports that a table header exists in Fixed and must stay visible
	// while the band scrolls.
	Pinned bool
}

// Fixed is the part that never scrolls.
func (l DocumentLayout) Fixed() []string { return l.Lines[:l.FixedEnd] }

// Band is the scrollable part.
func (l DocumentLayout) Band() []string { return l.Lines[l.FixedEnd:l.BandEnd] }

// Tail is the part after the band that never scrolls.
func (l DocumentLayout) Tail() []string { return l.Lines[l.BandEnd:] }

// RenderDocumentLayout renders a document and records the scroll band. The
// lines are identical to RenderDocument: this is one render pass with the
// boundaries kept, not a second layout algorithm.
func RenderDocumentLayout(th Theme, doc core.Document, width int) DocumentLayout {
	return RenderDocumentLayoutOptions(th, doc, width, RenderOptions{})
}

// RenderOptions are the presentation choices the caller owns. The document
// itself stays width- and mode-free: a feature describes what it has, and the
// screen decides how much of it fits.
type RenderOptions struct {
	// Compact omits decorative dividers and folds tables to their leading columns.
	Compact bool
	// Scrollbar reserves the last content cell of the scrolling table for the
	// approved one-cell scrollbar gutter. Only the table loses the cell: the
	// summary and guidance above and below it keep the full width, so a wide
	// path is not shortened by decoration that does not touch it.
	Scrollbar bool
}

// RenderDocumentLayoutOptions is RenderDocumentLayout with explicit
// presentation options.
func RenderDocumentLayoutOptions(th Theme, doc core.Document, width int, opts RenderOptions) DocumentLayout {
	if width <= 0 {
		// Before the first WindowSizeMsg there is no width to lay out. The
		// content is returned unwrapped instead of vanishing: a model built
		// before its size arrives must still render something complete.
		lines := unlaidDocument(th, doc)
		return DocumentLayout{Lines: lines, BandEnd: len(lines)}
	}
	// The screen decides compact mode from width *and* height, so a tall table
	// on a short terminal folds like a narrow one. The renderer never re-reads
	// the terminal size.
	tableOpts := TableOptions{Compact: opts.Compact}
	bandWidth := width
	if opts.Scrollbar && bandWidth > 1 {
		bandWidth--
	}
	var lines []string
	gap := false
	add := func(block []string) {
		if len(block) == 0 {
			return
		}
		if gap && len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, block...)
		gap = true
	}

	if strings.TrimSpace(doc.Title) != "" {
		for _, line := range Wrap(doc.Title, width) {
			lines = append(lines, th.Bold(TokenAccent, line))
		}
	}

	layout := DocumentLayout{}
	banded := false
	for _, block := range doc.Blocks {
		if block.Kind == core.BlockTable && block.Table != nil && !banded && len(block.Table.Rows) > 0 {
			table := LayoutTableOptions(th, *block.Table, bandWidth, tableOpts)
			if gap && len(lines) > 0 {
				lines = append(lines, "")
			}
			if table.Header != "" {
				lines = append(lines, table.Header)
			}
			layout.FixedEnd = len(lines)
			layout.Pinned = table.Header != ""
			lines = append(lines, table.Rows...)
			layout.BandEnd = len(lines)
			banded = true
			gap = true
			continue
		}
		if opts.Compact && compactDrops(block.Kind) {
			continue
		}
		switch block.Kind {
		case core.BlockHeading:
			var heading []string
			for _, line := range Wrap(block.Text, width) {
				heading = append(heading, th.Bold(TokenAccent, line))
			}
			add(heading)
		case core.BlockParagraph:
			add(Wrap(block.Text, width))
		case core.BlockValueLines:
			add(renderValueLines(th, block.Lines, width))
		case core.BlockKeyValues:
			add(renderPairs(th, block.Pairs, width))
		case core.BlockBadges:
			add(renderBadges(th, block.Badges, width))
		case core.BlockTable:
			if block.Table == nil {
				continue
			}
			add(LayoutTableOptions(th, *block.Table, width, tableOpts).Lines())
		case core.BlockDivider:
			add([]string{divider(th, width)})
		case core.BlockCallout:
			add(renderCallout(th, block.Callout, block.Text, width))
		default:
			continue
		}
	}
	layout.Lines = trimTrailingBlank(lines)
	if !banded {
		layout.FixedEnd = 0
		layout.BandEnd = len(layout.Lines)
		layout.Pinned = false
	}
	if layout.BandEnd > len(layout.Lines) {
		layout.BandEnd = len(layout.Lines)
	}
	if layout.FixedEnd > layout.BandEnd {
		layout.FixedEnd = layout.BandEnd
	}
	return layout
}

// compactDrops omits purely decorative blocks in compact mode.
func compactDrops(kind core.BlockKind) bool {
	return kind == core.BlockDivider
}

// unlaidDocument renders a document without any width decisions: text stays
// intact, table cells are joined by two spaces. It is only used when the width
// is unknown, and the next resize re-renders everything properly.
func unlaidDocument(th Theme, doc core.Document) []string {
	var lines []string
	if strings.TrimSpace(doc.Title) != "" {
		lines = append(lines, doc.Title)
	}
	add := func(block []string) {
		if len(block) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, block...)
	}
	for _, block := range doc.Blocks {
		switch block.Kind {
		case core.BlockHeading:
			add([]string{block.Text})
		case core.BlockParagraph:
			add(strings.Split(strings.TrimRight(block.Text, "\n"), "\n"))
		case core.BlockValueLines:
			var out []string
			for _, line := range block.Lines {
				out = append(out, paintValueLine(th, line))
			}
			add(out)
		case core.BlockKeyValues:
			var out []string
			for _, p := range block.Pairs {
				out = append(out, p.Key+"  "+p.Value)
			}
			add(out)
		case core.BlockBadges:
			var chips []string
			for _, b := range block.Badges {
				glyph, _ := badgeGlyph(th, b.Kind)
				if glyph != "" && !strings.HasPrefix(b.Text, glyph) {
					chips = append(chips, glyph+" "+b.Text)
					continue
				}
				chips = append(chips, b.Text)
			}
			if len(chips) > 0 {
				add([]string{strings.Join(chips, "   ")})
			}
		case core.BlockTable:
			if block.Table == nil {
				continue
			}
			if len(block.Table.Rows) == 0 {
				add([]string{emptyTableText(th, block.Table.EmptyText, 0)})
				continue
			}
			var out []string
			headers := make([]string, len(block.Table.Columns))
			for i, col := range block.Table.Columns {
				headers[i] = col.Header
			}
			out = append(out, strings.Join(headers, "  "))
			for _, row := range block.Table.Rows {
				cells := make([]string, len(row.Cells))
				for i, cell := range row.Cells {
					text := cell.Text
					if cell.Badge != core.BadgeNone {
						text = badgeText(th, cell.Badge, text)
					}
					cells[i] = text
				}
				out = append(out, strings.Join(cells, "  "))
			}
			add(out)
		case core.BlockDivider:
			add([]string{strings.Repeat(th.Glyphs.H, 40)})
		case core.BlockCallout:
			label, _ := calloutLabel(block.Callout)
			add([]string{label + " · " + block.Text})
		}
	}
	return trimTrailingBlank(lines)
}

// TableRows accepts the number of table rows a table block contains. It lets a
// caller that owns a fixed row budget ask whether the table has any rows before
// deciding how much room to give it.
func TableRows(doc core.Document) int {
	for _, block := range doc.Blocks {
		if block.Kind == core.BlockTable && block.Table != nil {
			return len(block.Table.Rows)
		}
	}
	return 0
}

// Panel frames a body in a titled box. It is the transition-period surface for a
// feature that still returns a plain string: the text is unchanged, but it sits
// in the same visual language as the structured documents.
//
// With decorated=false the border and title rule are dropped and the body keeps
// the full width, which is what compact mode wants. Every returned line is
// exactly width cells when width is positive.
func Panel(th Theme, title string, body []string, width int, decorated bool) []string {
	body = trimTrailingBlank(append([]string{}, body...))
	if width <= 0 {
		return append([]string{title}, body...)
	}
	if !decorated {
		out := make([]string, 0, len(body)+1)
		if title != "" {
			out = append(out, Fit(th.Bold(TokenAccent, Truncate(title, width, th.Ellipsis())), width, th.Ellipsis()))
		}
		for _, line := range body {
			out = append(out, Fit(line, width, th.Ellipsis()))
		}
		return out
	}

	inner := width - 4
	if inner < 1 {
		inner = 1
	}
	top := th.Glyphs.TL + th.Glyphs.H + " "
	topTitle := ""
	if title != "" {
		topTitle = Truncate(title, inner, th.Ellipsis())
		top += topTitle
	}
	// The rule fills up to the corner, so the top row is closed exactly like
	// the bottom one. Filling the whole remaining width would push the corner
	// off the row.
	fill := width - Width(top) - Width(th.Glyphs.TR)
	if fill < 0 {
		fill = 0
	}
	top = Fit(top+strings.Repeat(th.Glyphs.H, fill)+th.Glyphs.TR, width, th.Ellipsis())

	out := []string{top}
	for _, line := range body {
		text := Fit(strings.TrimRight(line, " "), inner, th.Ellipsis())
		out = append(out, th.Glyphs.V+" "+text+" "+th.Glyphs.V)
	}
	bottom := th.Glyphs.BL + strings.Repeat(th.Glyphs.H, max(width-2, 0)) + th.Glyphs.BR
	out = append(out, Fit(bottom, width, th.Ellipsis()))
	return out
}
