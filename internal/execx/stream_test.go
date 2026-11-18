package execx_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/rootmyself/dcraft/internal/execx"
)

func TestRunStreamNonTTYPassesInputOutputAndExitStatus(t *testing.T) {
	var stdout, stderr bytes.Buffer
	res, err := execx.NewOSRunner().RunStream(context.Background(), execx.Command{
		Name: "sh",
		Args: []string{"-c", "cat; echo err >&2; exit 3"},
	}, execx.Stream{
		Stdin:  strings.NewReader("from-stdin\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if stdout.String() != "from-stdin\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "err\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(res.Combined(), "err") {
		t.Fatalf("captured output = %q", res.Combined())
	}
}

func TestRunStreamNonTTYDoesNotAllocateATerminal(t *testing.T) {
	var stdout bytes.Buffer
	_, err := execx.NewOSRunner().RunStream(context.Background(), execx.Command{
		Name: "sh",
		Args: []string{"-c", "if [ -t 0 ] || [ -t 1 ]; then echo tty; else echo notty; fi"},
	}, execx.Stream{Stdin: strings.NewReader(""), Stdout: &stdout})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "notty" {
		t.Fatalf("child saw a terminal without TTY: %q", stdout.String())
	}
}

func TestRunStreamTTYAttachesAPseudoTerminalAndForwardsInput(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer master.Close()
	defer slave.Close()

	var stdout bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, runErr := execx.NewOSRunner().RunStream(context.Background(), execx.Command{
			Name: "sh",
			Args: []string{"-c", "if [ -t 0 ] && [ -t 1 ]; then echo tty; fi; IFS= read -r line; echo got:$line"},
		}, execx.Stream{Stdin: slave, Stdout: &stdout, TTY: true})
		done <- runErr
	}()

	if _, err := master.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunStream: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunStream did not finish")
	}
	if !strings.Contains(stdout.String(), "tty") {
		t.Fatalf("child did not see a terminal: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "got:hello") {
		t.Fatalf("stdin was not forwarded: %q", stdout.String())
	}
}

func TestRunStreamRestoresTheCallersTerminalAfterSuccessAndFailure(t *testing.T) {
	cases := []struct {
		name string
		cmd  execx.Command
		code int
	}{
		{name: "success", cmd: execx.Command{Name: "sh", Args: []string{"-c", "printf ok"}}, code: 0},
		{name: "failure", cmd: execx.Command{Name: "sh", Args: []string{"-c", "printf bad >&2; exit 4"}}, code: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatalf("pty: %v", err)
			}
			defer master.Close()
			defer slave.Close()

			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatalf("state before: %v", err)
			}
			var stdout bytes.Buffer
			res, err := execx.NewOSRunner().RunStream(context.Background(), tc.cmd, execx.Stream{
				Stdin: slave, Stdout: &stdout, TTY: true,
			})
			if err != nil {
				t.Fatalf("RunStream: %v", err)
			}
			if res.ExitCode != tc.code {
				t.Fatalf("exit = %d, want %d", res.ExitCode, tc.code)
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatalf("state after: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("the caller terminal was left in raw mode")
			}
		})
	}
}

func TestRunStreamCancellationStopsTheChildAndRestoresTheTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatalf("state before: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var stdout bytes.Buffer
		_, runErr := execx.NewOSRunner().RunStream(ctx, execx.Command{
			Name: "sh",
			Args: []string{"-c", "sleep 30"},
		}, execx.Stream{Stdin: slave, Stdout: &stdout, TTY: true})
		done <- runErr
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not stop the child")
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatalf("state after: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("the caller terminal was left in raw mode after cancellation")
	}
}

func TestRunStreamReportsAStartFailure(t *testing.T) {
	_, err := execx.NewOSRunner().RunStream(context.Background(), execx.Command{
		Name: "dcraft-definitely-not-a-binary",
	}, execx.Stream{Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected a start failure")
	}
}
