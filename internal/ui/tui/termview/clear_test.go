package termview

import (
	"fmt"
	"strings"
	"testing"
)

// clearSequence is what a shell's `clear` sends on a main screen.
const clearSequence = "\x1b[H\x1b[2J"

// A shell's `clear` starts from an empty screen: the scrollback above the prompt
// must not keep the rows the command removed. This is the defect the reader saw
// as "clear leaves something behind" after the prompt had scrolled off.
func TestShellClearEmptiesTheScrollback(t *testing.T) {
	v := New(20, 5)
	defer v.Close()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(v, "line-%02d\r\n", i)
	}
	if v.ScrollbackLen() == 0 {
		t.Fatal("the fixture did not scroll any row off the screen")
	}
	if _, err := v.Write([]byte(clearSequence)); err != nil {
		t.Fatalf("write clear: %v", err)
	}
	if got := v.ScrollbackLen(); got != 0 {
		t.Fatalf("scrollback = %d lines after clear, want 0", got)
	}
	// The screen itself is empty and the caret sits on its first row.
	rows := rowsText(v)
	for i, row := range rows {
		if strings.TrimSpace(row) != "" {
			t.Fatalf("row %d survived the clear: %q", i, row)
		}
	}
	if got := v.Window(0, 0).Total; got != 1 {
		t.Fatalf("row list after clear = %d rows, want the caret row only", got)
	}
}

// A log stream never asks for this: its GRID keeps the history a reader scrolls
// through, and a clear sequence inside container output is just output.
func TestPipeClearKeepsTheScrollback(t *testing.T) {
	v := NewPipe(20, 5)
	defer v.Close()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(v, "line-%02d\n", i)
	}
	before := v.ScrollbackLen()
	if before == 0 {
		t.Fatal("the fixture did not scroll any row off the screen")
	}
	if _, err := v.Write([]byte(clearSequence)); err != nil {
		t.Fatalf("write clear: %v", err)
	}
	if got := v.ScrollbackLen(); got == 0 {
		t.Fatal("a log stream's clear erased the reader's history")
	}
}

// A full-screen child owns the screen, so its own clear is screen-only: the
// shell history behind it is not that child's to erase.
func TestAltScreenClearKeepsTheShellHistory(t *testing.T) {
	v := New(20, 5)
	defer v.Close()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(v, "line-%02d\r\n", i)
	}
	before := v.ScrollbackLen()
	if before == 0 {
		t.Fatal("the fixture did not scroll any row off the screen")
	}
	// Enter the alternate screen the way a pager does, clear inside it, leave.
	if _, err := v.Write([]byte("\x1b[?1049h" + clearSequence)); err != nil {
		t.Fatalf("write alt screen clear: %v", err)
	}
	if got := v.ScrollbackLen(); got != before {
		t.Fatalf("alt-screen clear changed the shell history: %d -> %d", before, got)
	}
}

// ESC[3J is explicitly "clear including scrollback" and must keep working on
// both kinds of view.
func TestEraseDisplay3AlwaysEmptiesTheScrollback(t *testing.T) {
	for name, v := range map[string]*View{
		"attach": New(20, 5),
		"pipe":   NewPipe(20, 5),
	} {
		t.Run(name, func(t *testing.T) {
			defer v.Close()
			for i := 0; i < 12; i++ {
				fmt.Fprintf(v, "line-%02d\r\n", i)
			}
			if v.ScrollbackLen() == 0 {
				t.Fatal("the fixture did not scroll any row off the screen")
			}
			if _, err := v.Write([]byte("\x1b[H\x1b[3J")); err != nil {
				t.Fatalf("write erase-display 3: %v", err)
			}
			if got := v.ScrollbackLen(); got != 0 {
				t.Fatalf("scrollback = %d lines after ESC[3J, want 0", got)
			}
		})
	}
}

// Erase-above is a cursor-relative edit, not a clear: the rows above the cursor
// are removed from the screen, but the reader's history stays. Erase-below is
// what a shell's `clear` sends on a screen/linux terminal, so it does count as a
// clear (see TestShellClearWithEraseBelowEmptiesTheScrollback).
func TestEraseAboveKeepsTheScrollback(t *testing.T) {
	v := New(20, 5)
	defer v.Close()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(v, "line-%02d\r\n", i)
	}
	before := v.ScrollbackLen()
	if _, err := v.Write([]byte("\x1b[1J")); err != nil {
		t.Fatalf("write erase-above: %v", err)
	}
	if got := v.ScrollbackLen(); got != before {
		t.Fatalf("ED 1 changed the scrollback: %d -> %d", before, got)
	}
}

// A shell's clear does not always send ED 2. ncurses on a screen, tmux, linux
// or ansi terminal and busybox's clear send `ESC[H ESC[J` (ED 0), which used to
// leave every cleared row one PgUp away and drew it above the prompt again.
func TestShellClearWithEraseBelowEmptiesTheScrollback(t *testing.T) {
	for name, sequence := range map[string]string{
		"busybox ED0":  "\x1b[H\x1b[J",
		"scroll ED3+2": "\x1b[H\x1b[3J\x1b[H\x1b[2J",
	} {
		t.Run(name, func(t *testing.T) {
			v := New(20, 5)
			defer v.Close()
			for i := 0; i < 12; i++ {
				fmt.Fprintf(v, "line-%02d\r\n", i)
			}
			if v.ScrollbackLen() == 0 {
				t.Fatal("the fixture did not scroll any row off the screen")
			}
			if _, err := v.Write([]byte(sequence)); err != nil {
				t.Fatalf("write %q: %v", sequence, err)
			}
			if got := v.ScrollbackLen(); got != 0 {
				t.Fatalf("scrollback = %d lines after clear, want 0", got)
			}
			rows := rowsText(v)
			for y := 0; y < len(rows); y++ {
				if strings.TrimSpace(rows[y]) != "" {
					t.Fatalf("row %d survived the clear: %q", y, rows[y])
				}
			}
		})
	}
}
