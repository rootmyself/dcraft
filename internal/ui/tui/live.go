package tui

import (
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
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
	notice   string
	stopping bool
	once     sync.Once
}

const liveLineLimit = 400

func (l *liveState) append(line string) {
	l.lines = append(l.lines, line)
	if len(l.lines) > liveLineLimit {
		l.lines = l.lines[len(l.lines)-liveLineLimit:]
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
	ch := make(chan string, 512)
	writer := &liveWriter{ch: ch}
	m.liveCh = ch
	m.live = &liveState{mode: live.LiveMode(), writer: writer, ch: ch, lines: []string{}}
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
	case "q", "esc", "ctrl+c":
		return m.stopLive(), nil
	}
	return m, nil
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

func (m *Model) liveView() string {
	live := m.live
	var b strings.Builder
	if live.mode == core.LiveAttach {
		b.WriteString(msgLiveAttached)
	} else {
		b.WriteString(msgLiveStreaming)
	}
	b.WriteString("\n")
	if len(m.req.Services) > 0 {
		b.WriteString(msgLiveService + ": " + strings.Join(m.req.Services, ", ") + "\n")
	}
	if len(m.req.Args) > 0 {
		b.WriteString(msgLiveCommand + ": " + strings.Join(m.req.Args, " ") + "\n")
	}
	b.WriteString("\n")
	limit := m.height - 12
	if limit < 5 {
		limit = 20
	}
	start := 0
	if len(live.lines) > limit {
		start = len(live.lines) - limit
	}
	for _, line := range live.lines[start:] {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if live.notice != "" {
		b.WriteString("\n")
		b.WriteString(m.noticeText(live.notice))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
	closed  bool
	ch      chan string
	tail    []string
}

func (w *liveWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial += string(p)
	for {
		index := strings.IndexByte(w.partial, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(w.partial[:index], "\r")
		w.partial = w.partial[index+1:]
		w.emit(line)
	}
	return len(p), nil
}

func (w *liveWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.partial != "" {
		w.emit(strings.TrimSuffix(w.partial, "\r"))
		w.partial = ""
	}
}

func (w *liveWriter) emit(line string) {
	if w.closed {
		return
	}
	w.tail = append(w.tail, line)
	if len(w.tail) > liveLineLimit {
		w.tail = w.tail[len(w.tail)-liveLineLimit:]
	}
	select {
	case w.ch <- line:
	default:
	}
}

func (w *liveWriter) tailLines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.tail...)
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
