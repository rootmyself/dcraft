// Package execx is dcraft's narrow boundary for running external commands.
// Business rules depend on execx.Runner, never on os/exec directly, so they can
// be tested without Docker or a shell. Tests use execxtest.Runner.
package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
)

// Command describes one external process invocation.
type Command struct {
	Name string
	Args []string
	// Dir is the working directory. Empty means the caller's current one.
	Dir string
	// Env is the complete environment when non-nil. Nil inherits the process
	// environment.
	Env []string
	// Stdin is fed to the process. Nil means no input (an empty stdin).
	Stdin io.Reader
}

// Result is captured process output. A non-zero ExitCode is not an error: many
// Docker commands communicate state through exit status and stderr text.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Succeeded reports whether the process exited with status 0.
func (r Result) Succeeded() bool { return r.ExitCode == 0 }

// Combined returns stderr followed by stdout, trimmed of surrounding space.
// Callers use it only for short diagnostic detail.
func (r Result) Combined() string {
	var b bytes.Buffer
	b.Write(r.Stderr)
	if len(r.Stderr) > 0 && len(r.Stdout) > 0 {
		b.WriteByte('\n')
	}
	b.Write(r.Stdout)
	return string(bytes.TrimSpace(b.Bytes()))
}

// Runner is the external command boundary.
type Runner interface {
	// LookPath resolves an executable name the way the OS would.
	LookPath(file string) (string, error)
	// Run executes the command. A start failure (missing binary, exec error)
	// is returned as an error; a non-zero exit status is returned in Result
	// with a nil error.
	Run(ctx context.Context, cmd Command) (Result, error)
}

// OSRunner is the production Runner.
type OSRunner struct{}

func NewOSRunner() *OSRunner { return &OSRunner{} }

func (OSRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (OSRunner) Run(ctx context.Context, c Command) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Env != nil {
		cmd.Env = c.Env
	}
	cmd.Stdin = c.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = -1
		}
		return res, nil
	}
	return res, err
}
