package visual

import (
	"strings"
	"testing"
)

func fullFacts() Facts {
	return Facts{
		Project:     "acme-platform",
		Environment: "staging",
		Docker:      "27.4.1",
		Compose:     "2.31.0",
		Local:       "로컬",
	}
}

// The header is one row: stack identity on the left, observed tool facts on
// the right. It never polls; the caller passes the last observed values.
func TestHeaderShowsIdentityAndToolFacts(t *testing.T) {
	th := Plain()
	row := Header(th, 200, fullFacts())
	wantLeft := "dcraft · acme-platform · staging"
	wantRight := "Docker 27.4.1 · Compose 2.31.0 · 로컬"
	if !strings.HasPrefix(row, " "+wantLeft) {
		t.Fatalf("header left = %q", row)
	}
	if !strings.HasSuffix(row, wantRight+" ") {
		t.Fatalf("header right = %q", row)
	}
	if w := Width(row); w != 200 {
		t.Fatalf("header is %d cells, want 200", w)
	}
}

// Degradation drops tool facts from the right, then shrinks the left. The
// project identity always outlives the tool versions. Expected values below are
// derived from the contract text, not from the implementation.
func TestHeaderDegradationOrder(t *testing.T) {
	th := Plain()
	f := fullFacts()
	cases := []struct {
		width     int
		wantLeft  string
		wantRight string
	}{
		{200, "dcraft · acme-platform · staging", "Docker 27.4.1 · Compose 2.31.0 · 로컬"},
		{120, "dcraft · acme-platform · staging", "Docker 27.4.1 · Compose 2.31.0 · 로컬"},
		{80, "dcraft · acme-platform · staging", "Docker 27.4.1 · Compose 2.31.0 · 로컬"},
		{70, "dcraft · acme-platform · staging", "Docker 27.4.1 · Compose 2.31.0"},
		{62, "dcraft · acme-platform · staging", "Docker 27.4.1"},
		{48, "dcraft · acme-platform · staging", ""},
		{36, "dcraft · acme-platform", ""},
		{20, "dcraft", ""},
	}
	for _, tc := range cases {
		row := Header(th, tc.width, f)
		if w := Width(row); w > tc.width {
			t.Fatalf("width=%d: header is %d cells", tc.width, w)
		}
		if tc.wantLeft != "" && !strings.Contains(row, tc.wantLeft) {
			t.Fatalf("width=%d: header %q missing left %q", tc.width, row, tc.wantLeft)
		}
		if tc.wantRight == "" {
			if strings.Contains(row, "Docker") || strings.Contains(row, "Compose") {
				t.Fatalf("width=%d: tool facts survived: %q", tc.width, row)
			}
			continue
		}
		if !strings.Contains(row, tc.wantRight) {
			t.Fatalf("width=%d: header %q missing right %q", tc.width, row, tc.wantRight)
		}
	}
}

// The stack identity beats the tool versions: once the right side is empty,
// only the left may keep shrinking.
func TestHeaderNeverDropsTheProjectBeforeTools(t *testing.T) {
	th := Plain()
	row := Header(th, 60, fullFacts())
	if !strings.Contains(row, "acme-platform") {
		t.Fatalf("project was dropped before tools: %q", row)
	}
}

func TestHeaderUnknownFactsAreOmittedNotPlaceholdered(t *testing.T) {
	th := Plain()
	row := Header(th, 120, Facts{Project: "app", Local: "로컬"})
	if strings.Contains(row, "Docker") || strings.Contains(row, "Compose") {
		t.Fatalf("unknown tools rendered: %q", row)
	}
	if !strings.Contains(row, "dcraft · app") || !strings.Contains(row, "로컬") {
		t.Fatalf("header = %q", row)
	}
}

func TestHeaderSurvivesTinyWidths(t *testing.T) {
	th := Plain()
	for width := 1; width <= 40; width++ {
		row := Header(th, width, fullFacts())
		if w := Width(row); w > width {
			t.Fatalf("width=%d: header is %d cells: %q", width, w, row)
		}
	}
}

func TestHeaderNoColorEmitsNoEscapes(t *testing.T) {
	row := Header(Plain(), 120, fullFacts())
	if strings.Contains(row, "\x1b") {
		t.Fatalf("plain header has escapes: %q", row)
	}
}

// --- shell geometry --------------------------------------------------------

func bodyLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "body line " + itoa(i+1)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Every frame is exactly height rows and no row exceeds width. This is the
// structural half of the width contract.
func TestShellGeometryAtEveryBreakpoint(t *testing.T) {
	th := Plain()
	for _, size := range [][2]int{{40, 18}, {48, 20}, {60, 18}, {80, 24}, {100, 30}, {120, 36}, {200, 50}} {
		w, h := size[0], size[1]
		header := Header(th, w, fullFacts())
		footer := Footer(th, PhaseMenu, w)
		lines := Shell(th, w, h, header, bodyLines(30), footer, "… 생략")
		if len(lines) != h {
			t.Fatalf("%dx%d: %d rows", w, h, len(lines))
		}
		for i, line := range lines {
			if got := Width(line); got > w {
				t.Fatalf("%dx%d row %d: %d cells: %q", w, h, i, got, line)
			}
		}
		if !strings.Contains(lines[0], "dcraft") {
			t.Fatalf("%dx%d: header missing: %q", w, h, lines[0])
		}
		if !strings.Contains(lines[h-1], "이동") {
			t.Fatalf("%dx%d: footer missing: %q", w, h, lines[h-1])
		}
	}
}

// Body overflow is stated, never silent. The note replaces the last body row,
// so the frame keeps its exact height.
func TestShellBodyOverflowIsExplicit(t *testing.T) {
	th := Plain()
	lines := Shell(th, 80, 10, "header", bodyLines(30), "footer", "… 내용 더 있음")
	if len(lines) != 10 {
		t.Fatalf("rows = %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "… 내용 더 있음") {
		t.Fatalf("no overflow note:\n%s", joined)
	}
	if strings.Contains(joined, "body line 8") {
		t.Fatalf("clipping kept too many rows:\n%s", joined)
	}
}

// Without a size the shell must not destroy content: unit tests build the model
// before any WindowSizeMsg arrives.
func TestShellWithoutSizeKeepsAllBodyLines(t *testing.T) {
	th := Plain()
	body := bodyLines(40)
	lines := Shell(th, 0, 0, "header", body, "footer", "… 생략")
	if len(lines) != len(body)+2 {
		t.Fatalf("rows = %d, want %d", len(lines), len(body)+2)
	}
}

func TestShellTinyHeightsDegradeSafely(t *testing.T) {
	th := Plain()
	for h := 0; h <= 4; h++ {
		lines := Shell(th, 40, h, "header", bodyLines(5), "footer", "… 생략")
		if h > 0 && len(lines) != h {
			t.Fatalf("height=%d: %d rows", h, len(lines))
		}
		for i, line := range lines {
			if Width(line) > 40 {
				t.Fatalf("height=%d row %d too wide: %q", h, i, line)
			}
		}
	}
}

func TestShellBodyRowsMatchesGeometry(t *testing.T) {
	if got := BodyRows(18); got != 16 {
		t.Fatalf("BodyRows(18) = %d", got)
	}
	if got := BodyRows(1); got != 0 {
		t.Fatalf("BodyRows(1) = %d", got)
	}
	if got := BodyRows(0); got != 0 {
		t.Fatalf("BodyRows(0) = %d", got)
	}
}

// Content longer than the width is truncated with the theme's marker; the
// ASCII theme must use "~" and never emit a UTF-8 ellipsis.
func TestShellBodyWidthContractASCII(t *testing.T) {
	ascii := Resolve(Inputs{ThemeSetting: "dark", Profile: ProfileNone, DarkBackground: true, UTF8: false})
	long := "아주 긴 서비스 설명 " + strings.Repeat("very-long-value-", 8)
	lines := Shell(ascii, 30, 12, Header(ascii, 30, fullFacts()), []string{long}, Footer(ascii, PhaseMenu, 30), "~ 생략")
	for i, line := range lines {
		if Width(line) > 30 {
			t.Fatalf("row %d is %d cells: %q", i, Width(line), line)
		}
	}
	body := lines[1]
	if !strings.HasSuffix(body, "~") {
		t.Fatalf("ASCII truncation marker missing: %q", body)
	}
	if strings.Contains(body, "…") {
		t.Fatalf("UTF-8 ellipsis leaked into ASCII mode: %q", body)
	}
}

func TestHeaderColoredPlateSpansTheRow(t *testing.T) {
	th := Resolve(Inputs{ThemeSetting: "dark", Profile: ProfileTrueColor, DarkBackground: true, UTF8: true})
	row := Header(th, 80, fullFacts())
	if !strings.Contains(row, "\x1b[") {
		t.Fatal("colored header has no styling")
	}
	if Width(row) != 80 {
		t.Fatalf("colored header = %d cells", Width(row))
	}
	// The surface background must start at the first cell: the plate is the
	// whole row, not just the text.
	if !strings.HasPrefix(row, "\x1b[") {
		t.Fatalf("plate does not start at cell 0")
	}
	if stripANSI(row) == "" || !strings.HasPrefix(stripANSI(row), " dcraft") {
		t.Fatalf("plain text = %q", stripANSI(row))
	}
}

// Pinned lines are never the part that gets cut. The confirmation risk
// sentence must survive even when the body is far too long for the frame.
func TestPinnedLinesSurviveOverflow(t *testing.T) {
	th := Plain()
	risk := "이 작업은 스택을 변경합니다. 중단하면 일부 변경이 남을 수 있습니다."
	for _, size := range [][2]int{{40, 18}, {48, 18}, {80, 24}, {120, 36}} {
		w, h := size[0], size[1]
		lines := Compose(th, w, h, Frame{
			Header:   Header(th, w, fullFacts()),
			Body:     bodyLines(40),
			Pinned:   []string{risk},
			Footer:   Footer(th, PhaseConfirm, w),
			Overflow: "… 생략",
		})
		if len(lines) != h {
			t.Fatalf("%dx%d: %d rows", w, h, len(lines))
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "스택을 변경합니다") {
			t.Fatalf("%dx%d: risk sentence was cut:\n%s", w, h, joined)
		}
		if strings.Index(joined, "스택을 변경합니다") > strings.Index(lines[h-1], "[enter]")+len(joined) {
			t.Fatalf("%dx%d: risk is below the footer", w, h)
		}
		if !strings.Contains(lines[h-2], "스택을 변경합니다") {
			t.Fatalf("%dx%d: risk is not pinned above the footer: %q", w, h, lines[h-2])
		}
		for i, line := range lines {
			if Width(line) > w {
				t.Fatalf("%dx%d row %d too wide: %q", w, h, i, line)
			}
		}
	}
}
