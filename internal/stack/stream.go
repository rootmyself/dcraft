package stack

import (
	"context"
	"fmt"

	"github.com/rootmyself/dcraft/internal/core"
)

// LogsRequest is one live log read. Services are Compose service names (empty
// means every configured service). Index is a zero-based container instance
// index, or -1 for the whole service; the Compose adapter maps it to Compose's
// one-based --index.
type LogsRequest struct {
	Services   []string
	Index      int
	Follow     bool
	Tail       string
	Since      string
	Timestamps bool
	Color      bool
}

// ExecRequest is one command execution. Command is the exact argv the user
// supplied: dcraft never inserts a shell. Index is a zero-based instance index
// or -1.
type ExecRequest struct {
	Service string
	Index   int
	Command []string
	TTY     bool
}

// ExecResult is a completed command. A non-zero ExitCode is a normal result;
// start failures and cancellation are returned as errors.
type ExecResult struct {
	ExitCode int
	// Output is a bounded capture of the child's combined output, for Korean
	// diagnostics only. It is never a secret fetch.
	Output string
}

// Streamer is the narrow live-output boundary for logs and exec. It is
// implemented by internal/compose and consumed only by those features.
type Streamer interface {
	Logs(ctx context.Context, target core.Target, req LogsRequest, streams core.IO) error
	Exec(ctx context.Context, target core.Target, req ExecRequest, streams core.IO) (ExecResult, error)
}

// CommandError is a completed streaming command with a non-zero status.
type CommandError struct {
	Action   string
	ExitCode int
	Output   string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("docker compose %s exited with status %d", e.Action, e.ExitCode)
}
