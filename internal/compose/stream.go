package compose

import (
	"context"
	"strconv"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Logs implements stack.Streamer. It follows the project or an explicit
// service selection without stopping anything; cancellation only detaches the
// stream.
func (c *Compose) Logs(ctx context.Context, t core.Target, req stack.LogsRequest, streams core.IO) error {
	// Logs are output-only: never allocate a pseudo-terminal or change the
	// caller's terminal mode. Color is an explicit request, not a TTY side
	// effect.
	streams.TTY = false
	args := append(t.ComposeArgs(), "logs")
	if req.Follow {
		args = append(args, "--follow")
	}
	if !req.Color {
		args = append(args, "--no-color")
	}
	if req.Tail != "" {
		args = append(args, "--tail", req.Tail)
	}
	if req.Since != "" {
		args = append(args, "--since", req.Since)
	}
	if req.Timestamps {
		args = append(args, "--timestamps")
	}
	if req.Index >= 0 {
		args = append(args, "--index", strconv.Itoa(req.Index+1))
	}
	args = append(args, req.Services...)

	res, err := c.stream(ctx, t, args, streams)
	if err != nil {
		return err
	}
	if !res.Succeeded() {
		return &stack.CommandError{Action: "logs", ExitCode: res.ExitCode, Output: summarize(res.Combined())}
	}
	return nil
}

// Exec implements stack.Streamer. The user's argv is passed through unchanged
// after the service name; dcraft never adds `sh -c`. A non-zero remote exit is
// a normal result the feature maps to dcraft's own failure contract.
func (c *Compose) Exec(ctx context.Context, t core.Target, req stack.ExecRequest, streams core.IO) (stack.ExecResult, error) {
	args := append(t.ComposeArgs(), "exec")
	if req.Index >= 0 {
		args = append(args, "--index", strconv.Itoa(req.Index+1))
	}
	if !req.TTY {
		args = append(args, "-T")
	}
	args = append(args, req.Service)
	args = append(args, req.Command...)

	res, err := c.stream(ctx, t, args, streams)
	if err != nil {
		return stack.ExecResult{}, err
	}
	return stack.ExecResult{ExitCode: res.ExitCode, Output: summarize(res.Combined())}, nil
}

// stream runs one live command through the optional streaming capability of
// the shared external-command boundary. A start failure is a Docker problem;
// cancellation stays a context error.
func (c *Compose) stream(ctx context.Context, t core.Target, args []string, streams core.IO) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	streamer, ok := c.runner.(execx.Streamer)
	if !ok {
		return execx.Result{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgStreamUnsupported,
			NextActions: []string{msgStreamUnsupportedAction},
		})
	}
	res, err := streamer.RunStream(ctx, execx.Command{
		Name: "docker",
		Args: args,
		Dir:  t.ProjectDir,
	}, execx.Stream{
		Stdin:  streams.Stdin,
		Stdout: streams.Stdout,
		Stderr: streams.Stderr,
		TTY:    streams.TTY,
		Width:  streams.Width,
		Height: streams.Height,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return execx.Result{}, ctxErr
	}
	if err != nil {
		return execx.Result{}, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryDockerMissing,
			Severity:    core.SeverityError,
			Message:     msgDockerNotExecutable,
			Detail:      summarize(err.Error()),
			NextActions: []string{msgDockerMissingActionTwo},
		})
	}
	return res, nil
}
