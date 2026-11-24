package termview

import (
	"io"
	"sync"
)

// inputQueue is the child's stdin. It exists because the emulator's own input
// pipe is synchronous: a write blocks until a reader consumes it, and the
// render loop must never block on a child that is busy drawing. A goroutine
// pumps the emulator's pipe into this queue, so a keystroke returns at once and
// the child reads it when it is ready.
//
// The queue is also where the emulator's replies to the child land (a cursor
// position report, a mode report). Merging them in one pump keeps the child's
// byte order intact: a reply the emulator owes cannot be reordered behind a key
// the user typed after it.
type inputQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputQueue() *inputQueue {
	q := &inputQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Read blocks until bytes are available or the queue is closed. It implements
// io.Reader so the child can use it as its terminal input.
func (q *inputQueue) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, q.buf)
	q.buf = q.buf[n:]
	return n, nil
}

// push appends bytes and wakes a waiting reader.
func (q *inputQueue) push(p []byte) {
	if len(p) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.buf = append(q.buf, p...)
	q.cond.Broadcast()
}

// close makes a pending and every later Read return EOF. Bytes already queued
// are still delivered first, so a command that exits after its last keystroke
// does not lose that keystroke.
func (q *inputQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.cond.Broadcast()
}
