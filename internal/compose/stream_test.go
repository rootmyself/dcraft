package compose

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/stack"
)

func streamTarget() core.Target {
	return core.Target{
		ProjectDir:  "/p",
		Files:       []string{"/p/compose.yaml"},
		ProjectName: "hello-world",
	}
}

func TestLogsMapsFollowTailSinceTimestampsIndexAndColor(t *testing.T) {
	runner := streamsWriting("web-1 | line one\nweb-2 | line two\n")
	out := &bytes.Buffer{}
	err := New(runner, nil).Logs(context.Background(), streamTarget(), stack.LogsRequest{
		Services:   []string{"web"},
		Index:      1,
		Follow:     true,
		Tail:       "20",
		Since:      "10m",
		Timestamps: true,
		Color:      false,
	}, core.IO{Stdout: out})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	want := "docker compose -f /p/compose.yaml -p hello-world logs --follow --no-color --tail 20 --since 10m --timestamps --index 2 web"
	calls := runner.CallLines()
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %v\nwant  %s", calls, want)
	}
	if !strings.Contains(out.String(), "web-1 | line one") || !strings.Contains(out.String(), "web-2 | line two") {
		t.Fatalf("live output = %q", out.String())
	}
}

func TestLogsDefaultsToNoFollowWithoutExtraFlags(t *testing.T) {
	runner := streamsWriting("all services\n")
	err := New(runner, nil).Logs(context.Background(), streamTarget(), stack.LogsRequest{
		Index: -1,
	}, core.IO{Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	want := "docker compose -f /p/compose.yaml -p hello-world logs --no-color"
	if calls := runner.CallLines(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("call = %v, want %q", calls, want)
	}
}

func TestLogsNeverAllocatesATTY(t *testing.T) {
	var gotTTY bool
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		gotTTY = s.TTY
		return execx.Result{}, nil
	}}
	err := New(runner, nil).Logs(context.Background(), streamTarget(), stack.LogsRequest{
		Index: -1, Color: true,
	}, core.IO{Stdout: &bytes.Buffer{}, TTY: true})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if gotTTY {
		t.Fatal("logs allocated a terminal for an output-only stream")
	}
}

func TestLogsReturnsACommandErrorWithBoundedOutput(t *testing.T) {
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: []byte("no such service: cache")}, nil
	}}
	err := New(runner, nil).Logs(context.Background(), streamTarget(), stack.LogsRequest{
		Services: []string{"web"}, Index: -1,
	}, core.IO{Stdout: &bytes.Buffer{}})
	var cmdErr *stack.CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("error = %v, want CommandError", err)
	}
	if cmdErr.ExitCode != 1 || !strings.Contains(cmdErr.Output, "no such service") {
		t.Fatalf("command error = %+v", cmdErr)
	}
	if cmdErr.Action != "logs" {
		t.Fatalf("action = %q", cmdErr.Action)
	}
}

func TestLogsCancellationIsAContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		cancel()
		return execx.Result{ExitCode: 130}, nil
	}}
	err := New(runner, nil).Logs(ctx, streamTarget(), stack.LogsRequest{Index: -1}, core.IO{Stdout: &bytes.Buffer{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestExecPassesTheExactArgvWithoutAShell(t *testing.T) {
	runner := streamsWriting("")
	out := &bytes.Buffer{}
	res, err := New(runner, nil).Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web",
		Index:   -1,
		Command: []string{"printf", "%s", "hello world"},
	}, core.IO{Stdout: out})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	want := "docker compose -f /p/compose.yaml -p hello-world exec -T web printf %s hello world"
	if calls := runner.CallLines(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("call = %v\nwant  %q", calls, want)
	}
	for _, forbidden := range []string{" sh ", " -c ", "-lc"} {
		if strings.Contains(" "+runner.CallLines()[0]+" ", forbidden) {
			t.Fatalf("exec inserted a shell: %s", runner.CallLines()[0])
		}
	}
}

func TestExecMapsTTYAndZeroBasedIndex(t *testing.T) {
	runner := streamsWriting("")
	c := New(runner, nil)
	if _, err := c.Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web", Index: 0, Command: []string{"true"}, TTY: true,
	}, core.IO{Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Exec tty: %v", err)
	}
	if _, err := c.Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web", Index: 2, Command: []string{"true"}, TTY: false,
	}, core.IO{Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Exec no tty: %v", err)
	}
	calls := runner.CallLines()
	if want := "docker compose -f /p/compose.yaml -p hello-world exec --index 1 web true"; calls[0] != want {
		t.Fatalf("tty call = %q, want %q", calls[0], want)
	}
	if want := "docker compose -f /p/compose.yaml -p hello-world exec --index 3 -T web true"; calls[1] != want {
		t.Fatalf("no-tty call = %q, want %q", calls[1], want)
	}
}

func TestExecReturnsANonZeroExitAsAResult(t *testing.T) {
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		return execx.Result{ExitCode: 7, Stderr: []byte("remote failed")}, nil
	}}
	res, err := New(runner, nil).Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web", Index: -1, Command: []string{"false"},
	}, core.IO{Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Exec returned a boundary error for a normal non-zero exit: %v", err)
	}
	if res.ExitCode != 7 || !strings.Contains(res.Output, "remote failed") {
		t.Fatalf("result = %+v", res)
	}
}

func TestExecStartFailureIsADockerDiagnostic(t *testing.T) {
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		return execx.Result{}, errors.New("exec: docker: not found")
	}}
	_, err := New(runner, nil).Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web", Index: -1, Command: []string{"true"},
	}, core.IO{Stdout: &bytes.Buffer{}})
	var ce *core.Error
	if !errors.As(err, &ce) || len(ce.Diagnostics) == 0 || ce.Diagnostics[0].Category != core.CategoryDockerMissing {
		t.Fatalf("error = %v", err)
	}
}

func TestExecPassesTheCallersStreamsToTheRunner(t *testing.T) {
	stdin := strings.NewReader("input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	var got execx.Stream
	runner := &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		got = s
		return execx.Result{}, nil
	}}
	_, err := New(runner, nil).Exec(context.Background(), streamTarget(), stack.ExecRequest{
		Service: "web", Index: -1, Command: []string{"cat"}, TTY: true,
	}, core.IO{Stdin: stdin, Stdout: stdout, Stderr: stderr, TTY: true, Width: 100, Height: 40})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.Stdin != stdin || got.Stdout != stdout || got.Stderr != stderr || !got.TTY || got.Width != 100 || got.Height != 40 {
		t.Fatalf("stream = %+v", got)
	}
}

func streamsWriting(text string) *execxtest.Runner {
	return &execxtest.Runner{StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
		if s.Stdout != nil {
			_, _ = s.Stdout.Write([]byte(text))
		}
		return execx.Result{}, nil
	}}
}
