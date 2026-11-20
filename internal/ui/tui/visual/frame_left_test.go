package visual

import (
	"strings"
	"testing"
)

// A left pane shares the body region: header and footer still span the full
// width, and the content keeps exactly the remaining cells.
func TestComposeWithLeftPaneKeepsHeaderFooterFullWidth(t *testing.T) {
	th := Plain()
	left := []string{
		"+---+",
		"| A |",
		"| B |",
		"+---+",
	}
	body := []string{"content one", "content two"}
	out := Compose(th, 40, 9, Frame{
		Header: "header", Left: left, Body: body, Footer: "footer",
	})
	if len(out) != 9 {
		t.Fatalf("rows = %d, want 9", len(out))
	}
	for i, line := range out {
		if got := Width(line); got != 40 {
			t.Fatalf("row %d width = %d: %q", i, got, line)
		}
	}
	if !strings.HasPrefix(out[0], "header") || strings.Contains(out[0], "A") {
		t.Fatalf("header was placed in the pane: %q", out[0])
	}
	if !strings.HasPrefix(out[8], "footer") {
		t.Fatalf("footer row = %q", out[8])
	}
	// Left pane is 5 cells, then the approved one-cell gap.
	row := out[1]
	if !strings.HasPrefix(row, "+---+") {
		t.Fatalf("row 1 does not start with the pane: %q", row)
	}
	if got := strings.TrimRight(row[6:], " "); got != "content one" {
		t.Fatalf("content area = %q", got)
	}
}

// A left pane shorter than the body region leaves blanks below its own rows
// (the caller owns the pane's height); a taller one is cut to the region so the
// frame height is never negotiated by its content.
func TestComposeFitsTheLeftPaneToTheBodyRows(t *testing.T) {
	th := Plain()
	for _, tc := range []struct {
		name string
		left []string
	}{
		{name: "short", left: []string{"|"}},
		{name: "tall", left: []string{"|", "|", "|", "|", "|", "|", "|", "|", "|", "|"}},
	} {
		out := Compose(th, 30, 8, Frame{
			Header: "h", Left: tc.left, Body: []string{"body"}, Footer: "f",
		})
		if len(out) != 8 {
			t.Fatalf("%s: rows = %d", tc.name, len(out))
		}
		for i, line := range out {
			if got := Width(line); got != 30 {
				t.Fatalf("%s: row %d width = %d", tc.name, i, got)
			}
		}
		if tc.name == "short" {
			if !strings.HasPrefix(out[1], "| ") {
				t.Fatalf("short pane lost its own row:\n%s", strings.Join(out, "\n"))
			}
			if strings.Contains(out[2], "|") {
				t.Fatalf("pane was invented below its content:\n%s", strings.Join(out, "\n"))
			}
		}
		if tc.name == "tall" {
			// Six body rows for a ten-line pane: the frame keeps its height
			// and never grows to fit the pane.
			if strings.Count(strings.Join(out, "\n"), "|") != 6 {
				t.Fatalf("tall pane was not cut to the body rows:\n%s", strings.Join(out, "\n"))
			}
		}
	}
}

// The body is measured against the content width, so a long body line is
// truncated with the explicit marker instead of crossing into the pane.
func TestComposeLeftPaneShrinksTheContentBudget(t *testing.T) {
	th := Plain()
	out := Compose(th, 30, 6, Frame{
		Header: "h", Left: []string{"12345"}, Body: []string{strings.Repeat("x", 40)}, Footer: "f",
	})
	row := out[1]
	if !strings.HasPrefix(row, "12345 ") {
		t.Fatalf("pane missing: %q", row)
	}
	content := row[6:]
	if Width(content) != 24 {
		t.Fatalf("content cells = %d, want 24: %q", Width(content), content)
	}
	if !strings.HasSuffix(strings.TrimRight(content, " "), th.Glyphs.Ell) {
		t.Fatalf("content was clipped without a marker: %q", content)
	}
}

// Without a left pane the frame geometry is unchanged.
func TestComposeWithoutLeftPaneIsUnchanged(t *testing.T) {
	th := Plain()
	out := Compose(th, 20, 4, Frame{Header: "h", Body: []string{"b1", "b2"}, Footer: "f"})
	want := []string{Fit("h", 20, "…"), Fit("b1", 20, "…"), Fit("b2", 20, "…"), Fit("f", 20, "…")}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, out[i], want[i])
		}
	}
}
