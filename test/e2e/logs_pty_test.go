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
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
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
	return startPTYWithTerm(t, dir, env, "xterm-256color", args...)
}

// startPTYWithTerm is startPTY with an explicit TERM, so a test can exercise the
// color-profile decision on a real terminal.
func startPTYWithTerm(t *testing.T, dir string, env []string, termName string, args ...string) *ptySession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "TERM="+termName)
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
	for !screenContains(s.out.String(), text) {
		if time.Now().After(deadline) {
			_ = s.cmd.Process.Kill()
			s.t.Fatalf("pty output never contained %q:\n%s", text, s.out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForMenu waits until the menu is actually ready, not merely drawn. The
// footer shows "[↑↓] 이동" while a status run is still streaming too, and an
// arrow pressed then scrolls the running result instead of moving the cursor.
// Waiting for the refresh chip proves the run finished and the menu owns the
// keyboard, so the presses that follow all count.
func (s *ptySession) waitForMenu(timeout time.Duration) {
	s.t.Helper()
	s.waitFor("[r] 새로고침", timeout)
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

// navOrder returns the real menu order the TUI renders: section order, then
// registration order inside each section. It is derived from the registry so
// it stays correct when a feature is added.
func navOrder(t *testing.T) []core.Feature {
	t.Helper()
	reg := core.NewRegistry()
	all.Register(reg, &deps.Deps{
		Getenv:     func(string) string { return "" },
		Runner:     &execxtest.Runner{},
		Downloader: &noopDownloader{},
		Installer:  &noopInstaller{},
	})
	// The navigation is the menu tree, not one row per feature: a feature may
	// declare two entries and a heading is not selectable. The TUI also hides an
	// entry the runtime cannot serve, so the count here uses the same rule: the
	// environment switch row needs a resolver that can record the active alias,
	// and this fixture's Deps has none.
	var out []core.Feature
	include := func(entry core.MenuEntry) bool { return entry.Kind != core.MenuSwitchEnvironment }
	for _, section := range reg.MenuBySection(include).Sections {
		for _, entry := range section.Entries {
			if entry.Group {
				for _, child := range entry.Children {
					out = append(out, child.Feature)
				}
				continue
			}
			out = append(out, entry.Feature)
		}
	}
	return out
}

// menuDownPresses returns how many down-arrow presses move the menu cursor
// from the primary status feature to the named feature. The menu is grouped by
// descriptor section, so this walks the same order the screen draws.
func menuDownPresses(t *testing.T, name string) int {
	t.Helper()
	features := navOrder(t)
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

// openMenu filters the menu to one entry and runs it. It is the deterministic
// way to reach an entry from an unknown cursor position: counting arrow presses
// depends on how many arrived while the opening status run still owned the
// keyboard, and a lost press lands the cursor somewhere else.
//
// The filter is proven by the menu title, not by a cursor marker: the sidebar
// draws that marker for the selected entry anyway, so a stale frame could match
// before the filter even started. `메뉴 · 1` is only true when exactly one entry
// survives, and Enter on it commits the filter; the second Enter runs it.
func (s *ptySession) openMenu(label string) {
	s.t.Helper()
	s.walkToEntry(label)
	s.press("\r") // run the entry the cursor now marks
}

// walkToEntry moves the menu cursor with the down arrow until the drawn frame
// marks the wanted entry, then stops. Counting presses is not enough: a press
// that arrives while the opening status run still owns the keyboard scrolls the
// result instead of moving the cursor, and the count then lands elsewhere. This
// reads the frame that is actually on screen, so a lost press costs one more
// press rather than a wrong entry.
func (s *ptySession) walkToEntry(label string) {
	s.t.Helper()
	marker := "▸ " + label
	seen := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(currentFrame(s.out.String()), marker) {
			seen = true
			// The frame the test just read is the one the Enter acts on.
			time.Sleep(50 * time.Millisecond)
			return
		}
		s.press("\x1b[B")
	}
	if !seen {
		s.t.Fatalf("menu never marked %q:\n%s", label, s.out.String())
	}
}

// currentFrame is the text of the last drawn frame. The TUI paints each frame
// from the home position, so the final chunk is what the reader sees; the
// accumulated buffer also holds every earlier frame, which would let a stale
// marker satisfy a check.
func currentFrame(output string) string {
	frames := strings.Split(output, "\x1b[H")
	return visibleText(frames[len(frames)-1])
}

// menuLabel is the menu entry label a feature's first entry declares, read from
// the same registry the binary assembles. The e2e opens entries by this label
// instead of counting presses.
func menuLabel(t *testing.T, name string) string {
	t.Helper()
	for _, f := range navOrder(t) {
		if f.Descriptor().Name == name {
			for _, entry := range f.Descriptor().Menu {
				if entry.Kind != core.MenuGroup {
					return entry.Label
				}
			}
			return f.Descriptor().Title
		}
	}
	t.Fatalf("feature %q is not in the menu", name)
	return ""
}

func (s *ptySession) openMenuFeature(t *testing.T, name string) {
	t.Helper()
	s.openMenu(menuLabel(t, name))
}

// assertRestored proves the terminal is not left in raw mode after a normal
// exit or a failure.
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
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "logs")
	s.waitFor("서비스 선택", 15*time.Second)
	// The screen requires a named service: pick the first row, then confirm.
	s.press(" ")
	s.press("\r")
	s.waitFor("서비스 로그", 15*time.Second)
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
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "exec")
	s.waitFor("실행 전 확인", 15*time.Second)
	if !strings.Contains(s.out.String(), "서비스 인스턴스") || !strings.Contains(s.out.String(), "명령") {
		t.Fatalf("exec form does not show service/instance/command:\n%s", s.out.String())
	}
	s.press("\r") // choose the default instance, move to the command choice
	// The choice lists the pinned shells, then this project's recent commands,
	// then the new-command row. Up from the first row wraps to the last row, so
	// the new-command row is reachable without counting the stored history.
	s.press("\x1b[A")
	s.press("\r") // move to the typed command
	s.press("s", "h")
	s.press("\r") // submit
	s.waitFor("명령 실행", 20*time.Second)
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

// menuDown is the raw key sequence that moves the cursor from the primary
// status feature to the named feature through the sectioned menu.
func menuDown(t *testing.T, name string) string {
	t.Helper()
	return strings.Repeat("\x1b[B", menuDownPresses(t, name))
}

// The live log screen scrolls under a header that stays put, and detaching
// restores the terminal. The position row is real output of the real binary, not
// a unit-test fixture.
func TestPTYTUILogsScreenScrollsUnderAPinnedHeaderAndRestoresTerminal(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_LOG_FOLLOW_LINE": "followed line"})
	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "logs")
	s.waitFor("서비스 선택", 15*time.Second)
	// The services are listed in name order, so the first row is db.
	s.press(" ")
	s.press("\r")
	s.waitFor("실시간 로그", 15*time.Second)
	s.waitFor("followed line", 15*time.Second)
	// The pinned header names the selected service while the band scrolls.
	for _, want := range []string{"서비스: db", "인스턴스: 전체", "명령: logs"} {
		s.waitFor(want, 15*time.Second)
	}
	// The faked stream is short, so the position row may be absent; scrolling
	// keys must still be harmless and must not detach the screen.
	s.press("g", "G")
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

// A long stream exercises the real viewport: PgUp moves the visible rows while
// the header rows above them stay unchanged, and ctrl+] ends an attached command
// with the terminal restored.
func TestPTYTUIExecAttachDetachesWithCtrlBracketAndRestoresTerminal(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_EXEC_ECHO": "1"})
	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "exec")
	s.waitFor("실행 전 확인", 15*time.Second)
	s.press("\r")     // default instance, move to the command choice
	s.press("\x1b[A") // wrap to the new-command row
	s.press("\r")     // move to the typed command
	s.press("r", "e", "a", "d", "-", "l", "i", "n", "e")
	s.press("\r")
	s.waitFor("ready", 20*time.Second)
	// Forwarding is proven by the child reading what the TUI relayed.
	s.send("hello\n")
	s.waitFor("got:hello", 20*time.Second)
	// ctrl+] detaches instead of reaching the child a second time.
	s.send("\x1d")
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

// A log line that carries a tab, a carriage return and a cursor move must not
// push a row past the sidebar column. The grid consumes those sequences as
// terminal state, so the frame keeps its width and the sidebar its column.
//
// The child's bytes no longer reach the terminal, which is the point: the escape
// that used to overwrite the sidebar now only moves the grid's own cursor. The
// visible result of a rewrite is whatever a terminal would show, not a cleaned-up
// join of the raw segments.
func TestPTYTUILiveLogHostileLineKeepsTheFrame(t *testing.T) {
	fake := newFakeLiveDocker(t)
	project := liveProject(t)
	env := fake.env(t, t.TempDir(), map[string]string{"DOCKER_FAKE_LOG_HOSTILE": "1"})
	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.openMenuFeature(t, "logs")
	s.waitFor("서비스 선택", 15*time.Second)
	s.press("\r")
	s.waitFor("실시간 로그", 15*time.Second)
	s.waitFor("done", 20*time.Second)
	tail := s.out.String()
	// The hostile sequences are the grid's, never the real terminal's: the OSC
	// title must not reach the terminal as a title.
	if strings.Contains(tail, "\x1b]0;") {
		t.Fatalf("an OSC title reached the terminal:\n%q", tail)
	}
	// The frame keeps its width, so no row can cross the sidebar column.
	if bad := overWideRows(tail, 120); len(bad) > 0 {
		t.Fatalf("a log row is wider than the terminal: %q", bad[0])
	}
	// The cursor move stayed inside the grid: the sidebar border is intact on
	// every body row.
	rows := lastFrame(tail)
	border := visual.SidebarWidth(120) - 1
	for i, row := range rows {
		if i == 0 || i == len(rows)-1 {
			continue
		}
		if strings.Contains(row, "│") && len(row) > border && !strings.Contains(row[:border+1], "│") {
			t.Fatalf("row %d lost the sidebar border: %q", i, row)
		}
	}
	s.send("q")
	s.waitFor("마지막 실시간 출력", 15*time.Second)
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, tail)
	}
	s.assertRestored()
	fake.assertNoSubcommand(t, "stop")
	fake.assertNoSubcommand(t, "down")
}

// waitFrameFor waits for text in the frame that is currently drawn, not in any
// frame the run has produced. The accumulated buffer would match a screen that
// has already been replaced, which lets the next key land on the wrong screen.
func (s *ptySession) waitFrameFor(t *testing.T, text string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(currentFrame(s.out.String()), text) {
		if time.Now().After(deadline) {
			_ = s.cmd.Process.Kill()
			t.Fatalf("current frame never contained %q:\n%s", text, s.out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
