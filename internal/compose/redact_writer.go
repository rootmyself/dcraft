package compose

import (
	"bytes"
	"io"
	"sync"
)

// maxStreamLine bounds the bytes held for one unterminated line. Compose and
// Docker progress output is line oriented, so this only guards against a tool
// that writes a very long line without a newline.
const maxStreamLine = 64 << 10

// redactWriter forwards streamed tool output through the secret session's
// redaction. The captured path redacts the whole output before it becomes a
// result; the live path shows the same bytes while they arrive, so it must
// remove the same values instead of revealing them on screen.
//
// Redaction runs on a completed line. That keeps a value split across two
// writes from surviving, but a value that spans a newline is not matched here
// (the captured path would match it). Compose progress output is line
// oriented, so the tradeoff keeps the live view honest without buffering the
// whole run.
type redactWriter struct {
	mu     sync.Mutex
	out    io.Writer
	redact func(string) string
	buf    []byte
}

func newRedactWriter(out io.Writer, redact func(string) string) *redactWriter {
	if redact == nil {
		redact = func(text string) string { return text }
	}
	return &redactWriter{out: out, redact: redact}
}

func (w *redactWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if err := w.emit(w.buf[:i+1]); err != nil {
			return n, err
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) >= maxStreamLine {
		// A line that never ends would grow without bound. Emit what is held;
		// a value straddling this boundary can escape, which is why the limit
		// is far above any real progress line.
		if err := w.emit(w.buf); err != nil {
			return n, err
		}
		w.buf = nil
	}
	return n, nil
}

// Flush writes the last partial line. The caller runs it once the command has
// returned so the final line is neither lost nor left for a later run.
func (w *redactWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	line := w.buf
	w.buf = nil
	return w.emit(line)
}

// emit redacts and writes one line. The caller holds the lock.
func (w *redactWriter) emit(line []byte) error {
	_, err := io.WriteString(w.out, w.redact(string(line)))
	return err
}
