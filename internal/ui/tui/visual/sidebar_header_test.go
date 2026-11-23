package visual

import (
	"strings"
	"testing"
)

// The sidebar title bar states the feature count on the left and the approved
// `esc 닫기` hint on the right.
func TestSidebarHeaderCarriesTheCloseHint(t *testing.T) {
	th := Plain()
	lines := Sidebar(th, 30, 24, sampleNav())
	if len(lines) == 0 {
		t.Fatal("no sidebar")
	}
	top := lines[0]
	if !strings.Contains(top, "기능 · 4") {
		t.Fatalf("title lost: %q", top)
	}
	if !strings.Contains(top, "esc 닫기") {
		t.Fatalf("close hint lost: %q", top)
	}
	if got := Width(top); got != 30 {
		t.Fatalf("title row is %d cells: %q", got, top)
	}
	// The hint sits at the right edge: the row ends with the hint and the
	// closing corner.
	if !strings.HasSuffix(top, "esc 닫기 "+th.Glyphs.TR) {
		t.Fatalf("hint is not right-aligned: %q", top)
	}
}

// A pane too narrow for both keeps the title and drops the hint: the name says
// what the pane is, the hint only names a key.
func TestSidebarHeaderDropsTheHintBeforeTheTitle(t *testing.T) {
	th := Plain()
	top := Sidebar(th, 26, 20, sampleNav())[0]
	if !strings.Contains(top, "기능 · 4") {
		t.Fatalf("narrow pane lost the title: %q", top)
	}
	if got := Width(top); got != 26 {
		t.Fatalf("title row is %d cells: %q", got, top)
	}
}

// The hint is colorless text, so it survives a NO_COLOR terminal.
func TestSidebarHeaderHintIsPlainText(t *testing.T) {
	top := Sidebar(Plain(), 30, 24, sampleNav())[0]
	if strings.Contains(top, "\x1b") {
		t.Fatalf("plain sidebar carries an escape sequence: %q", top)
	}
}
