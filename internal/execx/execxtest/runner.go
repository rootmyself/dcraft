// Package execxtest provides a scriptable execx.Runner for unit tests.
package execxtest

import (
	"context"
	"strings"
	"sync"

	"github.com/rootmyself/dcraft/internal/execx"
)

// Runner is a fake external-command boundary. It records every call so tests
// can prove which commands were (and were not) executed.
type Runner struct {
	LookPathPath string
	LookPathErr  error
	// Handler returns the result for a command. A nil Handler fails every
	// command with exit code 127.
	Handler func(execx.Command) (execx.Result, error)
	// StreamHandler handles RunStream. It receives the stream so a test can
	// write live output. Nil falls back to Handler (or exit 127).
	StreamHandler func(execx.Command, execx.Stream) (execx.Result, error)

	mu    sync.Mutex
	calls []execx.Command
}

func (f *Runner) LookPath(file string) (string, error) {
	if f.LookPathErr != nil {
		return "", f.LookPathErr
	}
	if f.LookPathPath != "" {
		return f.LookPathPath, nil
	}
	return file, nil
}

func (f *Runner) Run(ctx context.Context, cmd execx.Command) (execx.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cloneCommand(cmd))
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	if f.Handler == nil {
		return execx.Result{Stderr: []byte("no fake handler for " + cmd.Name), ExitCode: 127}, nil
	}
	return f.Handler(cmd)
}

// RunStream executes with live I/O. See execx.Streamer.
func (f *Runner) RunStream(ctx context.Context, cmd execx.Command, stream execx.Stream) (execx.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cloneCommand(cmd))
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	if f.StreamHandler != nil {
		return f.StreamHandler(cmd, stream)
	}
	if f.Handler == nil {
		return execx.Result{Stderr: []byte("no fake handler for " + cmd.Name), ExitCode: 127}, nil
	}
	return f.Handler(cmd)
}

// Calls returns a copy of the recorded commands.
func (f *Runner) Calls() []execx.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]execx.Command, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallLines returns recorded invocations as "name arg arg" strings.
func (f *Runner) CallLines() []string {
	var lines []string
	for _, c := range f.Calls() {
		lines = append(lines, strings.TrimSpace(c.Name+" "+strings.Join(c.Args, " ")))
	}
	return lines
}

func cloneCommand(c execx.Command) execx.Command {
	c.Args = append([]string(nil), c.Args...)
	c.Env = append([]string(nil), c.Env...)
	return c
}
