package compose

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

// upProgressRunner answers the probe and config calls like secretRunner but
// serves `up` through RunStream so the live writer is exercised. captured is
// the output the streamed run would also return as its diagnostics.
func upProgressRunner(stream func(execx.Stream), captured ...string) *execxtest.Runner {
	return &execxtest.Runner{
		Handler: func(cmd execx.Command) (execx.Result, error) {
			line := strings.Join(cmd.Args, " ")
			switch {
			case strings.Contains(line, " config --format json"):
				return execx.Result{Stdout: []byte(`{"name":"dcraft-test-secrets","services":{"web":{"image":"busybox"}}}`)}, nil
			default:
				return execx.Result{Stderr: []byte("unexpected " + line), ExitCode: 9}, nil
			}
		},
		StreamHandler: func(cmd execx.Command, s execx.Stream) (execx.Result, error) {
			stream(s)
			return execx.Result{Stdout: []byte(strings.Join(captured, ""))}, nil
		},
	}
}

// A live line is user-visible text, so it must pass the same redaction the
// captured result does. A value split across writes must not survive either.
func TestUpProgressRedactsKnownValueFromStreamedOutput(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
`)
	const sentinel = "sentinel-stream-output-3311"
	runner := upProgressRunner(func(s execx.Stream) {
		_, _ = s.Stdout.Write([]byte("web  | container printed sent"))
		_, _ = s.Stdout.Write([]byte("inel-stream-output-3311\n"))
		_, _ = s.Stderr.Write([]byte("error: sentinel-stream-output-3311\n"))
	})
	c, _ := secretCompose(t, target.ProjectDir, runner, map[string]string{"WEB_SECRET": sentinel})

	var progress bytes.Buffer
	if _, err := c.UpProgress(context.Background(), target, nil, &progress); err != nil {
		t.Fatalf("UpProgress: %v", err)
	}
	if strings.Contains(progress.String(), sentinel) {
		t.Fatalf("live output leaked the value: %q", progress.String())
	}
	if !strings.Contains(progress.String(), "[redacted]") {
		t.Fatalf("live output was not redacted: %q", progress.String())
	}
	if got := strings.Count(progress.String(), "\n"); got != 2 {
		t.Fatalf("live output = %q, want both lines", progress.String())
	}
}

// The captured result and the live view are two views of the same bytes, so
// neither may carry a referenced value.
func TestUpProgressKeepsCapturedAndLiveRedactionInStep(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
`)
	const sentinel = "sentinel-both-views-6677"
	runner := upProgressRunner(func(s execx.Stream) {
		_, _ = s.Stdout.Write([]byte("up said " + sentinel + "\n"))
	}, "up said "+sentinel+"\n")
	c, _ := secretCompose(t, target.ProjectDir, runner, map[string]string{"WEB_SECRET": sentinel})

	var progress bytes.Buffer
	res, err := c.UpProgress(context.Background(), target, nil, &progress)
	if err != nil {
		t.Fatalf("UpProgress: %v", err)
	}
	if strings.Contains(res.Output, sentinel) {
		t.Fatalf("captured output leaked the value: %q", res.Output)
	}
	if strings.Contains(progress.String(), sentinel) {
		t.Fatalf("live output leaked the value: %q", progress.String())
	}
}

// The final line has no newline, so the writer must flush it when the command
// returns instead of dropping it or leaving it for a later run.
func TestUpProgressFlushesTheUnterminatedLastLine(t *testing.T) {
	_, target := secretProject(t, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: WEB_SECRET
`)
	const sentinel = "sentinel-final-line-5522"
	runner := upProgressRunner(func(s execx.Stream) {
		_, _ = s.Stdout.Write([]byte("done " + sentinel))
	})
	c, _ := secretCompose(t, target.ProjectDir, runner, map[string]string{"WEB_SECRET": sentinel})

	var progress bytes.Buffer
	if _, err := c.UpProgress(context.Background(), target, nil, &progress); err != nil {
		t.Fatalf("UpProgress: %v", err)
	}
	if got := progress.String(); got != "done [redacted]" {
		t.Fatalf("live output = %q, want the flushed last line", got)
	}
}
