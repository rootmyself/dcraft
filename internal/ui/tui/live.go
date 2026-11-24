package tui

import (
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/termview"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// liveState is the TUI side of an attached command or a log stream. Its output
// is a terminal screen, not a list of lines: a shell writes a prompt with no
// newline, echoes each key and redraws the line when the user edits it, and none
// of that survives a line-buffered reader. The grid in termview keeps the
// child's cursor and cells, and this file lays those rows out in the frame.
//
// Nothing here owns stack lifetime: leaving the screen cancels the run context
// and detaches, it never stops a service.
type liveState struct {
	mode core.LiveMode
	// view is the child's terminal screen.
	view *termview.View
	// writer is the stream the feature writes to. It feeds the grid and signals
	// the repaint, so one write is one screen update without a per-line message.
	writer *liveWriter
	// notice is the one state message under the output, such as "중단 중".
	notice string
	// stopping reports that a detach has been requested.
	stopping bool
	// once closes the view exactly once when the run ends.
	once sync.Once
}

// liveNotifyCapacity is the repaint signal's buffer. One is enough: the signal
// coalesces a burst of output into a single repaint, and a full buffer means a
// repaint is already pending.
const liveNotifyCapacity = 1

// liveTailRows is how many of the child's screen rows the management view keeps
// as the last output after a detach. It is a memory of what was on screen, not a
// log, so forty rows is plenty.
const liveTailRows = 40

// liveWriter fans the child's bytes into the grid and signals the render loop.
// The signal is non-blocking, so a child that prints faster than the screen
// repaints never waits on the interface.
type liveWriter struct {
	view   *termview.View
	notify chan struct{}
	mu     sync.Mutex
	closed bool
}

func (w *liveWriter) Write(p []byte) (int, error) {
	n, err := w.view.Write(p)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return n, err
	}
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}

// close stops the writer signalling the render loop. The caller closes the
// channel after this returns, so no send can follow the close: a feature
// goroutine may still be writing when its run is cancelled.
func (w *liveWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// setupLive wires a live feature's streams. Both modes get a grid: a log stream
// and an attached command differ in whether the child reads input, not in how
// its output is drawn.
func (m *Model) setupLive(f core.Feature) {
	live, ok := f.(core.LiveFeature)
	if !ok {
		m.req.IO = core.IO{}
		return
	}
	// The screen state exists before the size is measured: the header block is
	// part of the row budget and liveHeaderLines reads the screen kind. Measuring
	// first would size the child's terminal too tall, so a full-screen child would
	// draw under the header and footer.
	ch := make(chan struct{}, liveNotifyCapacity)
	m.liveCh = ch
	m.live = &liveState{mode: live.LiveMode()}
	cols, rows := m.liveGridSize()
	var view *termview.View
	if live.LiveMode() == core.LiveAttach {
		// An attached command owns a pseudo-terminal and draws with cursor
		// movement, so its bytes already carry the terminal's newline rule.
		view = termview.New(cols, rows)
	} else {
		// A log stream is not a terminal: `docker compose logs` writes bare
		// newlines, so the view applies the terminal driver's rule itself.
		view = termview.NewPipe(cols, rows)
	}
	writer := &liveWriter{view: view, notify: ch}
	m.live.view = view
	m.live.writer = writer
	// A live screen starts at the tail: the newest output is what the reader
	// came to see. Scrolling up stops following until the bottom is reached.
	m.liveTop = 0
	m.liveFollow = true
	m.req.IO = core.IO{
		Stdout: writer,
		Stderr: writer,
		Width:  cols,
		Height: rows,
	}
	if m.live.mode == core.LiveAttach {
		// Keys reach the child through the grid's input stream, which is also
		// where the terminal's own replies to the child travel.
		m.req.IO.Stdin = view.Input()
		m.req.IO.TTY = true
	}
}

// liveGridSize is the grid the child draws into: the body region the screen has
// for it, not the whole terminal. A child sized to the full window would draw
// under dcraft's header and footer.
func (m *Model) liveGridSize() (cols, rows int) {
	cols = m.bodyWidth()
	if cols < 1 {
		cols = 80
	}
	rows = m.liveBandRows()
	if rows < 1 {
		rows = 24
	}
	return cols, rows
}

// resizeLive keeps the child's grid the size of the band it is drawn in, so a
// full-screen child such as an editor fills the band and a resize reaches it.
func (m *Model) resizeLive() {
	if m.live != nil && m.live.view != nil {
		cols, rows := m.liveGridSize()
		m.live.view.Resize(cols, rows)
		m.req.IO.Width = cols
		m.req.IO.Height = rows
	}
	// The running `up` log is a grid too: a resize must reach it or its lines
	// wrap at the old column count.
	if m.progress != nil && m.progress.view != nil {
		cols := m.bodyWidth()
		if cols < 1 {
			cols = 80
		}
		rows := m.progressLogRows()
		if rows < 1 {
			rows = 8
		}
		m.progress.view.Resize(cols, rows)
	}
}

// closeLive ends the screen: the child's input closes, the grid is released and
// the screen state is cleared. The caller keeps what it wants first.
func (m *Model) closeLive() {
	if m.live == nil {
		return
	}
	m.live.once.Do(func() {
		// Stop the writer signalling before the channel closes: a feature
		// goroutine may still be writing when its run is cancelled.
		if m.live.writer != nil {
			m.live.writer.close()
		}
		if m.live.view != nil {
			_ = m.live.view.Close()
		}
		if m.liveCh != nil {
			close(m.liveCh)
		}
	})
	m.live = nil
	m.liveCh = nil
}

func tailOrKeep(tail, previous []string) []string {
	if len(tail) > 0 {
		return tail
	}
	return previous
}

// requestFor preserves collected inputs when the same feature is re-run and
// starts every other feature from the caller's target.
func (m *Model) requestFor(f core.Feature) core.Request {
	req := m.req
	if req.Feature != f.Descriptor().Name {
		req = core.Request{Feature: f.Descriptor().Name, Target: m.target}
	}
	req.Target = m.target
	req.Interactive = true
	req.JSON = false
	return req
}

// waitLiveCmd asks for one repaint. The grid is written by the child's own
// goroutine, so the screen learns about new output through this signal rather
// than through a per-line message: a burst of output costs one repaint, not
// hundreds.
func waitLiveCmd(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return liveTickMsg{}
	}
}

func (m *Model) updateLiveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An attached command owns the keyboard. The only key dcraft keeps is the
	// detach chord, because every other key is something a shell or an editor
	// needs: "?" is a character, ctrl+c interrupts the child, tab completes.
	if m.live.mode == core.LiveAttach {
		if msg.String() == "ctrl+]" {
			return m.stopLive(), nil
		}
		if bytes := keyBytes(msg); len(bytes) > 0 && m.live.view != nil {
			m.live.view.SendText(string(bytes))
		}
		return m, nil
	}
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "g", "G":
		// The log screen owns these keys: they move the output window and
		// never reach the child (logs has no input stream). A short log that
		// fits ignores them, so q/esc keep their single meaning.
		if m.liveScrolls() {
			m.liveScroll(msg.String())
			return m, nil
		}
	case "q", "esc", "ctrl+c":
		return m.stopLive(), nil
	}
	return m, nil
}

// liveScrolls reports whether the output overflows the band. It is the same
// computation the frame draws, so a key is never advertised by accident.
func (m *Model) liveScrolls() bool {
	if m.live == nil || m.live.view == nil {
		return false
	}
	return m.liveViewport().Overflow()
}

// liveScroll moves the output window and records whether the reader is still
// following the tail. Following resumes at the bottom; scrolling up stops it so
// arriving output does not drag the reader away from what they were reading.
func (m *Model) liveScroll(key string) {
	v := m.liveViewport()
	switch key {
	case "pgup":
		v.PgUp()
	case "pgdown":
		v.PgDn()
	case "home", "g":
		v.Home()
	case "end", "G":
		v.End()
	}
	m.liveTop = v.Top
	m.liveFollow = v.Top == v.MaxTop()
}

// stopLive requests cancellation. The engine reports the canceled outcome and
// the screen returns to the management view; services keep running.
func (m *Model) stopLive() *Model {
	if m.live == nil || m.live.stopping {
		return m
	}
	m.live.stopping = true
	m.live.notice = msgCanceling
	m.lastLive = m.captureLiveTail()
	if m.runCancel != nil {
		m.runCancel()
	}
	if m.live.view != nil {
		_ = m.live.view.Close()
	}
	return m
}

// captureLiveTail is what the management screen keeps after a detach: the rows
// the child's screen was showing, so the reader keeps the context they left in.
func (m *Model) captureLiveTail() []string {
	if m.live == nil || m.live.view == nil {
		return nil
	}
	total := m.live.view.Window(0, 0).Total
	start := total - liveTailRows
	if start < 0 {
		start = 0
	}
	// Only the tail is needed, so only the tail is read: the scrollback can hold
	// thousands of rows the panel will never show.
	win := m.live.view.Window(start, total)
	palette := m.livePalette()
	out := make([]string, 0, len(win.Rows))
	for _, line := range win.Rows {
		if text := termview.Paint(line.Cells, palette); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// livePalette is the paint decision for the child's cells. A colorless terminal
// gets text only, so NO_COLOR and TERM=dumb keep their no-escape guarantee: the
// child's own colours never smuggle an escape into dcraft's frame.
func (m *Model) livePalette() termview.Palette {
	if !m.theme.Color {
		return nil
	}
	return termview.NewPalette()
}

// liveHeaderLines is the fixed block above the output. It states the screen
// kind and the promise that the service keeps running, then the service, the
// instance and the command. None of these scroll: the context of what is being
// watched stays on screen while the output moves.
func (m *Model) liveHeaderLines() []string {
	if m.live == nil {
		// setupLive measures the band before the screen exists; the header is
		// empty then and the measurement uses the same row count either way.
		return nil
	}
	var state []string
	if m.live.mode == core.LiveAttach {
		// msgLiveAttached is the existing attach text; the running-state line
		// repeats the promise the stream sentence carries in one line.
		state = append(state, m.title(msgLiveAttached), m.hintText(msgLiveKeepsRunning))
	} else {
		state = append(state, m.title(msgLiveStreaming))
	}
	instance := msgLiveAllInstances
	if index := strings.TrimSpace(m.req.Options["index"]); index != "" {
		instance = "#" + index
	}
	return append(state,
		msgLiveService+": "+serviceScopeText(m.req.Services),
		msgLiveInstance+": "+instance,
		msgLiveCommand+": "+m.liveCommandText(),
	)
}

// liveCommandText names what is running: the attached argv when the screen
// forwards keys, otherwise the feature's own command name. It never invents a
// flag the caller did not ask for.
func (m *Model) liveCommandText() string {
	if len(m.req.Args) > 0 {
		return strings.Join(m.req.Args, " ")
	}
	if m.active != nil {
		return m.active.Descriptor().Name
	}
	return m.req.Feature
}

// liveNoticeLines is the stopping message below the output. It is part of the
// layout budget so it can never push the band past the frame.
func (m *Model) liveNoticeLines() []string {
	if m.live == nil || m.live.notice == "" {
		return nil
	}
	return []string{"", m.noticeText(m.live.notice)}
}

// liveFixedRows is how many body rows the live screen spends before the output
// band: the optional navigation row, the title block, the header block and its
// blank separator.
func (m *Model) liveFixedRows() int {
	rows := 0
	if m.navLine() != "" {
		rows++
	}
	rows += m.titleRows()
	rows += len(m.liveHeaderLines()) + 1
	return rows
}

// liveBandRows is how many rows are available for the output, the position row
// and the notice together: everything the body has left after the fixed header.
// A zero result means no WindowSizeMsg has arrived yet.
func (m *Model) liveBandRows() int {
	budget := m.bodyBudget()
	if budget <= 0 {
		return 0
	}
	available := budget - m.liveFixedRows() - len(m.liveNoticeLines())
	if available < 1 {
		available = 1
	}
	return available
}

// liveViewport is the output window: the band, minus the position row when it is
// shown. Following is applied last so the window reaches the final line instead
// of stopping one short.
func (m *Model) liveViewport() visual.Viewport {
	if m.live == nil || m.live.view == nil {
		return visual.Viewport{}
	}
	return m.liveViewportOf(m.liveRowCount())
}

// liveRowCount is how many rows the grid holds, read without materialising them
// so a key handler that only needs the window never pays for the rows.
func (m *Model) liveRowCount() int {
	if m.live == nil || m.live.view == nil {
		return 0
	}
	return m.live.view.Window(0, 0).Total
}

// liveViewportOf is the one window computation over a given total. The layout
// pass reads the total once and draws a window of the same grid, so the counter,
// the drawn rows and the keys cannot describe different moments.
func (m *Model) liveViewportOf(total int) visual.Viewport {
	if m.live == nil || m.live.view == nil {
		return visual.Viewport{}
	}
	height := m.liveBandRows()
	if height <= 0 {
		// Unknown size: nothing is off screen yet, and the next WindowSizeMsg
		// reflows it.
		return visual.Viewport{Height: total, Total: total}
	}
	if total > height {
		// The indicator gets its own row instead of covering the last output
		// line: a covered line would be unreachable at the bottom.
		height--
	}
	if height < 1 {
		height = 1
	}
	v := visual.Viewport{Top: m.liveTop, Height: height, Total: total}.Clamp()
	if m.liveFollow {
		v.End()
	}
	return v
}

// livePositionRow is the one-row position indicator directly above the keycap
// footer: `46-59/60` on the left and the keys that move it on the right. It is
// empty when everything fits, so a short screen costs no row.
func (m *Model) livePositionRow(v visual.Viewport) string {
	if !v.Overflow() {
		return ""
	}
	left := v.Counter()
	right := msgLiveScrollKeys
	if m.live.mode == core.LiveAttach {
		// Every key belongs to the child here; naming the TUI's scroll keys
		// would promise something the screen does not do.
		right = ""
	}
	width := m.bodyWidth()
	if width <= 0 {
		if right == "" {
			return left
		}
		return left + "  " + right
	}
	if right == "" {
		return m.hintText(left)
	}
	gap := width - visual.Width(left) - visual.Width(right)
	if gap < 1 {
		gap = 1
	}
	return m.hintText(left) + strings.Repeat(" ", gap) + m.hintText(right)
}

// liveLayout is the live screen: fixed header, the child's screen, position row
// and the stopping notice. It is one layout pass; the keys and the indicator use
// liveViewport too, so what is drawn and what PgUp moves cannot disagree.
//
// The position row sits flush above the keycap footer, not directly under the
// output: it is the last thing before the keys, where a status line belongs, and
// a short screen leaves it in the same place instead of floating mid-screen.
func (m *Model) liveLayout() bodyLayout {
	vp := m.liveViewportOf(m.liveRowCount())
	header := m.liveHeaderLines()
	notice := m.liveNoticeLines()
	position := m.livePositionRow(vp)

	lines := make([]string, 0, m.liveFixedRows()+vp.Visible()+len(notice)+2)
	if nav := m.navLine(); nav != "" {
		lines = append(lines, nav)
	}
	if title := m.screenTitle(); title != "" {
		// The position lives on its own row above the footer, so the title
		// stays a title instead of also being a counter.
		lines = append(lines, m.titleRow(title, ""), "")
	}
	lines = append(lines, header...)
	lines = append(lines, "")

	lines = append(lines, m.liveOutputRows(vp)...)

	if position != "" {
		// Pad the gap so the position and the notice are the final body rows.
		// The frame fills leftover rows with blanks anyway; doing it here puts
		// the indicator where the reader looks for it.
		if budget := m.bodyBudget(); budget > 0 {
			for len(lines)+len(notice)+1 < budget {
				lines = append(lines, "")
			}
		}
		lines = append(lines, position)
	}
	lines = append(lines, notice...)

	m.liveTop = vp.Top
	return bodyLayout{lines: lines, viewport: vp, scrolls: vp.Overflow()}
}

// liveOutputRows draws the window's rows from the child's grid. The cursor is
// painted into the cell it sits on when the child still shows it, so the reader
// can see where typing will land; a full-screen child that hides it keeps its
// own highlight.
func (m *Model) liveOutputRows(vp visual.Viewport) []string {
	if m.live == nil || m.live.view == nil {
		return nil
	}
	start, end := vp.Window()
	// One snapshot carries the rows and the caret, so a write cannot move the
	// caret onto a row from a different moment than the cells drawn.
	win := m.live.view.Window(start, end)
	palette := m.livePalette()
	// Only an attached command has a caret, and only a painted screen can show
	// one: the inversion is an escape, and NO_COLOR promises none.
	cursorLine, cursorX := -1, 0
	if m.live.mode == core.LiveAttach && palette != nil {
		cursorLine, cursorX = win.CursorRow, win.CursorX
	}

	width := m.bodyWidth()
	out := make([]string, 0, len(win.Rows))
	for i, line := range win.Rows {
		cells := line.Cells
		if start+i == cursorLine {
			cells = withCursorAt(cells, cursorX)
		}
		row := termview.Paint(cells, palette)
		// The scrolling band carries the approved one-cell gutter. The child
		// drew to the full grid width, so the last cell is what the gutter
		// takes; the row is cut with the explicit marker instead of widening.
		if width > 1 && vp.Overflow() {
			row = visual.Fit(row, width-1, m.theme.Glyphs.Ell)
		}
		out = append(out, row)
	}
	if width > 1 && vp.Overflow() && len(out) > 0 {
		gutter := visual.Scrollbar(m.theme, len(out), vp.Top, vp.Visible(), vp.Total)
		for i := range out {
			cell := " "
			if i < len(gutter) {
				cell = gutter[i]
			}
			out[i] += cell
		}
	}
	return out
}

// withCursorAt inverts the cell at x, so a shell prompt shows where typing
// lands. A colorless terminal never reaches here: the inversion is an escape
// sequence, and NO_COLOR promises none.
func withCursorAt(cells []termview.Cell, x int) []termview.Cell {
	// A wide rune owns a cell plus a placeholder that draws nothing. Inverting
	// the placeholder would have to materialise a space, making the row one cell
	// wider than the grid, so the caret belongs on the rune.
	if x > 0 && x < len(cells) && cells[x].Width == 0 {
		x--
	}
	if x < 0 || x >= len(cells) {
		return cells
	}
	out := append([]termview.Cell{}, cells...)
	out[x].Color.Attrs ^= termview.AttrReverse
	return out
}

// keyBytes converts a Bubble Tea key to the bytes a terminal would send. It
// covers printable input, editing and navigation; unmapped keys are dropped.
func keyBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyShiftUp:
		return []byte("\x1b[1;2A")
	case tea.KeyShiftDown:
		return []byte("\x1b[1;2B")
	case tea.KeyShiftRight:
		return []byte("\x1b[1;2C")
	case tea.KeyShiftLeft:
		return []byte("\x1b[1;2D")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}
	s := msg.String()
	if len(s) == 6 && strings.HasPrefix(s, "ctrl+") {
		ch := s[5]
		if ch >= 'a' && ch <= 'z' {
			return []byte{ch - 'a' + 1}
		}
	}
	return nil
}
