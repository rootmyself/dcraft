package e2e

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/features/all"
)

// ptySession runs the dcraft binary under a real pseudo-terminal so terminal
// handling (raw mode, restoration, ctrl+c) is exercised, not simulated.
type ptySession struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	out    *syncBuffer
	before *term.State
}

func startPTY(t *testing.T, dir string, env []string, args ...string) *ptySession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "TERM=xterm-256color")
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	_ = pty.Setsize(master, &pty.Winsize{Rows: 60, Cols: 120})
	before, err := term.GetState(int(master.Fd()))
	if err != nil {
		_ = master.Close()
		t.Fatalf("term state: %v", err)
	}
	out := &syncBuffer{}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, master)
		close(done)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-done
	})
	return &ptySession{t: t, cmd: cmd, master: master, out: out, before: before}
}

func (s *ptySession) waitFor(text string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(s.out.String(), text) {
		if time.Now().After(deadline) {
			_ = s.cmd.Process.Kill()
			s.t.Fatalf("pty output never contained %q:\n%s", text, s.out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *ptySession) send(text string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(text)); err != nil {
		s.t.Fatalf("write pty: %v", err)
	}
}

// press writes keys one at a time so Bubble Tea receives separate key events
// instead of one coalesced read.
func (s *ptySession) press(keys ...string) {
	s.t.Helper()
	for _, key := range keys {
		s.send(key)
		time.Sleep(120 * time.Millisecond)
	}
}

func (s *ptySession) wait() int {
	s.t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		s.t.Fatalf("wait: %v", err)
		return -1
	case <-time.After(30 * time.Second):
		_ = s.cmd.Process.Kill()
		s.t.Fatalf("process did not exit:\n%s", s.out.String())
		return -1
	}
}

// assertRestored proves the terminal is not left in raw mode after a normal
// exit or a failure.
// menuDownPresses returns how many down-arrow presses move the menu cursor
// from the primary status feature to the named feature. The menu order is the
// registration order, so this stays correct when another ticket adds a feature.
func menuDownPresses(t *testing.T, name string) int {
	t.Helper()
	reg := core.NewRegistry()
	all.Register(reg, &deps.Deps{
		Getenv:     func(string) string { return "" },
		Runner:     &execxtest.Runner{},
		Downloader: &noopDownloader{},
		Installer:  &noopInstaller{},
	})
	features := reg.All()
	statusIdx, targetIdx := -1, -1
	for i, f := range features {
		switch f.Descriptor().Name {
		case "status":
			statusIdx = i
		case name:
			targetIdx = i
		}
	}
	if statusIdx < 0 || targetIdx < 0 {
		t.Fatalf("features = %v (status=%d target=%d)", features, statusIdx, targetIdx)
	}
	return (targetIdx - statusIdx + len(features)) % len(features)
}

func (s *ptySession) pressMenuDown(count int) {
	s.t.Helper()
	keys := make([]string, 0, count+1)
	for i := 0; i < count; i++ {
		keys = append(keys, "\x1b[B")
	}
	keys = append(keys, "\r")
	s.press(keys...)
}

func (s *ptySession) assertRestored() {
	s.t.Helper()
	after, err := term.GetState(int(s.master.Fd()))
	if err != nil {
		s.t.Fatalf("term state after: %v", err)
	}
	if !reflect.DeepEqual(s.before, after) {
		s.t.Fatal("terminal was left in raw mode")
	}
}

func TestPTYLogsFollowCtrlCReturnsWithExit130AndRestoresTerminal(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_LOG_FOLLOW_LINE": "followed line"})
	s := startPTY(t, project, env, "logs", "-s", "web")
	s.waitFor("followed line", 15*time.Second)
	s.send("\x03")
	code := s.wait()
	if code != 130 {
		t.Fatalf("exit = %d, want 130\n%s", code, s.out.String())
	}
	if !strings.Contains(s.out.String(), "계속 실행") {
		t.Fatalf("cancel guidance missing:\n%s", s.out.String())
	}
	s.assertRestored()
	fake.assertNoSubcommand(t, "stop")
	fake.assertNoSubcommand(t, "down")
}

func TestPTYExecAttachesInputAndRestoresTerminalOnSuccess(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_EXEC_ECHO": "1"})
	s := startPTY(t, project, env, "exec", "-s", "db", "--", "read-line")
	s.waitFor("ready", 15*time.Second)
	s.send("hello\n")
	s.waitFor("got:hello", 15*time.Second)
	code := s.wait()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, s.out.String())
	}
	s.assertRestored()
}

func TestPTYExecFailureRestoresTerminalAndReportsTheRemoteExit(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{
		"DOCKER_FAKE_EXEC_ECHO": "1",
		"DOCKER_FAKE_EXEC_EXIT": "7",
	})
	s := startPTY(t, project, env, "exec", "-s", "db", "--", "read-line")
	s.waitFor("ready", 15*time.Second)
	s.send("hello\n")
	s.waitFor("got:hello", 15*time.Second)
	code := s.wait()
	if code != 1 {
		t.Fatalf("dcraft exit = %d, want 1\n%s", code, s.out.String())
	}
	if !strings.Contains(s.out.String(), "종료 코드 7") {
		t.Fatalf("remote exit code missing:\n%s", s.out.String())
	}
	s.assertRestored()
}

func TestPTYTUILogsScreenReturnsToTheMenuAndKeepsServices(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_LOG_FOLLOW_LINE": "followed line"})
	s := startPTY(t, project, env)
	s.waitFor("로그 보기 (logs)", 15*time.Second)
	s.pressMenuDown(menuDownPresses(t, "logs"))
	s.waitFor("followed line", 15*time.Second)
	s.send("q")
	s.waitFor("마지막 실시간 출력", 15*time.Second)
	s.send("q")
	code := s.wait()
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
	fake.assertNoSubcommand(t, "stop")
	fake.assertNoSubcommand(t, "down")
}

func TestPTYTUIExecScreenShowsTargetRunsAndReturnsToTheMenu(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_EXEC_STDOUT": "remote-stdout"})
	s := startPTY(t, project, env)
	s.waitFor("로그 보기 (logs)", 15*time.Second)
	s.pressMenuDown(menuDownPresses(t, "exec"))
	s.waitFor("실행 전 확인", 15*time.Second)
	if !strings.Contains(s.out.String(), "서비스 인스턴스") || !strings.Contains(s.out.String(), "명령") {
		t.Fatalf("exec form does not show service/instance/command:\n%s", s.out.String())
	}
	s.press("\r") // choose the default instance, move to command
	s.press("d", "a", "t", "e")
	s.press("\r") // submit
	s.waitFor("명령을 실행했습니다", 20*time.Second)
	s.send("q")
	code := s.wait()
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
	if !strings.Contains(s.out.String(), "remote-stdout") {
		t.Fatalf("exec output missing:\n%s", s.out.String())
	}
	fake.assertNoSubcommand(t, "stop")
	fake.assertNoSubcommand(t, "down")
}
