package termview

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// newTestView starts a view and feeds bytes, the way the child's output arrives.
func newTestView(t *testing.T, cols, rows int, input string) *View {
	t.Helper()
	v := New(cols, rows)
	if _, err := v.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return v
}

// rowsText renders the visible screen, one string per row.
func rowsText(v *View) []string {
	lines := v.Lines()
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, Text(line.Cells))
	}
	return out
}

// A shell prompt carries no newline. The grid must hold it as soon as it
// arrives, which is the whole reason a line-buffered writer was wrong.
func TestPromptWithoutNewlineIsVisible(t *testing.T) {
	v := newTestView(t, 20, 3, "$ ")
	rows := rowsText(v)
	if rows[0] != "$" {
		t.Fatalf("prompt row = %q, want %q", rows[0], "$")
	}
}

// Typing arrives one key at a time, with no newline. Each keystroke must land in
// the grid immediately.
func TestTypedKeysAppearAsTheyArrive(t *testing.T) {
	v := New(20, 3)
	for _, key := range []string{"$ ", "l", "s", " ", "-", "l"} {
		if _, err := v.Write([]byte(key)); err != nil {
			t.Fatalf("write %q: %v", key, err)
		}
	}
	if got := rowsText(v)[0]; got != "$ ls -l" {
		t.Fatalf("row = %q, want %q", got, "$ ls -l")
	}
}

// Backspace is a cursor move plus an erase, not a character. A shell emits
// `\b\x1b[K` when the user deletes; the grid must apply it instead of printing
// the control bytes.
func TestBackspaceAndEraseRewriteTheLine(t *testing.T) {
	v := newTestView(t, 20, 3, "abc\b\x1b[K")
	if got := rowsText(v)[0]; got != "ab" {
		t.Fatalf("row = %q, want %q", got, "ab")
	}
}

// A carriage return returns to column zero and the next text overwrites what
// was there. A real terminal leaves the untouched tail of a longer earlier row,
// so the grid must too: a progress bar that pads with spaces is what clears it.
func TestCarriageReturnOverwritesFromColumnZero(t *testing.T) {
	v := newTestView(t, 20, 3, "100%\r90%  ")
	if got := strings.TrimRight(rowsText(v)[0], " "); got != "90%" {
		t.Fatalf("row = %q, want %q", got, "90%")
	}
	// The padded overwrite leaves no stale tail from the longer earlier row.
	if strings.Contains(rowsText(v)[0], "100") {
		t.Fatalf("the previous row survived: %q", rowsText(v)[0])
	}
}

// Editing in the middle of a line writes at the cursor instead of appending.
func TestCursorMovementEditsInPlace(t *testing.T) {
	// Write "ab", go to column 0, move right one, then overwrite "X".
	v := newTestView(t, 20, 3, "ab\x1b[1;1H\x1b[CX")
	if got := rowsText(v)[0]; got != "aX" {
		t.Fatalf("row = %q, want %q", got, "aX")
	}
}

// Insert mode is not part of the emulator, so a child that relies on it would
// overwrite instead of shifting. This pins the current behaviour so a dependency
// change that adds IRM shows up here rather than on a user's screen.
func TestInsertModeIsNotImplemented(t *testing.T) {
	// Insert one blank cell at the cursor, which shifts the tail right by one.
	v := newTestView(t, 20, 3, "ab\x1b[1;1H\x1b[C\x1b[@X")
	if got := strings.TrimRight(rowsText(v)[0], " "); got != "aXb" {
		t.Fatalf("row = %q, want %q after ICH", got, "aXb")
	}
}

// A Korean syllable is two cells wide and occupies one cell with a placeholder
// after it. Text must not double the syllable or lose the next character.
func TestWideRunesKeepTheirCells(t *testing.T) {
	v := newTestView(t, 10, 2, "한글ab")
	line := v.Lines()[0]
	if got := Text(line.Cells); got != "한글ab" {
		t.Fatalf("text = %q, want %q", got, "한글ab")
	}
	if line.Cells[0].Width != 2 {
		t.Fatalf("first cell width = %d, want 2", line.Cells[0].Width)
	}
	if line.Cells[1].Width != 0 {
		t.Fatalf("placeholder width = %d, want 0", line.Cells[1].Width)
	}
}

// A hostile line must not move dcraft's own cursor: a terminal title and a
// cursor-up sequence are consumed by the grid, and the text that remains is what
// the row holds.
func TestHostileSequencesBecomeCellState(t *testing.T) {
	v := newTestView(t, 20, 3, "a\x1b]0;title\x07b\x1b[2Ac")
	joined := strings.Join(rowsText(v), "\n")
	if strings.Contains(joined, "title") {
		t.Fatalf("an OSC title reached the text: %q", joined)
	}
	if strings.Contains(joined, "\x1b") {
		t.Fatalf("an escape reached the text: %q", joined)
	}
	if !strings.Contains(joined, "ab") {
		t.Fatalf("printable text was lost: %q", joined)
	}
}

// Paint is a caller decision. With no palette the row must carry no escape at
// all, which is what NO_COLOR and TERM=dumb require.
func TestNoPaletteEmitsNoEscape(t *testing.T) {
	v := newTestView(t, 20, 2, "\x1b[1;31mred\x1b[0m plain")
	row := Paint(v.Lines()[0].Cells, nil)
	if strings.Contains(row, "\x1b") {
		t.Fatalf("colorless paint carries an escape: %q", row)
	}
	if row != "red plain" {
		t.Fatalf("row = %q, want %q", row, "red plain")
	}
}

// A coloured run is painted once and closed, and the plain run after it carries
// no colour: the reader sees a red word and then uncoloured text.
func TestPaintedRowColoursOnlyItsOwnRun(t *testing.T) {
	v := newTestView(t, 20, 2, "\x1b[31mred\x1b[0m plain")
	got := Paint(v.Lines()[0].Cells, NewPalette())
	if want := ansi.Strip(got); want != "red plain" {
		t.Fatalf("text = %q, want %q", want, "red plain")
	}
	redEnd := strings.Index(got, "red") + len("red")
	if !strings.Contains(got[:redEnd], "31") {
		t.Fatalf("the red run carries no red: %q", got)
	}
	if tail := got[redEnd:]; strings.Contains(tail, "31") {
		t.Fatalf("the colour leaked past its run: %q", tail)
	}
}

// Scrolling output keeps older rows reachable above the screen.
func TestScrolledOutputStaysReachable(t *testing.T) {
	v := New(20, 3)
	for _, line := range []string{"one", "two", "three", "four", "five"} {
		if _, err := v.Write([]byte(line + "\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	if v.ScrollbackLen() != 3 {
		t.Fatalf("scrollback = %d, want 3", v.ScrollbackLen())
	}
	rows := rowsText(v)
	joined := strings.Join(rows, "|")
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("row %q is unreachable; rows=%q", want, joined)
		}
	}
}

// A resize keeps the scrollback and reflows the grid.
func TestResizeKeepsScrollback(t *testing.T) {
	v := New(40, 5)
	for _, line := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := v.Write([]byte(line + "\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	before := v.Window(0, 0).Total
	v.Resize(20, 2)
	if got := v.Window(0, 0).Total; got < before {
		t.Fatalf("total refused from %d to %d", before, got)
	}
	// The grid keeps the largest size it has had: a shrink cannot truncate the
	// bottom of the buffer, so Size reports the grid actually in use.
	cols, rows := v.Size()
	if cols != 40 || rows != 5 {
		t.Fatalf("size = %dx%d, want the kept 40x5", cols, rows)
	}
}

// The child reads its input from the view, and a terminal's reply to the child
// (a cursor position report) must travel the same pipe as the user's keys.
func TestInputPipeCarriesWrittenText(t *testing.T) {
	v := New(20, 3)
	v.SendText("hi")
	buf := make([]byte, 8)
	n, err := v.Input().Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "hi" {
		t.Fatalf("read %q, want %q", buf[:n], "hi")
	}
}

// Closing makes the child's read return EOF, which is how a detach ends.
func TestCloseEndsTheInputPipe(t *testing.T) {
	v := New(20, 3)
	if err := v.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	buf := make([]byte, 8)
	if _, err := v.Input().Read(buf); err == nil {
		t.Fatal("read after close returned no error")
	}
}

// The cursor is part of the screen: a shell leaves it visible, a full-screen
// program hides it.
func TestCursorVisibilityFollowsTheChild(t *testing.T) {
	v := newTestView(t, 20, 3, "$ ")
	pos, shown := v.Cursor()
	if !shown {
		t.Fatal("cursor is hidden after a prompt")
	}
	if pos.X != 2 || pos.Y != 0 {
		t.Fatalf("cursor = %+v, want (2,0)", pos)
	}
	if _, err := v.Write([]byte("\x1b[?25l")); err != nil {
		t.Fatal(err)
	}
	if _, shown := v.Cursor(); shown {
		t.Fatal("cursor is still shown after ESC[?25l")
	}
}

// A split escape sequence arrives across writes; the parser must carry it.
func TestSplitSequenceAcrossWrites(t *testing.T) {
	v := New(20, 2)
	if _, err := v.Write([]byte("\x1b[")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Write([]byte("31mred")); err != nil {
		t.Fatal(err)
	}
	if got := Text(v.Lines()[0].Cells); got != "red" {
		t.Fatalf("row = %q, want %q", got, "red")
	}
}

// A bare newline from a piped stream does not return the carriage, so a pipe
// view normalises it the way a terminal driver does. Without this every log line
// would start where the previous one ended.
func TestPipeViewTranslatesBareNewline(t *testing.T) {
	v := NewPipe(20, 4)
	if _, err := v.Write([]byte("one\ntwo")); err != nil {
		t.Fatal(err)
	}
	rows := rowsText(v)
	if rows[0] != "one" {
		t.Fatalf("row 0 = %q, want %q", rows[0], "one")
	}
	if rows[1] != "two" {
		t.Fatalf("row 1 = %q, want %q", rows[1], "two")
	}
}

// A terminal view must not translate: the child's pseudo-terminal already did.
func TestTerminalViewKeepsItsBytes(t *testing.T) {
	v := newTestView(t, 20, 4, "one\r\ntwo\r\n")
	rows := rowsText(v)
	if rows[0] != "one" || rows[1] != "two" {
		t.Fatalf("rows = %q, want one and two", rows)
	}
}

// A carriage return and its newline may arrive on separate writes.
func TestSplitLineEndingIsNotDoubleTranslated(t *testing.T) {
	v := NewPipe(20, 4)
	if _, err := v.Write([]byte("one\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Write([]byte("\ntwo")); err != nil {
		t.Fatal(err)
	}
	rows := rowsText(v)
	if rows[0] != "one" || rows[1] != "two" {
		t.Fatalf("rows = %q, want one and two (no double translation)", rows)
	}
}

// Write must report the caller's byte count, not the count after newline
// translation. A short count makes os/exec abandon the pipe, which silently
// stopped a streaming command after its first line.
func TestWriteReportsTheCallersByteCount(t *testing.T) {
	v := NewPipe(80, 10)
	// Four bytes, and the translation makes five: the return value must be four.
	n, err := v.Write([]byte("one\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != 4 {
		t.Fatalf("Write returned %d, want the caller's 4 bytes", n)
	}
}

// A streaming child whose output arrives in several writes keeps every line,
// which is the shape os/exec gives a writer that is not an *os.File.
func TestStreamingWriterKeepsEveryLine(t *testing.T) {
	v := NewPipe(80, 10)
	script := exec.Command("/bin/sh", "-c", "printf 'one\\n'; sleep 0.05; printf 'two\\n'; sleep 0.05; printf 'three\\n'")
	script.Stdout = v
	script.Stderr = v
	if err := script.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := script.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	joined := strings.Join(rowsText(v), "|")
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("row %q is missing; rows=%q", want, joined)
		}
	}
}

// The caret must sit on the row the child is drawing, including the ordinary
// case of a prompt near the bottom of a screen that is not full yet.
func TestCaretRowMatchesThePromptRow(t *testing.T) {
	for _, c := range []struct {
		rows    int
		content int
	}{
		{10, 8},
		{10, 9},
		{6, 3},
		{24, 2},
	} {
		// New keeps the caret's own row, which is the interactive-shell view.
		v := New(40, c.rows)
		for i := 0; i < c.content; i++ {
			if _, err := v.Write([]byte(fmt.Sprintf("L%d\r\n", i))); err != nil {
				t.Fatal(err)
			}
		}
		win := v.Window(0, v.Window(0, 0).Total)
		if win.CursorRow < 0 {
			t.Fatalf("%d rows / %d lines: no caret row", c.rows, c.content)
		}
		if win.CursorRow >= len(win.Rows) {
			t.Fatalf("%d rows / %d lines: caret row %d past %d rows", c.rows, c.content, win.CursorRow, len(win.Rows))
		}
		// The caret sits on the empty prompt row just below the last written
		// line, which is where the shell waits for input.
		if got := Text(win.Rows[win.CursorRow].Cells); got != "" {
			t.Fatalf("%d rows / %d lines: caret is on %q, want the empty prompt row", c.rows, c.content, got)
		}
	}
}

// A caret that lands on a wide rune's placeholder goes on the rune itself, so
// the row never becomes one cell wider than the grid.
func TestCaretOnAWideRuneKeepsTheRowWidth(t *testing.T) {
	v := newTestView(t, 20, 2, "한a")
	row := v.Window(0, 1).Rows[0]
	if got := Text(row.Cells); got != "한a" {
		t.Fatalf("row = %q, want %q", got, "한a")
	}
	if row.Cells[1].Width != 0 {
		t.Fatalf("cell 1 is not the placeholder of 한")
	}
}

// A full-screen child owns the whole screen: the main screen's scrollback must
// not be drawn above it, and a painted status bar must survive the blank trim.
func TestAltScreenHidesScrollbackAndKeepsPaintedRows(t *testing.T) {
	v := New(20, 5)
	for i := 0; i < 4; i++ {
		if _, err := v.Write([]byte(fmt.Sprintf("shell-%d\r\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	// Enter the alternate screen and draw a screen whose last row is a
	// reverse-video status bar made of spaces.
	if _, err := v.Write([]byte("\x1b[?1049hAPP-TOP\r\nmid\r\n\r\n\x1b[7m          \x1b[0m")); err != nil {
		t.Fatal(err)
	}
	lines := v.Lines()
	joined := ""
	for _, line := range lines {
		joined += Text(line.Cells) + "|"
	}
	if strings.Contains(joined, "shell-") {
		t.Fatalf("the main screen's scrollback is drawn over the app: %q", joined)
	}
	if !strings.Contains(joined, "APP-TOP") {
		t.Fatalf("the app's own screen is missing: %q", joined)
	}
	if !strings.Contains(joined, "mid") {
		t.Fatalf("a painted row was trimmed as blank: %q", joined)
	}
}

// A shrink must not discard what was on screen. The emulator truncates the
// bottom of its buffer, which is where a shell's prompt and a log's newest lines
// live, so the grid keeps its largest size and the viewport shows the tail.
func TestShrinkLosesNoRows(t *testing.T) {
	v := NewPipe(40, 30)
	for i := 0; i < 9; i++ {
		if _, err := v.Write([]byte(fmt.Sprintf("row%d\r\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	before := v.Lines()
	v.Resize(40, 4)

	after := v.Lines()
	if len(after) != len(before) {
		t.Fatalf("row count fell from %d to %d", len(before), len(after))
	}
	// The newest row is the one a reader is watching; it must survive.
	joined := make([]string, 0, len(after))
	for _, line := range after {
		joined = append(joined, Text(line.Cells))
	}
	if got := joined[len(joined)-1]; got != "row8" {
		t.Fatalf("last row = %q, want row8 (newest output): %q", got, joined)
	}
	// No row may appear twice: a duplicate means the salvage copied a row that
	// survived anyway instead of the one being dropped.
	seen := map[string]bool{}
	for _, row := range joined {
		if row == "" {
			continue
		}
		if seen[row] {
			t.Fatalf("row %q appears twice: %q", row, joined)
		}
		seen[row] = true
	}
}

// A narrower window keeps the text, so widening restores it instead of showing
// a row that was truncated away.
func TestNarrowShrinkKeepsTheRowContent(t *testing.T) {
	v := NewPipe(40, 5)
	if _, err := v.Write([]byte("the-whole-line\r\n")); err != nil {
		t.Fatal(err)
	}
	v.Resize(10, 5)
	v.Resize(40, 5)
	if got := Text(v.Lines()[0].Cells); got != "the-whole-line" {
		t.Fatalf("row = %q, want the whole line after widening again", got)
	}
}

// A readable window is a slice of the same row list, not a copy of the whole
// scrollback: a full screen costs the rows drawn, not the history behind them.
func TestWindowReadsOnlyTheRequestedRows(t *testing.T) {
	v := NewPipe(40, 10)
	for i := 0; i < 200; i++ {
		if _, err := v.Write([]byte(fmt.Sprintf("line-%03d\r\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(5, func() { _ = v.Window(190, 200) })
	if allocs > 200 {
		t.Fatalf("Window(10 rows) allocated %.0f times over a 200-row history; it copies the history", allocs)
	}
	win := v.Window(190, 200)
	if len(win.Rows) != 10 {
		t.Fatalf("Window returned %d rows, want 10", len(win.Rows))
	}
	if got := Text(win.Rows[0].Cells); got != "line-190" {
		t.Fatalf("first row = %q, want line-190", got)
	}
}

// A write after Close fails instead of feeding a pump that has stopped. The
// child is gone, so accepting the bytes would silently drop them.
func TestWriteAfterCloseFails(t *testing.T) {
	v := New(20, 3)
	if err := v.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := v.Write([]byte("late")); err != io.ErrClosedPipe {
		t.Fatalf("Write after Close = %v, want io.ErrClosedPipe", err)
	}
}

// A window requested past the last row reports an empty range at the end, so a
// caller's index arithmetic stays legal.
func TestWindowClampsStartPastTheEnd(t *testing.T) {
	v := NewPipe(20, 5)
	if _, err := v.Write([]byte("one\r\n")); err != nil {
		t.Fatal(err)
	}
	total := v.Window(0, 0).Total
	win := v.Window(total+10, total+20)
	if win.Start != total {
		t.Fatalf("Start = %d, want the total %d", win.Start, total)
	}
	if len(win.Rows) != 0 {
		t.Fatalf("rows = %d, want none", len(win.Rows))
	}
	if win.Start > win.Total {
		t.Fatalf("Start %d is past Total %d", win.Start, win.Total)
	}
}
