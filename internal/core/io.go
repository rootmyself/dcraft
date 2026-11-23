package core

import (
	"io"
	"os"
)

// IO carries the live terminal streams for a feature that attaches to a
// running process (logs, exec). It is deliberately UI-owned: only the
// interface knows whether its terminal is interactive, and JSON mode never
// attaches. Nil fields fall back to the process's own standard streams.
//
// TTY means "the caller can relay an interactive terminal": stdin and stdout
// are terminals in a CLI, or the TUI's attached screen can forward input and
// render the child's pseudo-terminal. It never means "assume a shell".
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
	// Width and Height are the caller's terminal size when known. They size
	// the child pseudo-terminal; zero means "derive from Stdin or 80x24".
	Width  int
	Height int
	// Progress is an opt-in stream for a long operation's own tool output
	// (currently `up`). It is separate from Stdout/Stderr so a normal capture
	// result and a live terminal feature keep their existing meaning. Nil means
	// capture-only: the CLI never sets it, so its output is unchanged.
	Progress io.Writer
}

// WithDefaults fills unset streams with the process standard streams. Tests
// and the TUI always set their own.
func (s IO) WithDefaults() IO {
	if s.Stdin == nil {
		s.Stdin = os.Stdin
	}
	if s.Stdout == nil {
		s.Stdout = os.Stdout
	}
	if s.Stderr == nil {
		s.Stderr = os.Stderr
	}
	return s
}
