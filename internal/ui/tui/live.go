package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// liveState is the TUI side of an attached process: the child writes to a
// liveWriter, and for core.LiveAttach the user's keys are forwarded through a
// byte reader. Nothing here owns stack lifetime: leaving the screen cancels
// the run context and detaches, it never stops a service.
type liveState struct {
	mode     core.LiveMode
	writer   *liveWriter
	keys     *keyReader
	ch       chan string
	lines    []string
	dropped  int
	notice   string
	stopping bool
	once     sync.Once
}

const liveLineLimit = 400

// liveChannelCapacity is the buffered delivery channel between the child and the
// render loop. It absorbs a burst without blocking the child; a longer burst
// drops lines, which the indicator then states.
const liveChannelCapacity = 512

// append adds one output line and keeps the tail bounded. Dropped lines are
// counted so the position indicator can say that older output is no longer
// reachable instead of pretending the stream started at the cap.
func (l *liveState) append(line string) {
	// Sanitize once, where the line enters the model. Every later reader
	// (the viewport, the scrollbar, the kept tail) then measures real cells.
	l.lines = append(l.lines, visual.SanitizeLine(line))
	if over := len(l.lines) - liveLineLimit; over > 0 {
		l.lines = l.lines[over:]
		l.dropped += over
	}
}

func (l *liveState) close() {
	l.once.Do(func() {
		l.writer.close()
		close(l.ch)
		if l.keys != nil {
			l.keys.close()
		}
	})
}

// setupLive wires a live feature's streams. Only LiveAttach receives an input
// pipe and TTY semantics; logs are output-only and stay colorless.
func (m *Model) setupLive(f core.Feature) {
	live, ok := f.(core.LiveFeature)
	if !ok {
		m.req.IO = core.IO{}
		return
	}
	ch := make(chan string, liveChannelCapacity)
	writer := &liveWriter{ch: ch}
	m.liveCh = ch
	m.live = &liveState{mode: live.LiveMode(), writer: writer, ch: ch, lines: []string{}}
	// A live screen starts at the tail: the newest output is what the reader
	// came to see. Scrolling up stops following until the bottom is reached.
	m.liveTop = 0
	m.liveFollow = true
	m.req.IO = core.IO{
		Stdout: writer,
		Stderr: writer,
		Width:  m.width,
		Height: m.height,
	}
	if m.live.mode == core.LiveAttach {
		m.live.keys = newKeyReader()
		m.req.IO.Stdin = m.live.keys
		m.req.IO.TTY = true
	}
}

func (m *Model) closeLive() {
	if m.live != nil {
		m.live.writer.flush()
		m.live.close()
		m.live = nil
		m.liveCh = nil
	}
}

// drainLive moves already-produced lines into the render list before the
// channel is closed. A fast command can finish before the TUI processes every
// live message; its output still belongs on screen.
func (m *Model) drainLive() {
	if m.live == nil || m.liveCh == nil {
		return
	}
	for {
		select {
		case line, ok := <-m.liveCh:
			if !ok {
				return
			}
			m.live.append(line)
		default:
			return
		}
	}
}

func lastLines(lines []string, limit int) []string {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return append([]string{}, lines...)
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

func waitLiveCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return liveLineMsg{line: line}
	}
}

func (m *Model) updateLiveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "?" && m.helpAvailable() {
		// The live row advertises help; the overlay opens over the output
		// instead of being forwarded to the attached process.
		m.openHelp()
		return m, nil
	}
	if m.live.mode == core.LiveAttach {
		if msg.String() == "ctrl+]" {
			return m.stopLive(), nil
		}
		if bytes := keyBytes(msg); len(bytes) > 0 && m.live.keys != nil {
			m.live.keys.write(bytes)
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

// liveScrolls reports whether the output band overflows the window. It is the
// same computation the frame draws, so a key is never advertised by accident.
func (m *Model) liveScrolls() bool {
	if m.live == nil {
		return false
	}
	return m.liveViewport().Overflow()
}

// liveScroll moves the output window and records whether the reader is still
// following the tail. Following resumes at the bottom; scrolling up stops it so
// arriving lines do not drag the reader away from what they were reading.
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
	m.lastLive = lastLines(m.live.writer.tailLines(), 12)
	if m.runCancel != nil {
		m.runCancel()
	}
	if m.live.keys != nil {
		m.live.keys.close()
	}
	return m
}

// liveHeaderLines is the fixed block above the output. It states the screen
// kind and the promise that the service keeps running, then the service, the
// instance and the command. None of these scroll: the context of what is being
// watched stays on screen while the output moves.
func (m *Model) liveHeaderLines() []string {
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

// liveNeedsPositionRow reports whether the position row is shown. It appears
// when the output overflows or when the tail cap dropped older lines, because
// both are facts the reader cannot see in the band alone.
func (m *Model) liveNeedsPositionRow() bool {
	if m.live == nil {
		return false
	}
	return m.liveDropped() > 0 || len(m.live.lines) > m.liveBandRows()
}

// liveViewport is the output window: the band, minus the position row when it is
// shown. Following is applied last so the window reaches the final line instead
// of stopping one short.
func (m *Model) liveViewport() visual.Viewport {
	if m.live == nil {
		return visual.Viewport{}
	}
	total := len(m.live.lines)
	height := m.liveBandRows()
	if height <= 0 {
		// Unknown size: nothing is off screen yet, and the next WindowSizeMsg
		// reflows it.
		return visual.Viewport{Height: total, Total: total}
	}
	if m.liveNeedsPositionRow() {
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

// liveCounter is the position statement: the visible range, the reachable
// total, and an explicit note when the tail cap forced older lines out. Dropped
// output is stated rather than silently missing.
func (m *Model) liveCounter(v visual.Viewport) string {
	counter := v.Counter()
	if counter == "" {
		return ""
	}
	if dropped := m.liveDropped(); dropped > 0 {
		counter += " · " + fmt.Sprintf(msgLiveDropped, dropped)
	}
	return counter
}

// liveDropped is how many produced lines the window can no longer reach: the
// ones the tail cap evicted plus the ones a full delivery channel skipped. Both
// are output the reader could have seen, so the indicator states their sum
// instead of implying the stream started at the cap.
func (m *Model) liveDropped() int {
	if m.live == nil {
		return 0
	}
	dropped := m.live.dropped
	if m.live.writer != nil {
		dropped += m.live.writer.droppedLines()
	}
	return dropped
}

// livePositionRow is the one-row position indicator directly above the keycap
// footer: `46-59/60` on the left and the keys that move it on the right. It is
// empty when everything fits and nothing was dropped, so a short log costs no
// row.
func (m *Model) livePositionRow(v visual.Viewport) string {
	if !m.liveNeedsPositionRow() {
		return ""
	}
	left := m.liveCounter(v)
	if left == "" {
		// Only the dropped-lines note remains, which is still a fact worth
		// stating even when the retained lines fit.
		left = fmt.Sprintf(msgLiveDropped, m.liveDropped())
	}
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

// attachLogScrollbar paints the approved one-cell gutter over the log band. The
// output lines are arbitrary child text and are never re-wrapped, so the gutter
// takes the last body cell and a line that already reached it is cut with the
// explicit marker; the reader keeps the position row and the counter.
func (m *Model) attachLogScrollbar(rows []string, vp visual.Viewport) []string {
	width := m.bodyWidth()
	if width <= 1 {
		return rows
	}
	gutter := visual.Scrollbar(m.theme, len(rows), vp.Top, vp.Visible(), vp.Total)
	if len(gutter) == 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, line := range rows {
		cell := " "
		if i < len(gutter) {
			cell = gutter[i]
		}
		out[i] = visual.Fit(line, width-1, m.theme.Glyphs.Ell) + cell
	}
	return out
}

// liveLayout is the live screen: fixed header, scrolling output band, position
// row and the stopping notice. It is one layout pass; the keys and the
// indicator use liveViewport too, so what is drawn and what PgUp moves cannot
// disagree.
//
// The position row sits flush above the keycap footer, not directly under the
// output: it is the last thing before the keys, where a status line belongs, and
// a short log leaves it in the same place instead of floating mid-screen.
func (m *Model) liveLayout() bodyLayout {
	vp := m.liveViewport()
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
	start, end := vp.Window()
	output := append([]string{}, m.live.lines[start:end]...)
	// The log viewport is the second scrolling content list the contract gives
	// a scrollbar. The output band is the live feed, not a table, so the gutter
	// is added over the drawn rows instead of narrowing the child's own output.
	if vp.Overflow() && len(output) > 0 {
		output = m.attachLogScrollbar(output, vp)
	}
	lines = append(lines, output...)

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
	return bodyLayout{lines: lines, viewport: vp, scrolls: m.liveNeedsPositionRow()}
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

// liveWriter turns arbitrary process output into whole lines for the TUI. It
// never blocks the child: if the renderer falls behind, lines are dropped
// rather than stalling the stream.
type liveWriter struct {
	mu      sync.Mutex
	partial string
	// pending is the row a carriage return is still rewriting. A progress bar
	// prints `10%\r20%\r…` without a newline; each segment replaces the last,
	// so the screen draws one row instead of one row per update.
	pending string
	closed  bool
	ch      chan string
	tail    []string
	// dropped counts lines the delivery channel skipped because the renderer
	// fell behind. They are real output, so the indicator states them.
	dropped int
}

func (w *liveWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial += string(p)
	for {
		newline := strings.IndexByte(w.partial, '\n')
		rewrite := strings.IndexByte(w.partial, '\r')
		switch {
		case newline < 0 && rewrite < 0:
			return len(p), nil
		case newline >= 0 && (rewrite < 0 || newline < rewrite):
			// The newline ends the row. A rewrite replaced whatever the row
			// held, so the last segment is the row and the pending text before
			// it is not output.
			segment := strings.TrimSuffix(w.partial[:newline], "\r")
			w.partial = w.partial[newline+1:]
			w.pending = ""
			w.emit(segment)
		default:
			w.pending = w.partial[:rewrite]
			w.partial = w.partial[rewrite+1:]
		}
	}
}

func (w *liveWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A row still being rewritten is its latest segment: the text after the last
	// carriage return is what the reader saw, so any earlier segment is stale.
	row := strings.TrimSuffix(w.partial, "\r")
	if row == "" {
		return
	}
	w.pending, w.partial = "", ""
	w.emit(row)
}

func (w *liveWriter) emit(line string) {
	if w.closed {
		return
	}
	// The writer keeps raw text; liveState.append is the one place a line is
	// sanitized, so the kept tail and the drawn rows come from the same rule.
	w.tail = append(w.tail, line)
	if len(w.tail) > liveLineLimit {
		w.tail = w.tail[len(w.tail)-liveLineLimit:]
	}
	select {
	case w.ch <- line:
	default:
		// The renderer is behind. Dropping is what keeps the child from
		// blocking, and the count makes the loss visible.
		w.dropped++
	}
}

// droppedLines reports how many lines the delivery channel skipped.
func (w *liveWriter) droppedLines() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}

func (w *liveWriter) tailLines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The kept tail is shown on the management screen, so it runs through the
	// same sanitizer as a drawn row: a control byte must not reach the terminal
	// from either path.
	out := make([]string, len(w.tail))
	for i, line := range w.tail {
		out[i] = visual.SanitizeLine(line)
	}
	return out
}

func (w *liveWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// keyReader is an in-memory stdin for an attached process. The TUI writes
// bytes with write; close makes Read return EOF, which detaches a command
// waiting on stdin.
type keyReader struct {
	mu     sync.Mutex
	ch     chan []byte
	buf    []byte
	closed bool
}

func newKeyReader() *keyReader {
	return &keyReader{ch: make(chan []byte, 64)}
}

func (k *keyReader) Read(p []byte) (int, error) {
	for len(k.buf) == 0 {
		chunk, ok := <-k.ch
		if !ok {
			return 0, io.EOF
		}
		k.buf = chunk
	}
	n := copy(p, k.buf)
	k.buf = k.buf[n:]
	return n, nil
}

func (k *keyReader) write(bytes []byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	select {
	case k.ch <- bytes:
	default:
	}
}

func (k *keyReader) close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	close(k.ch)
}
