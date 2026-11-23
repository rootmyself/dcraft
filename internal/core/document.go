package core

import "strings"

// BlockKind names the seven neutral body blocks a feature can publish. The
// document is deliberately colorless and width-free: the TUI decides how to
// paint and fold it, the CLI never renders it at all.
type BlockKind string

const (
	// BlockHeading is a short section title.
	BlockHeading BlockKind = "heading"
	// BlockParagraph is prose that may wrap.
	BlockParagraph BlockKind = "paragraph"
	// BlockKeyValues is a list of short labelled values.
	BlockKeyValues BlockKind = "keyvalues"
	// BlockTable is a scrollable table with a fixed header.
	BlockTable BlockKind = "table"
	// BlockBadges is a compact state summary.
	BlockBadges BlockKind = "badges"
	// BlockCallout is guidance the reader must not miss.
	BlockCallout BlockKind = "callout"
	// BlockDivider is a horizontal rule between groups.
	BlockDivider BlockKind = "divider"
	// BlockValueLines is line-oriented text in which the feature declares each
	// value's kind. It renders like a paragraph but paints the declared runs.
	BlockValueLines BlockKind = "value-lines"
)

// Document is the neutral result body the TUI renders. It carries no color,
// width or terminal knowledge: the renderer decides presentation.
type Document struct {
	Title  string
	Blocks []Block
}

// Block is one document node. Only the fields named by Kind are read.
type Block struct {
	Kind    BlockKind
	Text    string      // heading / paragraph text
	Pairs   []Pair      // key-values block
	Table   *Table      // table block
	Badges  []Badge     // badges block
	Callout CalloutKind // callout block
	Lines   []ValueLine // value-lines block
}

// TextRun is one declared segment of a line. An empty Kind is plain text, such
// as a label or punctuation; a value kind paints only that segment.
type TextRun struct {
	Text string
	Kind ValueKind
}

// ValueLine is one line of a value-classified body. The feature declares the
// runs; the renderer never infers a kind from the text.
type ValueLine []TextRun

// Text is the line as the CLI prints it.
func (l ValueLine) Text() string {
	var b strings.Builder
	for _, run := range l {
		b.WriteString(run.Text)
	}
	return b.String()
}

// ValueLinesText is the plain text of a value-classified body. It is the bridge
// that keeps a feature's CLI presenter and its TUI document on one formatter.
func ValueLinesText(lines []ValueLine) string {
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.Text()
	}
	return strings.Join(text, "\n")
}

// ValueKind describes a value's meaning independently of its presentation.
type ValueKind string

const (
	ValuePlain   ValueKind = ""
	ValuePath    ValueKind = "path"
	ValueURL     ValueKind = "url"
	ValueImage   ValueKind = "image"
	ValueCommand ValueKind = "command"
	ValueTime    ValueKind = "time"
	ValueService ValueKind = "service"
	ValueProject ValueKind = "project"
)

// Pair is one key-value row.
type Pair struct {
	Key, Value string
	Kind       ValueKind
}

// Table is a table block. Columns declare order and relative width weight;
// the renderer owns cell widths and may drop columns it cannot fit.
type Table struct {
	// Columns declares order and relative width weight. The renderer owns
	// cell widths and may drop columns it cannot fit.
	Columns []Column
	Rows    []Row
	// EmptyText explains an empty table. Required when Rows is empty so
	// "no services" never renders as a blank panel.
	EmptyText string
}

// Column is one table column.
type Column struct {
	Header string
	// Weight is the flexible width share; 0 means a fixed-width column.
	Weight int
	// Min and Max bound a flexible column in display cells.
	Min, Max int
	// Fixed is the exact width for a fixed column (Weight 0).
	Fixed int
	// DropPriority: lower numbers are dropped first. 0 = never drop.
	DropPriority int
	Align        Align
}

// Align is the horizontal alignment of a column. The zero value is left.
type Align string

const (
	// AlignLeft starts the cell at its first cell.
	AlignLeft Align = "left"
	// AlignRight ends the cell at its last cell; used for counts.
	AlignRight Align = "right"
	// AlignCenter centers the cell in its width.
	AlignCenter Align = "center"
)

// Row is one table row.
type Row struct {
	Cells []Cell
	// Selectable=false renders muted and cannot be chosen.
	Selectable bool
}

// Cell is one table cell.
type Cell struct {
	Text string
	Kind ValueKind
	// Badge renders Text with the state icon + Korean label instead of a
	// bare word. Style is semantic only; the renderer picks the color.
	Badge BadgeKind
	// Detail is the full value shown in the detail view when Text is
	// truncated. Empty means Text is already complete.
	Detail string
}

// BadgeKind is the semantic state of a badge. It is not a color: the renderer
// pairs every kind with an icon and keeps the Korean label in the text.
type BadgeKind string

const (
	// BadgeNone marks a cell without a semantic state. It is the zero value:
	// there is exactly one "no badge" sentinel in the program.
	BadgeNone       BadgeKind = ""
	BadgeRunning    BadgeKind = "running"
	BadgeStopped    BadgeKind = "stopped"
	BadgeUnhealthy  BadgeKind = "unhealthy"
	BadgeStarting   BadgeKind = "starting"
	BadgeNotCreated BadgeKind = "not_created"
	BadgeRisk       BadgeKind = "risk"
)

// Badge is one summary chip.
type Badge struct {
	Kind BadgeKind
	Text string
}

// CalloutKind is the severity of a callout block.
type CalloutKind string

const (
	CalloutInfo   CalloutKind = "info"
	CalloutNote   CalloutKind = "note"
	CalloutWarn   CalloutKind = "warn"
	CalloutDanger CalloutKind = "danger"
)

// ViewPresenter is an optional feature capability. A feature that implements
// it returns a neutral document the TUI renders; the string Presenter path
// stays for the other twelve features.
type ViewPresenter interface {
	Document(res Result) Document
}
