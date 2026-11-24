// Package termview renders a child process's terminal screen inside the TUI.
//
// A process that owns a pseudo-terminal draws with cursor movement, line
// erasure, colour and alternate screens: an interactive shell writes a prompt,
// echoes each key, and redraws the line when the user edits it. None of that is
// expressible as a list of output lines, so this package keeps the child's
// screen as a grid of cells and hands the TUI the rows it should draw.
//
// The grid is the contract. Callers never see the child's escape bytes: a
// terminal title, a cursor move or an OSC 8 link is consumed by the emulator and
// becomes cell state, not text. That is what keeps a hostile or colourful stream
// from moving dcraft's own cursor.
package termview

import (
	"image/color"
	"io"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Color is one cell's paint. A nil field means the terminal's own default, so a
// colorless renderer can produce text that carries no escape at all.
type Color struct {
	Fg    color.Color
	Bg    color.Color
	Attrs uint8
}

// Text attributes, mirroring the terminal's SGR bits a child can set. They are
// named here so the TUI never interprets a raw bit itself.
const (
	AttrBold          uint8 = 1 << 0
	AttrFaint         uint8 = 1 << 1
	AttrItalic        uint8 = 1 << 2
	AttrBlink         uint8 = 1 << 3
	AttrRapidBlink    uint8 = 1 << 4
	AttrReverse       uint8 = 1 << 5
	AttrConceal       uint8 = 1 << 6
	AttrStrikethrough uint8 = 1 << 7
)

// Cell is one grid position. Content is a single grapheme cluster; Width is 2
// for a wide (East Asian) rune, and the cell to its right is a placeholder with
// Width 0 that draws nothing of its own.
type Cell struct {
	Content string
	Width   int
	Color   Color
}

// Position is a grid coordinate, zero-based.
type Position struct{ X, Y int }

// DefaultScrollback is how many lines the grid keeps above the screen. It is
// large enough that a long build or test run stays reachable with PgUp while
// staying bounded.
const DefaultScrollback = 10000

// View is a child's terminal screen. Write feeds the child's output; Lines
// returns the scrollback and the visible screen as rows the TUI can draw.
//
// A View is safe for concurrent use: the child's output arrives on its own
// goroutine while the render loop reads the grid.
type View struct {
	mu   sync.Mutex
	emu  *vt.SafeEmulator
	rows int
	cols int
	// onlcr translates a bare newline to a carriage return plus newline. A real
	// terminal driver does this on output, so a pseudo-terminal's bytes already
	// carry it; a plain pipe's do not, and without the translation every line
	// after the first would start where the previous one ended.
	onlcr bool
	// lastByte is the final byte of the previous write, so a carriage return and
	// its newline split across two writes are still one line terminator.
	lastByte byte
	// in is the child's stdin. It is filled by a pump that reads the emulator's
	// own pipe, so a keystroke never blocks on a child that is busy drawing and
	// the emulator's replies stay ahead of the keys typed after them.
	in *inputQueue
}

// New creates a view for a child that owns a pseudo-terminal. Its output already
// carries the terminal driver's newline translation.
func New(cols, rows int) *View {
	return newView(cols, rows, false)
}

// NewPipe creates a view for a stream that is not a terminal, such as
// `docker compose logs` read from a pipe. A bare newline is translated the way a
// terminal driver translates it, so each line starts at the left edge instead of
// continuing under the previous one.
func NewPipe(cols, rows int) *View {
	return newView(cols, rows, true)
}

func newView(cols, rows int, onlcr bool) *View {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	emu := vt.NewSafeEmulator(cols, rows)
	// The child's colours reach the screen as it drew them: the grid keeps each
	// cell's own paint and converts nothing. The caller decides whether paint
	// may be emitted at all, through the Palette it passes to Paint.
	emu.SetDefaultForegroundColor(nil)
	emu.SetDefaultBackgroundColor(nil)
	emu.SetScrollbackSize(DefaultScrollback)
	v := &View{emu: emu, rows: rows, cols: cols, onlcr: onlcr, in: newInputQueue()}
	go v.pumpInput()
	return v
}

// pumpInput moves the emulator's own input into the queue. The emulator writes
// its replies (a cursor position report) to a synchronous pipe, so reading it
// here is what keeps the child from waiting on a reply nobody drains.
func (v *View) pumpInput() {
	buf := make([]byte, 256)
	for {
		n, err := v.emu.Read(buf)
		if n > 0 {
			v.in.push(buf[:n])
		}
		if err != nil {
			v.in.close()
			return
		}
	}
}

// Write accepts the child's output. It never blocks on the screen.
func (v *View) Write(p []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := p
	if v.onlcr {
		out = v.lfToCRLF(p)
	}
	if _, err := v.emu.Write(out); err != nil {
		return 0, err
	}
	// Write reports how many of the caller's bytes it consumed, not how many it
	// fed the grid: the newline translation makes the two lengths differ, and a
	// short count makes os/exec abandon the pipe.
	return len(p), nil
}

// lfToCRLF returns p with every bare line feed turned into a carriage return
// and a line feed. A feed that already follows a carriage return is left alone,
// including one whose carriage return arrived on the previous write, so a
// stream that mixes both forms is never double-translated.
func (v *View) lfToCRLF(p []byte) []byte {
	carriageBefore := func(i int) bool {
		if i > 0 {
			return p[i-1] == '\r'
		}
		return v.lastByte == '\r'
	}
	changed := false
	for i, b := range p {
		if b == '\n' && !carriageBefore(i) {
			changed = true
			break
		}
	}
	if !changed {
		if len(p) > 0 {
			v.lastByte = p[len(p)-1]
		}
		return p
	}
	out := make([]byte, 0, len(p)+8)
	for i, b := range p {
		if b == '\n' && !carriageBefore(i) {
			out = append(out, '\r')
		}
		out = append(out, b)
	}
	if len(p) > 0 {
		v.lastByte = p[len(p)-1]
	}
	return out
}

// Resize changes the grid. The emulator keeps what scrolled off the top, so a
// shorter screen does not lose output the reader can still scroll to.
func (v *View) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cols == cols && v.rows == rows {
		return
	}
	v.cols, v.rows = cols, rows
	v.emu.Resize(cols, rows)
}

// Size is the grid's current size.
func (v *View) Size() (cols, rows int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cols, v.rows
}

// Input is the stream the child reads its input from. A terminal's own replies
// to the child (a cursor position report, a mode report) travel this stream
// along with the user's keys, in the order the child must see them.
func (v *View) Input() io.Reader { return v.in }

// SendText writes bytes to the child as if the terminal had typed them. It
// never blocks: the child reads them when it is ready.
func (v *View) SendText(text string) {
	if text == "" {
		return
	}
	v.in.push([]byte(text))
}

// Close releases the input pipe. A command waiting on stdin then reads EOF and
// exits, which is how a detached attach ends cleanly.
func (v *View) Close() error {
	v.in.close()
	return v.emu.Close()
}

// ScrollbackLen is how many lines are reachable above the screen.
func (v *View) ScrollbackLen() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.emu.ScrollbackLen()
}

// Cursor is the child's cursor. Shown reports whether it is currently visible:
// a full-screen program hides it and draws its own highlight.
func (v *View) Cursor() (pos Position, shown bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.cursorLocked()
}

// Line is one row of cells, oldest first as returned by Lines. Cells is empty
// for a row the grid never wrote.
type Line struct{ Cells []Cell }

// Lines returns the scrollback and the visible screen as one row list, oldest
// first. The current screen is the tail, so a viewport that follows the tail
// shows what the child is drawing now, and PgUp reaches everything the grid
// still holds.
//
// A terminal's screen is always its full height, so the rows below the last
// thing the child drew are blank. They are not output, and counting them would
// make a twenty-line screen report twenty-four rows. The trailing blanks are
// trimmed, keeping the row the cursor sits on so a prompt at the bottom still
// has a cell to land in.
func (v *View) Lines() []Line {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.linesLocked()
}

func (v *View) linesLocked() []Line {
	sb := v.emu.Scrollback()
	held := sb.Len()
	out := make([]Line, 0, held+v.rows)
	for i := 0; i < held; i++ {
		out = append(out, lineFrom(sb.Line(i)))
	}
	for y := 0; y < v.rows; y++ {
		cells := make([]Cell, 0, v.cols)
		for x := 0; x < v.cols; x++ {
			cells = append(cells, v.cellAt(x, y))
		}
		out = append(out, Line{Cells: cells})
	}
	// Drop the blank rows after the last thing the child drew. A shell that has
	// printed a prompt has already drawn it, so the row a caret sits on is only
	// kept when it carries content or when the screen is otherwise empty; a bare
	// newline at the bottom is not a row of output.
	for len(out) > 0 && isBlankLine(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		// An empty screen still needs one row for the caret to sit on.
		out = append(out, Line{Cells: make([]Cell, v.cols)})
	}
	return out
}

// isBlankLine reports whether a row has nothing to draw.
func isBlankLine(line Line) bool {
	for _, c := range line.Cells {
		if c.Width == 0 {
			continue
		}
		if c.Content != "" && c.Content != " " {
			return false
		}
	}
	return true
}

// CursorLine is the absolute index of the cursor's row in the list Lines
// returns, or -1 when the cursor is hidden or sits below everything drawn.
func (v *View) CursorLine() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	pos, shown := v.cursorLocked()
	if !shown || pos.Y < 0 || pos.Y >= v.rows {
		return -1
	}
	// The list trims trailing blank screen rows, so the caret's row is measured
	// from the end of the screen: the last drawn row is the one the caret sits on
	// when the child is waiting at a prompt.
	lines := v.linesLocked()
	held := v.emu.ScrollbackLen()
	total := held + v.rows
	index := held + pos.Y
	// Shift by however many trailing rows were trimmed.
	trimmed := total - len(lines)
	index -= trimmed
	if index < 0 || index >= len(lines) {
		return -1
	}
	return index
}

func (v *View) cursorLocked() (Position, bool) {
	p := v.emu.CursorPosition()
	return Position{X: p.X, Y: p.Y}, !v.emu.CursorHidden()
}

func lineFrom(line uv.Line) Line {
	if line == nil {
		return Line{}
	}
	cells := make([]Cell, 0, len(line))
	for _, c := range line {
		cells = append(cells, Cell{
			Content: c.Content,
			Width:   c.Width,
			Color:   Color{Fg: c.Style.Fg, Bg: c.Style.Bg, Attrs: c.Style.Attrs},
		})
	}
	return Line{Cells: cells}
}

func (v *View) cellAt(x, y int) Cell {
	c := v.emu.CellAt(x, y)
	if c == nil {
		return Cell{Content: " ", Width: 1}
	}
	return Cell{
		Content: c.Content,
		Width:   c.Width,
		Color:   Color{Fg: c.Style.Fg, Bg: c.Style.Bg, Attrs: c.Style.Attrs},
	}
}

// Text renders a row as text only: no escape sequence, trailing blanks
// trimmed. This is the colorless form and also the one a width measurement
// needs.
func Text(cells []Cell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.Width == 0 {
			// The placeholder after a wide rune draws nothing on its own.
			continue
		}
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

// Paint is the one cell-to-string decision. A nil palette means the caller may
// not emit paint, so the row is text only: NO_COLOR, TERM=dumb and a non-TTY
// output all land here and stay free of escape sequences.
func Paint(cells []Cell, palette Palette) string {
	if palette == nil {
		return Text(cells)
	}
	var b strings.Builder
	// Each row opens with a reset. The child may leave a colour open across a
	// newline (`\x1b[31mred` with no reset), and a grid that redraws one row at
	// a time would otherwise start the next row in that colour. A row with no
	// paint writes nothing, so a colorless frame stays free of escapes.
	var current Color
	first := true
	painted := false
	for _, c := range cells {
		if c.Width == 0 {
			continue
		}
		content := c.Content
		if content == "" {
			content = " "
		}
		if first || !samePaint(current, c.Color) {
			if painted {
				b.WriteString(ansi.ResetStyle)
				painted = false
			}
			if sgr := palette.SGR(c.Color); sgr != "" {
				if !painted {
					// A reset is cheaper than trusting the previous row's
					// final state, and it is what makes a row independent.
					b.WriteString(ansi.ResetStyle)
				}
				b.WriteString(sgr)
				painted = true
			}
			current = c.Color
			first = false
		}
		b.WriteString(content)
	}
	if painted {
		b.WriteString(ansi.ResetStyle)
	}
	return strings.TrimRight(b.String(), " ")
}

// samePaint reports whether two cells share paint, so a run of same-coloured
// cells costs one sequence instead of one per cell.
func samePaint(a, b Color) bool {
	if a.Attrs != b.Attrs {
		return false
	}
	return sameColor(a.Fg, b.Fg) && sameColor(a.Bg, b.Bg)
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// Palette turns a cell's paint into an SGR sequence for the caller's terminal.
// A nil Palette emits no paint at all.
type Palette interface {
	SGR(c Color) string
}
