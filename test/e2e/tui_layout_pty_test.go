package e2e

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

// The real-PTY scenarios for the redesigned screen. They run the real binary on
// a real pseudo-terminal, so SIGWINCH, alt-screen redraw and raw-mode
// restoration are exercised by the terminal itself, not simulated.

// frameHome is the sequence Bubble Tea's alt-screen renderer emits at the start
// of every repaint. Splitting the raw capture on it is the only reliable frame
// boundary: the renderer erases to end-of-line and jumps home instead of
// emitting a newline, so consecutive frames can share a captured line.
const frameHome = "\x1b[H"

// cursorMove matches the cursor placement the renderer appends after the last
// row of a repaint. It is not content, so a row carrying only this sequence is
// one the renderer skipped.
var cursorMove = regexp.MustCompile("\x1b\\[[0-9;]*[Hf]")

// lastFrame returns the visible screen as the terminal would show it.
//
// Every repaint starts at home, but the renderer only writes the rows whose
// content changed since the previous flush: an unchanged row arrives as an
// empty line (the cursor just moves down). Animation such as the sidebar
// marquee therefore emits partial repaints between the full frame that follows
// a resize, so reading only the last chunk would see just the animated row.
// Replaying every repaint in order and keeping the previous row for an empty
// line reproduces the actual screen.
func lastFrame(screen string) []string {
	var rows []string
	for _, chunk := range strings.Split(screen, frameHome) {
		if strings.Contains(chunk, "\x1b[2J") {
			rows = nil
		}
		lines := strings.Split(chunk, "\n")
		for i, line := range lines {
			line = cursorMove.ReplaceAllString(strings.TrimSuffix(line, "\r"), "")
			line = strings.ReplaceAll(line, "\x1b[J", "")
			if line == "" {
				// A skipped row: the terminal keeps what is already there.
				continue
			}
			for len(rows) <= i {
				rows = append(rows, "")
			}
			rows[i] = line
		}
		if strings.Contains(chunk, "\x1b[J") && len(rows) > len(lines) {
			// Erase below: the screen got shorter, so the rows past the new
			// frame are gone instead of stale content from a taller one.
			rows = rows[:len(lines)]
		}
	}
	for i, row := range rows {
		rows[i] = visibleText(row)
	}
	for len(rows) > 0 && strings.TrimRight(rows[len(rows)-1], " ") == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// sgrOnly matches a Select Graphic Rendition sequence: color and attributes.
// The alt-screen renderer must still send cursor movement (and a terminal may
// be queried with a CSI response request), so a colorless assertion has to be
// about SGR, not about every escape sequence.
var sgrOnly = regexp.MustCompile("\x1b\\[[0-9;]*m")

// sixteenSGR matches an SGR sequence carrying at least one of the 16 ANSI
// colors (30-37, 90-97, 40-47, 100-107).
var sixteenSGR = regexp.MustCompile("\x1b\\[[0-9;]*(3[0-7]|9[0-7]|4[0-7]|10[0-7])[0-9;]*m")

// overWideRows returns every non-empty row of the last painted frame that is
// wider than width.
func overWideRows(screen string, width int) []string {
	var out []string
	for _, line := range lastFrame(screen) {
		line = strings.TrimRight(line, " ")
		if line == "" {
			continue
		}
		if n := ansi.StringWidth(line); n > width {
			out = append(out, line)
		}
	}
	return out
}

// A partial repaint between full frames must not erase the rest of the screen:
// the renderer emits only the changed rows, so replaying the capture has to
// keep the rows it skipped. Before this helper replayed every chunk, an
// animated sidebar row landing last hid the header from the assertions.
func TestLastFrameAppliesPartialRepaints(t *testing.T) {
	full := frameHome + "header\r\nbody a\r\nbody b\r\nfooter"
	partial := frameHome + "\n\nbody B\n\n\x1b[4;H"
	if got, want := strings.Join(lastFrame(full+partial), "|"), "header|body a|body B|footer"; got != want {
		t.Fatalf("replayed screen = %q, want %q", got, want)
	}

	// The cursor placement after the last row is not content: a partial repaint
	// that skipped the footer must keep it.
	skippedFooter := frameHome + "\n\n\n\x1b[4;H"
	if got, want := strings.Join(lastFrame(full+skippedFooter), "|"), "header|body a|body b|footer"; got != want {
		t.Fatalf("skipped footer = %q, want %q", got, want)
	}

	// A padded blank row the renderer did write is content, not a skip, so it
	// replaces what the previous frame had in that row.
	short := frameHome + "header\nbody\nfooter"
	cleared := frameHome + "   \nbody\nfooter"
	if got, want := strings.Join(lastFrame(short+cleared), "|"), "   |body|footer"; got != want {
		t.Fatalf("written blank row = %q, want %q", got, want)
	}
}

// resizing the terminal must reflow the frame immediately: the header comes
// back, the shape changes at the 100-column breakpoint and no row is ever wider
// than the new terminal.
func TestPTYTUIResizeReflowsAndKeepsTheFrame(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.waitFor("스택 상태", 15*time.Second)

	// Wide: the persistent sidebar is drawn.
	if !screenContains(s.out.String(), "╭ 메뉴") {
		t.Fatalf("wide layout has no sidebar:\n%s", visibleText(s.out.String()))
	}

	for _, size := range []struct{ cols, rows int }{{80, 24}, {40, 18}, {200, 50}, {120, 30}} {
		if err := pty.Setsize(s.master, &pty.Winsize{Rows: uint16(size.rows), Cols: uint16(size.cols)}); err != nil {
			t.Fatalf("Setsize %dx%d: %v", size.cols, size.rows, err)
		}
		// SIGWINCH is delivered by the kernel to the foreground process group;
		// give the renderer one frame to repaint, then assert on that frame.
		time.Sleep(700 * time.Millisecond)
		frame := strings.Join(lastFrame(s.out.String()), "\n")
		if !strings.Contains(frame, "dcraft") {
			t.Fatalf("%dx%d: header disappeared after resize:\n%s", size.cols, size.rows, frame)
		}
		if !strings.Contains(frame, "[") {
			t.Fatalf("%dx%d: footer disappeared after resize:\n%s", size.cols, size.rows, frame)
		}
		if bad := overWideRows(s.out.String(), size.cols); len(bad) > 0 {
			t.Fatalf("%dx%d: %d row(s) wider than the terminal, first: %q", size.cols, size.rows, len(bad), bad[0])
		}
		// The breakpoint is a layout switch, not a resize artefact: below 100
		// columns the sidebar is gone and the one-row navigation is drawn.
		wantSidebar := size.cols >= 100
		if got := strings.Contains(frame, "╭ 메뉴"); got != wantSidebar {
			t.Fatalf("%dx%d: sidebar present = %v, want %v:\n%s", size.cols, size.rows, got, wantSidebar, frame)
		}
	}

	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
}

// Returning to a screen restores its state: the menu cursor stays on the
// feature that was run, and the last result is still the body.
func TestPTYTUIReturningToAScreenKeepsTheCursorAndResult(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.waitFor("스택 상태", 15*time.Second)

	// Walk to stop and open the scope table, then leave it with esc.
	s.openMenuFeature(t, "stop")
	s.waitFor("서비스 선택", 15*time.Second)
	s.send("\x1b")
	time.Sleep(400 * time.Millisecond)

	tail := s.out.String()
	if !screenContains(tail, "▸ 스택 중지") {
		t.Fatalf("the cursor did not stay on the feature that was left:\n%s", visibleText(tail))
	}
	if !screenContains(tail, "스택 상태") {
		t.Fatalf("the last result was lost on return:\n%s", visibleText(tail))
	}
	if screenContains(tail[safeIndex(tail, "서비스 선택"):], "선택 서비스") {
		t.Fatalf("the scope table is still drawn after esc")
	}

	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
	assertNoMutatingCalls(t, fake.calls(t))
}

func safeIndex(s, sub string) int {
	if i := strings.LastIndex(s, sub); i >= 0 {
		return i
	}
	return 0
}

// NO_COLOR: the same screens render with no escape sequence at all. The alt
// screen still needs cursor movement, so the assertion is scoped to SGR
// (color and attribute) sequences, which are what the contract forbids.
func TestPTYTUINoColorStillRendersStructure(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := append(fake.env(t, t.TempDir()), "NO_COLOR=1")

	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	s.waitFor("스택 상태", 15*time.Second)

	tail := s.out.String()
	if sgr := sgrOnly.FindAllString(tail, -1); len(sgr) > 0 {
		t.Fatalf("NO_COLOR run emitted %d SGR sequence(s), first %q:\n%s", len(sgr), sgr[0], visibleText(tail))
	}
	// Structure survives without paint: header, sidebar section, badges with
	// icons and labels, keycaps.
	for _, want := range []string{"dcraft", "조회", "실행 중", "서비스", "[enter] 실행"} {
		if !screenContains(tail, want) {
			t.Fatalf("NO_COLOR frame missing %q:\n%s", want, visibleText(tail))
		}
	}

	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, s.out.String())
	}
	s.assertRestored()
}

// Each color profile renders the same text. This is the profile matrix the
// contract asks for: 256-color and 16-color terminals must still show the
// structure, and the color set is decided by the theme function, not the
// renderer.
func TestPTYTUIRendersPerColorProfile(t *testing.T) {
	cases := []struct {
		name  string
		term  string
		color bool
		// sixteen marks a terminal that must receive the nearest 16 colors,
		// never a 256-color or truecolor sequence.
		sixteen bool
	}{
		{name: "truecolor", term: "xterm-256color", color: true},
		{name: "256", term: "xterm-256color", color: true},
		{name: "16color", term: "xterm", color: true, sixteen: true},
		{name: "dumb", term: "dumb", color: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeDocker(t)
			project := t.TempDir()
			writeProject(t, project)
			env := fake.env(t, t.TempDir())

			// startPTY appends TERM=xterm-256color; build the session through
			// the same helper and then override TERM by starting the process
			// with the profile's TERM already in the environment.
			s := startPTYWithTerm(t, project, env, tc.term)
			s.waitFor("마지막 조회", 15*time.Second)
			s.waitFor("스택 상태", 15*time.Second)

			tail := s.out.String()
			cols := 120
			if bad := overWideRows(tail, cols); len(bad) > 0 {
				t.Fatalf("%s: row wider than the terminal: %q", tc.name, bad[0])
			}
			for _, want := range []string{"dcraft", "스택 상태", "[q] 종료"} {
				if !screenContains(tail, want) {
					t.Fatalf("%s: frame missing %q:\n%s", tc.name, want, visibleText(tail))
				}
			}
			if tc.sixteen {
				for _, bad := range []string{"38;2", "38;5", "48;2", "48;5"} {
					if strings.Contains(tail, bad) {
						t.Fatalf("%s: 16-color terminal received %q:\n%q", tc.name, bad, tail)
					}
				}
				if !sixteenSGR.MatchString(tail) {
					t.Fatalf("%s: no 16-color sequence:\n%q", tc.name, tail)
				}
			} else if tc.color {
				if !strings.Contains(tail, ";38;2;") && !strings.Contains(tail, ";38;5;") {
					t.Fatalf("%s: no color sequence in a color-capable terminal:\n%q", tc.name, tail)
				}
			}

			s.send("q")
			if code := s.wait(); code != 0 {
				t.Fatalf("%s: TUI exit = %d", tc.name, code)
			}
			s.assertRestored()
		})
	}
}

// A real run leaves raw mode restored even when the frame is redrawn many
// times: the frame is asserted first, then the terminal state.
func TestPTYTUIRawModeIsRestoredAfterSeveralResizes(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	s := startPTY(t, project, env)
	s.waitFor("마지막 조회", 15*time.Second)
	for i := 0; i < 3; i++ {
		if err := pty.Setsize(s.master, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
			t.Fatalf("Setsize: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		if err := pty.Setsize(s.master, &pty.Winsize{Rows: 50, Cols: 160}); err != nil {
			t.Fatalf("Setsize: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !screenContains(s.out.String(), "dcraft") {
		t.Fatalf("header lost after the resize loop:\n%s", visibleText(s.out.String()))
	}
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d", code)
	}
	s.assertRestored()
}

// The 16-color profile receives the reviewed per-token slot table, not a nearest
// RGB guess: every state token keeps a distinct code, and no 256-color or
// truecolor sequence appears.
func TestPTY16ColorKeepsDistinctStateTokens(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())
	s := startPTYWithTerm(t, project, env, "xterm")
	s.waitFor("마지막 조회", 15*time.Second)
	s.waitFor("스택 상태", 15*time.Second)
	tail := s.out.String()
	for _, bad := range []string{"38;2", "38;5", "48;2", "48;5"} {
		if strings.Contains(tail, bad) {
			t.Fatalf("16-color terminal received %q:\n%q", bad, tail)
		}
	}
	// The status screen paints a running service green and the accent
	// somewhere on the menu. Both must arrive as their own slots.
	for _, code := range []string{"\x1b[0;92m", "\x1b[0;94m"} {
		if !strings.Contains(tail, code) {
			t.Fatalf("16-color output lacks %q:\n%q", code, tail)
		}
	}
	s.send("q")
	if code := s.wait(); code != 0 {
		t.Fatalf("TUI exit = %d", code)
	}
	s.assertRestored()
}
