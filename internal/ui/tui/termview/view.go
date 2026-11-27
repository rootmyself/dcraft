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

// Options configure a view. The zero value is a terminal stream with no caret.
type Options struct {
	// Onlcr translates a bare newline into a carriage return plus newline. A
	// pseudo-terminal's driver already does this; a plain pipe's does not, and
	// without it every line after the first would start where the previous one
	// ended.
	Onlcr bool
	// KeepCaretRow keeps the blank row the caret sits on at the end of the row
	// list. Only a screen that draws a caret wants it: an interactive shell
	// waits on the row after its prompt, while a log has no caret and its
	// trailing blank is not output.
	KeepCaretRow bool
	// ClearScrollbackOnED2 makes the child's erase-display (ESC[2J, what
	// `clear` sends) reset the scrollback as well as the screen. A shell's
	// `clear` means "start from an empty screen"; keeping the cleared rows
	// reachable above the prompt is what made the reader scroll into text the
	// command removed. Only an attached shell asks for this, and only on its
	// main screen: a full-screen child's own clear stays screen-only, because
	// the shell history behind it belongs to the reader.
	ClearScrollbackOnED2 bool
}

// View is a child's terminal screen. Write feeds the child's output; Window
// returns the rows a viewport should draw.
//
// A View is safe for concurrent use: the child's output arrives on its own
// goroutine while the render loop reads the grid.
type View struct {
	mu   sync.Mutex
	emu  *vt.SafeEmulator
	rows int
	cols int
	// opts is fixed at construction; the write path reads Onlcr and the row
	// list reads KeepCaretRow.
	opts Options
	// lastByte is the final byte of the previous write, so a carriage return and
	// its newline split across two writes are still one line terminator.
	lastByte byte
	// in is the child's stdin. It is filled by a pump that reads the emulator's
	// own pipe, so a keystroke never blocks on a child that is busy drawing and
	// the emulator's replies stay ahead of the keys typed after them.
	in *inputQueue
	// pumpDone closes when the input pump has stopped reading the emulator, so
	// Close never races it.
	pumpDone chan struct{}
	// closeOnce makes Close idempotent: the emulator's own Close is not
	// concurrency-safe, so it must run exactly once and after the pump.
	closeOnce sync.Once
	// closed is this view's own closed flag. A write after Close must fail
	// instead of feeding a pump that has stopped.
	closed bool
	// clearScrollback is set by the erase-display handler while the emulator is
	// parsing and acted on by Write once the parse is finished, because the
	// emulator's lock is held for the whole parse.
	clearScrollback bool
}

// New creates a view for a child that owns a pseudo-terminal. Its output already
// carries the terminal driver's newline translation.
//
// An attached shell's `clear` empties the scrollback as well: the shell asked to
// start from an empty screen, and leaving the cleared rows one PgUp away is what
// made `clear` look like it only half worked.
func New(cols, rows int) *View {
	return NewWith(cols, rows, Options{KeepCaretRow: true, ClearScrollbackOnED2: true})
}

// NewPipe creates a view for a stream that is not a terminal, such as
// `docker compose logs` read from a pipe. A bare newline is translated the way a
// terminal driver translates it, so each line starts at the left edge instead of
// continuing under the previous one.
func NewPipe(cols, rows int) *View {
	return NewWith(cols, rows, Options{Onlcr: true})
}

// NewWith creates a view with explicit options.
func NewWith(cols, rows int, opts Options) *View {
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
	v := &View{
		emu: emu, rows: rows, cols: cols, opts: opts,
		in: newInputQueue(), pumpDone: make(chan struct{}),
	}
	// Erase-display is what `clear` sends. Which parameter it carries depends on
	// the terminal and the clear implementation: ncurses on a screen/linux
	// terminal and busybox send ED 0 (`ESC[H ESC[J`), while xterm sends ED 2.
	// Both mean "start from an empty screen" to the reader. The emulator's own
	// handler pushes the visible rows onto the scrollback before clearing, which
	// is right for a full-screen program and wrong for a shell: the cleared text
	// would stay one PgUp away. This handler only records the request; the clear
	// itself happens after the emulator has finished parsing, so it never
	// re-enters a locked emulator.
	if opts.ClearScrollbackOnED2 {
		emu.RegisterCsiHandler('J', v.recordClearHandler())
	}
	go v.pumpInput()
	return v
}

// recordClearHandler notes an erase-display that means "clear" and lets the
// emulator's own handler draw the screen. The scrollback is emptied by Write
// after the parse, once the emulator's lock is free: calling into the emulator
// from inside a handler would deadlock on that lock.
//
// ED 0 and ED 2 both count. A shell's `clear` is `ESC[H ESC[J` on a screen or
// linux terminal and `ESC[H ESC[2J` on xterm; the reader asked for an empty
// screen either way. ED 1 (erase above) is a scroll-region edit, not a clear.
func (v *View) recordClearHandler() vt.CsiHandler {
	return func(params ansi.Params) bool {
		switch n, _, _ := params.Param(0, 0); n {
		case 0, 2:
			v.clearScrollback = true
		}
		// Returning false runs the emulator's own handler too, which is what
		// actually clears the visible rows.
		return false
	}
}

// pumpInput moves the emulator's own input into the queue. The emulator writes
// its replies (a cursor position report) to a synchronous pipe, so reading it
// here is what keeps the child from waiting on a reply nobody drains.
//
// The pump owns the emulator's Read for the view's lifetime: Close closes that
// pipe, Read returns EOF, and the goroutine exits. It signals pumpDone last so
// Close knows the emulator is no longer being read.
func (v *View) pumpInput() {
	defer close(v.pumpDone)
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
	if v.closed {
		// The emulator's own closed flag is not used: Close cannot set it
		// without racing the pump. This view's flag is what reports a write
		// after close, so a late write cannot block on a synchronous reply pipe
		// nobody is reading.
		return 0, io.ErrClosedPipe
	}
	out := p
	if v.opts.Onlcr {
		out = v.lfToCRLF(p)
	}
	if _, err := v.emu.Write(out); err != nil {
		return 0, err
	}
	// The attached shell's `clear` keeps no history: the parse above has
	// finished, so the emulator's lock is free and the scrollback can be
	// emptied without re-entering it. A full-screen child is excluded: its
	// clear is screen-only and the shell history behind it is not its to erase.
	if v.clearScrollback {
		v.clearScrollback = false
		if !v.emu.IsAltScreen() {
			v.emu.ClearScrollback()
		}
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
//
// This is a byte-level rewrite, so a newline inside an escape payload (a DCS or
// OSC string) is translated too. The emulator still consumes such a payload
// correctly; the bound is that the payload is not kept opaque.
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

// Resize changes the grid. It only ever grows: the emulator truncates the bottom
// of the screen when the grid gets shorter, which throws away the newest rows a
// reader is looking at (a shell's prompt is on the bottom row). Keeping the grid
// at the largest size it has had means a resize never destroys output, and the
// viewport simply draws the tail of it.
//
// Width works the same way: a narrower window draws the left part of each row
// and cuts the rest with the frame's own marker, so widening again restores
// what was there.
func (v *View) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if cols < v.cols {
		cols = v.cols
	}
	if rows < v.rows {
		rows = v.rows
	}
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
//
// The emulator's own Close is deliberately not called: it writes the field its
// Read and Write read without taking the lock the safe wrapper uses, so calling
// it races the pump goroutine. Closing the emulator's input pipe is what makes
// the pump's Read return EOF, which is the whole effect Close needed.
func (v *View) Close() error {
	v.closeOnce.Do(func() {
		v.mu.Lock()
		v.closed = true
		v.mu.Unlock()
		v.in.close()
		if pipe, ok := v.emu.InputPipe().(io.Closer); ok {
			_ = pipe.Close()
		}
		<-v.pumpDone
	})
	return nil
}

// ScrollbackLen is how many lines are reachable above the screen. A full-screen
// child has none: the emulator keeps no history for its alternate screen.
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

func (v *View) cursorLocked() (Position, bool) {
	p := v.emu.CursorPosition()
	return Position{X: p.X, Y: p.Y}, !v.emu.CursorHidden()
}

// Line is one row of cells, as returned by Window. Cells is empty for a row the
// grid never wrote.
type Line struct{ Cells []Cell }

// Window is one snapshot of what a viewport asks for: how many rows exist, the
// requested slice of them, and the caret's absolute row. It is one lock, so the
// counter, the drawn rows and the caret cannot describe different moments.
//
// Only the requested rows are materialised. A 10000-line scrollback costs the
// rows on screen, not all of them, which is what makes a repaint cheap enough to
// run on every keystroke.
type Window struct {
	// Total is how many rows are reachable.
	Total int
	// Start is the absolute index of the first returned row.
	Start int
	// Rows are the rows from Start up to the requested end.
	Rows []Line
	// CursorRow is the caret's absolute row, or -1 when it is not drawn.
	CursorRow int
	// CursorX is the caret's column within its row.
	CursorX int
}

// Window returns the rows in [start, end) of the row list, together with the
// total and the caret. A range past the end is clamped.
//
// A terminal's screen is always its full height, so the rows below the last
// thing the child drew are blank. They are not output, and counting them would
// make a twenty-line screen report twenty-four rows. Trailing blanks are dropped;
// a row the caret sits on is kept only when the view was built to draw one.
func (v *View) Window(start, end int) Window {
	v.mu.Lock()
	defer v.mu.Unlock()
	total := v.drawnRowsLocked()
	if start < 0 {
		start = 0
	}
	// Clamp start after the total is known, so a range past the end reports an
	// empty window at the end rather than a start beyond the total. A caller
	// that computes start+i or total-start from the result then gets a legal
	// index.
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	if end < start {
		end = start
	}
	out := Window{Total: total, Start: start, CursorRow: -1}
	held := v.scrollbackHeldLocked()
	sb := v.emu.Scrollback()
	out.Rows = make([]Line, 0, end-start)
	for i := start; i < end; i++ {
		switch {
		case i < held:
			out.Rows = append(out.Rows, lineFrom(sb.Line(i)))
		case i-held < v.rows:
			out.Rows = append(out.Rows, v.screenLineLocked(i-held))
		default:
			// An index the screen no longer holds: a blank row keeps the
			// caller's indexing correct.
			out.Rows = append(out.Rows, Line{Cells: make([]Cell, v.cols)})
		}
	}
	if pos, shown := v.cursorLocked(); shown && pos.Y >= 0 && pos.Y < v.rows && v.opts.KeepCaretRow {
		if row := held + pos.Y; row >= 0 && row < total {
			out.CursorRow = row
			out.CursorX = pos.X
		}
	}
	return out
}

// screenLineLocked is one visible screen row as a Line.
func (v *View) screenLineLocked(y int) Line {
	cells := make([]Cell, 0, v.cols)
	for x := 0; x < v.cols; x++ {
		cells = append(cells, v.cellAt(x, y))
	}
	return Line{Cells: cells}
}

// Lines returns the whole row list. It exists for tests and for callers that
// need everything; a render loop uses Window so it reads only what it draws.
func (v *View) Lines() []Line {
	return v.Window(0, v.Window(0, 0).Total).Rows
}

// scrollbackHeldLocked is how many scrollback rows the row list includes: none
// while a full-screen child owns the screen.
func (v *View) scrollbackHeldLocked() int {
	if v.emu.IsAltScreen() {
		return 0
	}
	return v.emu.ScrollbackLen()
}

// drawnRowsLocked is how many rows the list holds, computed without
// materialising them: every scrollback row, then the screen rows up to the last
// one that is not blank.
func (v *View) drawnRowsLocked() int {
	held := v.scrollbackHeldLocked()
	last := -1
	for y := v.rows - 1; y >= 0; y-- {
		if !v.screenRowBlankLocked(y) {
			last = y
			break
		}
	}
	// The caret's row is drawn even when it is blank, so it is the floor of the
	// list: a shell waiting at a prompt sits one row below its last output.
	if v.opts.KeepCaretRow {
		if pos, shown := v.cursorLocked(); shown && pos.Y >= 0 && pos.Y < v.rows && pos.Y > last {
			last = pos.Y
		}
	}
	if last < 0 {
		// An empty screen still needs one row for the caret to sit on.
		return held + 1
	}
	return held + last + 1
}

// screenRowBlankLocked reports whether one screen row has nothing drawn on it.
// It returns at the first cell that draws something, so a full screen costs one
// or two cells per row rather than a full row.
func (v *View) screenRowBlankLocked(y int) bool {
	for x := 0; x < v.cols; x++ {
		if !isBlankCell(v.cellAt(x, y)) {
			return false
		}
	}
	return true
}

// isBlankCell reports whether one cell has nothing to draw.
//
// A cell is not blank when it carries paint. A status bar or a selected row is
// drawn as spaces with a background colour or reverse video, and trimming it
// would delete part of a full-screen child's interface.
func isBlankCell(c Cell) bool {
	if c.Width == 0 {
		// The placeholder after a wide rune is part of that rune.
		return true
	}
	if c.Content != "" && c.Content != " " {
		return false
	}
	return c.Color.Bg == nil && c.Color.Attrs == 0
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
	// Each coloured run opens with a reset. The child may leave a colour open
	// across a newline (`\x1b[31mred` with no reset), and a grid that redraws one
	// row at a time would otherwise start the next row in that colour. A row
	// with no paint writes nothing, so a colorless frame stays free of escapes.
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
				// A reset before each run makes the row independent of whatever
				// the previous row left open.
				b.WriteString(ansi.ResetStyle)
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
	// The trim happens after the closing reset, so only trailing blanks are
	// removed and the reset itself always survives.
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
