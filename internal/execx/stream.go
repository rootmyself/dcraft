package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// Stream describes live terminal I/O for one external command.
//
// With TTY false the command reads Stdin and writes Stdout/Stderr directly and
// never sees a terminal. With TTY true dcraft allocates a pseudo-terminal for
// the child, forwards bytes in both directions, and puts the caller's terminal
// into raw mode only when Stdin is a terminal; the previous mode is always
// restored before RunStream returns, including on failure and cancellation.
type Stream struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
	// Width and Height size the child pseudo-terminal. Zero derives the size
	// from the caller's terminal, then falls back to 80x24.
	Width  int
	Height int
}

// Streamer is the optional live-output capability of a Runner. Features that
// attach to a process require it; plain captured execution keeps using Run.
type Streamer interface {
	RunStream(ctx context.Context, cmd Command, stream Stream) (Result, error)
}

// tailLimit bounds the output kept for diagnostics. The live stream itself is
// never buffered into the result.
const tailLimit = 8 << 10

// RunStream implements Streamer with an operating-system process.
func (OSRunner) RunStream(ctx context.Context, c Command, stream Stream) (Result, error) {
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if stream.Stdout == nil {
		stream.Stdout = io.Discard
	}
	if stream.Stderr == nil {
		stream.Stderr = io.Discard
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	if !stream.TTY {
		return runPiped(ctx, cmd, stream)
	}
	return runPTY(ctx, cmd, stream)
}

func runPiped(ctx context.Context, cmd *exec.Cmd, stream Stream) (Result, error) {
	tail := &tailBuffer{limit: tailLimit}
	cmd.Stdin = stream.Stdin
	// Docker Compose reports some exec failures (a missing binary, a stopped
	// service) on stdout. Capture both streams for diagnostics; the live
	// writers still receive every byte.
	cmd.Stdout = io.MultiWriter(stream.Stdout, tail)
	cmd.Stderr = io.MultiWriter(stream.Stderr, tail)
	err := cmd.Run()
	res := Result{Stderr: tail.Bytes()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = -1
		}
		return res, nil
	}
	return Result{}, err
}

func runPTY(ctx context.Context, cmd *exec.Cmd, stream Stream) (Result, error) {
	restore, err := makeRaw(stream.Stdin)
	if err != nil {
		return Result{}, err
	}
	defer restore()

	size := &pty.Winsize{Rows: uint16(stream.Height), Cols: uint16(stream.Width)}
	if size.Rows == 0 || size.Cols == 0 {
		size = terminalSize(stream)
	}
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = ptmx.Close() }()

	tail := &tailBuffer{limit: tailLimit}
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		_, _ = io.Copy(io.MultiWriter(stream.Stdout, tail), ptmx)
	}()
	if stream.Stdin != nil {
		go func() { _, _ = io.Copy(ptmx, stream.Stdin) }()
	}

	waitErr := cmd.Wait()
	// Give the output copier a chance to drain the pseudo-terminal before the
	// master is closed; a command that exits immediately must not lose its
	// last lines.
	select {
	case <-outputDone:
	case <-time.After(2 * time.Second):
		_ = ptmx.Close()
		<-outputDone
	}
	_ = ptmx.Close()

	res := Result{Stderr: tail.Bytes()}
	if waitErr == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = -1
		}
		return res, nil
	}
	return Result{}, waitErr
}

// makeRaw puts a real terminal stdin into raw mode and returns the restore
// function. A non-terminal reader is left alone: the TUI forwards bytes
// without owning the terminal.
func makeRaw(stdin io.Reader) (func(), error) {
	f, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(int(f.Fd()), state) }, nil
}

func terminalSize(stream Stream) *pty.Winsize {
	if f, ok := stream.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if size, err := pty.GetsizeFull(f); err == nil && size.Rows > 0 && size.Cols > 0 {
			return size
		}
	}
	return &pty.Winsize{Rows: 24, Cols: 80}
}

// tailBuffer keeps the last limit bytes written to it. It is safe for the
// single writer each command uses.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.limit > 0 && len(p) > b.limit {
		p = p[len(p)-b.limit:]
	}
	b.buf.Write(p)
	if b.limit > 0 && b.buf.Len() > b.limit {
		trimmed := b.buf.Bytes()[b.buf.Len()-b.limit:]
		copied := append([]byte(nil), trimmed...)
		b.buf.Reset()
		b.buf.Write(copied)
	}
	return n, nil
}

func (b *tailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
