package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// streamingLogs is a logs screen with a deterministic script. The script gets
// the run context and the invocation so a test can print lines and then hold
// the stream still until it has scrolled.
func streamingLogs(t *testing.T, script func(ctx context.Context, in core.Invocation) (any, error)) *fakeLiveFeature {
	t.Helper()
	d := descriptor("logs", true)
	d.Title = "로그 보기"
	return &fakeLiveFeature{desc: d, mode: core.LiveStream, run: script}
}

func numberedLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line-%02d", i)
	}
	return out
}

// waitView pumps live messages until the screen shows want.
func waitView(t *testing.T, m *Model, q *msgQueue, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.View(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("view never showed %q:\n%s", want, m.View())
		}
		q.next(t, m)
	}
}

// detachLive presses one key and pumps until the management screen is back.
func detachLive(t *testing.T, m *Model, q *msgQueue, msg tea.KeyMsg) {
	t.Helper()
	nm, _ := m.Update(msg)
	*m = *(nm.(*Model))
	deadline := time.Now().Add(5 * time.Second)
	for m.phase != phaseReady {
		if time.Now().After(deadline) {
			t.Fatalf("live screen did not close after %q:\n%s", msg.String(), m.View())
		}
		q.next(t, m)
	}
}

// liveHeader is every body row before the first output row. It is what must
// stay fixed while the output scrolls.
func liveHeader(t *testing.T, view string) []string {
	t.Helper()
	var header []string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "line-") {
			return header
		}
		header = append(header, line)
	}
	t.Fatalf("no output row in view:\n%s", view)
	return nil
}

// indicatorRow is the last body row: the position statement sits directly
// above the keycap footer, outside the scrolling band.
func indicatorRow(t *testing.T, m *Model) string {
	t.Helper()
	lines := viewLines(t, m)
	if len(lines) < 2 {
		t.Fatalf("frame has %d rows", len(lines))
	}
	return lines[len(lines)-2]
}

// A long log stream scrolls under a header that never moves: the service, the
// instance, the screen kind and the command stay readable while PgUp/g walk
// back through the output.
func TestLiveLogsScrollUnderAPinnedHeader(t *testing.T) {
	lines := numberedLines(60)
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range lines {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, "line-59")

	header := liveHeader(t, m.View())
	for _, want := range []string{"로그 보기", "실시간 로그", "서비스는 계속 실행 중", "서비스: 스택 전체", "인스턴스: 전체", "명령: logs"} {
		if !strings.Contains(strings.Join(header, "\n"), want) {
			t.Fatalf("pinned header missing %q:\n%s", want, strings.Join(header, "\n"))
		}
	}
	if row := indicatorRow(t, m); !strings.Contains(row, "/60") || !strings.Contains(row, "PgUp/PgDn") {
		t.Fatalf("position indicator = %q", row)
	}
	if !strings.Contains(m.View(), "line-59") {
		t.Fatalf("the tail is not visible at the start:\n%s", m.View())
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	after := liveHeader(t, m.View())
	if strings.Join(header, "\n") != strings.Join(after, "\n") {
		t.Fatalf("PgUp moved the pinned header:\nbefore:\n%s\nafter:\n%s",
			strings.Join(header, "\n"), strings.Join(after, "\n"))
	}
	if strings.Contains(m.View(), "line-59") {
		t.Fatalf("PgUp did not move the window:\n%s", m.View())
	}
	if row := indicatorRow(t, m); !strings.Contains(row, "/60") {
		t.Fatalf("position indicator after PgUp = %q", row)
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if !strings.Contains(m.View(), "line-00") {
		t.Fatalf("g did not reach the first line:\n%s", m.View())
	}
	if !strings.HasPrefix(strings.TrimSpace(indicatorRow(t, m)), "1-") {
		t.Fatalf("position indicator at the top = %q", indicatorRow(t, m))
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if !strings.Contains(m.View(), "line-59") {
		t.Fatalf("G did not return to the tail:\n%s", m.View())
	}
	if row := indicatorRow(t, m); !strings.Contains(row, "60/60") {
		t.Fatalf("position indicator at the tail = %q", row)
	}
	if strings.Join(header, "\n") != strings.Join(liveHeader(t, m.View()), "\n") {
		t.Fatalf("scrolling changed the pinned header:\n%s", m.View())
	}

	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	view := m.View()
	for _, want := range []string{"마지막 실시간 출력", "line-59", "로그 보기"} {
		if !strings.Contains(view, want) {
			t.Fatalf("management view missing %q:\n%s", want, view)
		}
	}
}

// Application output is user data, but the screen still measures what the
// terminal will draw. A tab is expanded to four spaces and a colour SGR is kept,
// so a coloured line reads the same without any control byte reaching the
// terminal. Everything else on the line is untouched: no re-color, no rewrap.
func TestLiveLogLinesKeepTheirTextAndPaint(t *testing.T) {
	source := []string{
		`proj-web-1  | 2026-11-20T10:00:00Z GET /healthz 200 3ms`,
		"\x1b[31mproj-web-1  | error:\x1b[0m connection refused",
		"proj-web-1  |     indented    with    tabs",
		`proj-web-1  | {"level":"info","msg":"json stays one line"}`,
		"proj-web-1  | 한국어 로그 줄도 그대로",
	}
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range source {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	// A color theme: the frame is allowed to paint its own chrome, so any color
	// on an output row would be the TUI adding it.
	colored := visual.Resolve(visual.Inputs{
		ThemeSetting: "dark", Profile: visual.ProfileTrueColor, DarkBackground: true, UTF8: true,
	})
	m.theme = colored
	q := startCmds(m.Init())
	resize(t, m, 100, 24)
	waitView(t, m, q, "한국어 로그 줄도 그대로")

	// The band is the rows after the fixed header, as many as the viewport
	// draws. Each frame row is padded to the terminal width, so trimming the
	// padding must give back exactly the bytes the process wrote.
	lines := viewLines(t, m)
	// Row 0 is the terminal header; the body starts right below it.
	begin := 1 + m.liveFixedRows()
	end := begin + m.liveViewport().Visible()
	if end > len(lines) {
		t.Fatalf("band %d..%d past the frame:\n%s", begin, end, m.View())
	}
	draw := lines[begin:end]
	if len(draw) != len(source) {
		t.Fatalf("drew %d output rows, want %d:\n%s", len(draw), len(source), m.View())
	}
	for i, want := range source {
		if got := strings.TrimRight(draw[i], " "); got != want {
			t.Fatalf("log row %d changed:\n got %q\nwant %q", i, got, want)
		}
	}

	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	// The kept panel is the same bytes: the management screen may frame it, but
	// the log content itself is untouched.
	view := m.View()
	for _, line := range source[1:] {
		if !strings.Contains(view, line) {
			t.Fatalf("management panel lost %q:\n%s", line, view)
		}
	}
}

// recordingAttach echoes what the child actually received so key forwarding can
// be asserted on bytes, not on the TUI's intent.
type recordingAttach struct {
	*fakeLiveFeature
	mu     sync.Mutex
	got    []byte
	closed bool
}

func (f *recordingAttach) Run(ctx context.Context, in core.Invocation) (any, error) {
	fmt.Fprintln(in.IO.Stdout, "attached to proj-db-1")
	buf := make([]byte, 128)
	for {
		n, err := in.IO.Stdin.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.got = append(f.got, buf[:n]...)
			f.mu.Unlock()
		}
		if err != nil {
			f.mu.Lock()
			f.closed = true
			f.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (f *recordingAttach) received() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte{}, f.got...)
}

// An attached screen keeps forwarding every key it does not reserve, and only
// ctrl+] ends it. Detaching restores the management screen without stopping the
// service and leaves the last output readable.
func TestLiveAttachForwardsKeysAndCtrlBracketDetaches(t *testing.T) {
	desc := descriptor("attach", true)
	desc.Title = "서비스 접속"
	desc.AcceptsArgs = true
	f := &recordingAttach{fakeLiveFeature: &fakeLiveFeature{desc: desc, mode: core.LiveAttach}}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	for m.live == nil {
		q.next(t, m)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("l")},
		{Type: tea.KeyRunes, Runes: []rune("s")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyUp},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyTab},
	} {
		nm, _ := m.Update(key)
		*m = *(nm.(*Model))
	}
	// Ctrl+c, tab, enter and the arrow reach the child: an attached terminal
	// forwards them, and the TUI must not intercept them for its own screen.
	want := []byte("ls\r\x1b[A\x03\t")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := f.received(); string(got) == string(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child received %q, want %q", f.received(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// ctrl+] is the detach key and is never forwarded.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	*m = *(nm.(*Model))
	for m.phase != phaseReady {
		q.next(t, m)
	}
	got := string(f.received())
	if strings.ContainsRune(got, 0x1d) {
		t.Fatalf("ctrl+] was forwarded to the child: %q", got)
	}
	if m.result == nil || m.result.Outcome != core.OutcomeCanceled {
		t.Fatalf("result = %+v", m.result)
	}

	view := m.View()
	for _, want := range []string{"마지막 실시간 출력", "attached to proj-db-1", "서비스는 계속 실행 중"} {
		if !strings.Contains(view, want) {
			t.Fatalf("management view missing %q:\n%s", want, view)
		}
	}
	if !f.closed {
		t.Fatal("detach did not close the child's input stream")
	}
}

// The tail cap stays: a runaway stream keeps only the most recent lines. What
// changed is honesty — when lines were dropped, the position row says how many,
// so a reader cannot mistake the retained window for the whole stream.
func TestLiveDroppedLinesAreStatedInTheIndicator(t *testing.T) {
	total := liveLineLimit + 30
	lines := numberedLines(total)
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range lines {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, fmt.Sprintf("line-%02d", total-1))

	row := indicatorRow(t, m)
	if !strings.Contains(row, "오래된 줄 30개 버림") {
		t.Fatalf("position row does not state the dropped lines: %q", row)
	}
	if !strings.Contains(row, "/400") {
		t.Fatalf("position row total is not the retained window: %q", row)
	}
	// The cap itself is unchanged: only the last 400 lines are kept.
	if len(m.live.lines) != liveLineLimit {
		t.Fatalf("retained %d lines, want %d", len(m.live.lines), liveLineLimit)
	}
	if m.live.dropped != 30 {
		t.Fatalf("dropped count = %d, want 30", m.live.dropped)
	}
	// Scrolling to the top of what is still reachable must not claim line-00.
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if strings.Contains(m.View(), "line-00") {
		t.Fatalf("g reached a dropped line:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "line-30") {
		t.Fatalf("g did not reach the oldest retained line:\n%s", m.View())
	}
	if !strings.Contains(indicatorRow(t, m), "오래된 줄 30개 버림") {
		t.Fatalf("the dropped note disappeared after scrolling: %q", indicatorRow(t, m))
	}

	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !strings.Contains(m.View(), fmt.Sprintf("line-%02d", total-1)) {
		t.Fatalf("the retained tail was lost after detach:\n%s", m.View())
	}
}

// The last output does not vanish with the live screen: the management view
// keeps it in a titled panel, so the reader can still see what the process
// printed after detaching.
func TestLiveLastOutputPanelStaysOnTheManagementScreen(t *testing.T) {
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		fmt.Fprintln(in.IO.Stdout, "ready on :8080")
		fmt.Fprintln(in.IO.Stdout, "handled GET /healthz")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, "handled GET /healthz")

	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	lines := viewLines(t, m)
	title := -1
	for i, line := range lines {
		if strings.Contains(line, "마지막 실시간 출력") {
			title = i
			break
		}
	}
	if title < 0 {
		t.Fatalf("last-output panel missing:\n%s", m.View())
	}
	// The panel is decorated at 80 columns: a border row carries the title and
	// a closing border row follows the output. A bare heading would not.
	if !strings.Contains(lines[title], "╭") || !strings.Contains(lines[title], "─") {
		t.Fatalf("panel title is not a decorated panel row: %q", lines[title])
	}
	if !strings.Contains(lines[title], "╮") {
		t.Fatalf("panel is not closed on the right: %q", lines[title])
	}
	body := strings.Join(lines[title:], "\n")
	if !strings.Contains(body, "ready on :8080") || !strings.Contains(body, "handled GET /healthz") {
		t.Fatalf("panel lost the output:\n%s", body)
	}
	if !strings.Contains(lines[len(lines)-1], "종료") {
		t.Fatalf("the keycap footer was pushed off:\n%s", m.View())
	}
	// Leaving the screen never stops the service.
	if !strings.Contains(m.View(), "서비스는 계속 실행 중") {
		t.Fatalf("the running promise is missing:\n%s", m.View())
	}
}

// The keycap row states the scroll keys only when they do something: a short
// log that fits must not advertise PgUp. The approved live keys stay.
func TestLiveFooterAdvertisesScrollOnlyWhenTheLogScrolls(t *testing.T) {
	short := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		fmt.Fprintln(in.IO.Stdout, "one line")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, short)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, "one line")
	footer := viewLines(t, m)[len(viewLines(t, m))-1]
	for _, want := range []string{"[q] 화면 닫기", "[ctrl+c] 중단"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("live footer missing %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "PgUp") {
		t.Fatalf("a fitting log advertises scrolling: %q", footer)
	}
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	long := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range numberedLines(60) {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m2 := newTestModel(t, long)
	m2.theme = *plainTheme()
	q2 := startCmds(m2.Init())
	resize(t, m2, 80, 24)
	waitView(t, m2, q2, "line-59")
	footer = viewLines(t, m2)[len(viewLines(t, m2))-1]
	if !strings.Contains(footer, "PgUp/PgDn") {
		t.Fatalf("a scrolling log does not advertise PgUp/PgDn: %q", footer)
	}
	// The scroll keys belong to the log, so ctrl+c still ends the screen.
	for _, want := range []string{"[q] 화면 닫기", "[ctrl+c] 중단"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("live footer lost %q: %q", want, footer)
		}
	}
	detachLive(t, m2, q2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
}

// chattyDiagnosticLive reports a canceled diagnostic long enough that the
// management screen needs more rows than one frame holds at 80x24.
type chattyDiagnosticLive struct{ *fakeLiveFeature }

func (f *chattyDiagnosticLive) CanceledDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category: core.CategoryCanceled, Severity: core.SeverityWarning,
		Message: "로그 스트림을 분리. 서비스는 계속 실행 중. 이 문장은 관리 화면에서 여러 행을 차지하도록 길게 적었음.",
		NextActions: []string{
			"dcraft status로 서비스가 계속 실행 중인지 확인.",
			"다시 따라보려면 dcraft logs를 실행.",
			"화면을 닫아도 컨테이너는 멈추지 않음.",
		},
	}
}

// The kept output panel is a body block, so it costs rows. The result's scroll
// window must be measured against what is actually left; otherwise the position
// counter describes rows that the panel pushed off screen and G cannot reach
// them.
func TestLiveLastOutputPanelKeepsTheResultReachable(t *testing.T) {
	inner := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range numberedLines(24) {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, &chattyDiagnosticLive{inner})
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, "line-23")
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if !m.resultScrolls() {
		t.Fatalf("the panel plus the diagnostics fit at 80x24; the case is not exercised:\n%s", m.View())
	}
	vp := m.currentViewport()
	_, last, total := splitCounter(t, vp.Counter())
	if total != len(m.resultScreen().Band) {
		t.Fatalf("counter total %d, band has %d rows", total, len(m.resultScreen().Band))
	}
	if last != vp.Top+vp.Visible() {
		t.Fatalf("counter last %d vs window %d..%d", last, vp.Top, vp.Top+vp.Visible())
	}
	// The final band row is reachable: G lands on it.
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	band := m.resultScreen().Band
	lastBand := strings.TrimSpace(band[len(band)-1])
	if lastBand != "" && !strings.Contains(m.View(), lastBand) {
		t.Fatalf("G did not reach the last result row %q:\n%s", lastBand, m.View())
	}
	// The panel itself stays on screen: putting the result away is what the
	// reader must do to reclaim the rows, not losing the output.
	if !strings.Contains(m.View(), "마지막 실시간 출력") {
		t.Fatalf("scrolling dropped the kept output panel:\n%s", m.View())
	}
}

// The kept panel is secondary to the result it follows. On a small terminal it
// must yield rows instead of pushing the result and its position row off screen:
// a panel that hides the outcome is worse than a shorter panel.
func TestLiveLastOutputPanelYieldsRowsToTheResultWhenSmall(t *testing.T) {
	inner := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range numberedLines(24) {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	other := &stubFeature{desc: sectionDescriptor("status", "스택 상태", "현재 스택 상태를 읽음.", core.SectionInspect, false, false), body: "상태"}
	m := newTestModel(t, &chattyDiagnosticLive{inner}, other)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 40, 18)
	waitView(t, m, q, "line-23")
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if !strings.Contains(m.View(), "마지막 실시간 출력") {
		t.Fatalf("the kept panel is missing:\n%s", m.View())
	}
	// The outcome is what the reader needs after an event, so it must be on
	// screen or reachable. A panel that fills the frame makes it neither.
	if !strings.Contains(m.View(), "작업 중단") {
		// Not visible: then it must be announced and reachable.
		if !strings.Contains(m.View(), "PgUp/PgDn") {
			t.Fatalf("the result is off screen with no position row:\n%s", m.View())
		}
		pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
		if !strings.Contains(m.View(), "작업 중단") {
			t.Fatalf("G did not reach the result:\n%s", m.View())
		}
	}
	// The panel states that it is a tail when it had to shorten itself.
	if strings.Contains(m.View(), "line-12") {
		t.Fatalf("the panel did not shorten itself on a small screen:\n%s", m.View())
	}
	for i, line := range viewLines(t, m) {
		if visual.Width(line) > 40 {
			t.Fatalf("row %d too wide: %q", i, line)
		}
	}
}

// The fixed header names the concrete target, not just the stack: a reader who
// narrowed the scope must be able to confirm it while the log scrolls. The
// instance comes from the same option the engine used, so the screen cannot
// describe a different container than the one being read.
func TestLiveHeaderNamesTheSelectedServiceAndInstance(t *testing.T) {
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		fmt.Fprintln(in.IO.Stdout, "proj-web-2 | first")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	for m.live == nil {
		q.next(t, m)
	}
	// Simulate the scope the service screen and the exec form would have set
	// before the run started. The header reads the request, so this is exactly
	// what the engine receives.
	m.req.Services = []string{"web"}
	m.req.Options = map[string]string{"index": "1"}
	waitView(t, m, q, "proj-web-2 | first")

	view := m.View()
	for _, want := range []string{"서비스: web", "인스턴스: #1", "명령: logs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("header missing %q:\n%s", want, view)
		}
	}
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
}

// Every row of the live screen fits the terminal at every approved breakpoint,
// header, output and position row included.
func TestLiveScreenFitsEveryApprovedBreakpoint(t *testing.T) {
	for _, size := range [][2]int{{40, 18}, {48, 20}, {80, 24}, {100, 30}, {120, 36}, {200, 50}} {
		logs := numberedLines(60)
		live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
			for _, line := range logs {
				fmt.Fprintln(in.IO.Stdout, line)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		m := newTestModel(t, live)
		m.theme = *plainTheme()
		q := startCmds(m.Init())
		resize(t, m, size[0], size[1])
		waitView(t, m, q, "line-59")
		lines := viewLines(t, m)
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if got := visual.Width(line); got != size[0] {
				t.Fatalf("%dx%d row %d is %d cells: %q", size[0], size[1], i, got, line)
			}
		}
		// The header is still there at the smallest size.
		if !strings.Contains(m.View(), "서비스: 스택 전체") {
			t.Fatalf("%dx%d lost the fixed header:\n%s", size[0], size[1], m.View())
		}
		detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	}
}

// An attached screen forwards every key to the child, so its position row must
// state the range without promising scroll keys the TUI does not own. A reader
// pressing PgUp there expects the remote process to see it.
func TestLiveAttachPositionRowDoesNotAdvertiseScrollKeys(t *testing.T) {
	desc := descriptor("attach", true)
	desc.Title = "서비스 접속"
	desc.AcceptsArgs = true
	f := &recordingAttach{fakeLiveFeature: &fakeLiveFeature{desc: desc, mode: core.LiveAttach}}
	m := newTestModel(t, f)
	m.theme = *plainTheme()
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	for m.live == nil {
		q.next(t, m)
	}
	// Fill the band past its height so the position row appears.
	for i := 0; i < 40; i++ {
		nm, _ := m.Update(liveLineMsg{line: fmt.Sprintf("remote-%02d", i)})
		*m = *(nm.(*Model))
	}
	row := indicatorRow(t, m)
	if !strings.Contains(row, "/40") {
		t.Fatalf("attach has no position statement: %q", row)
	}
	if strings.Contains(row, "PgUp") || strings.Contains(row, "g/G") {
		t.Fatalf("attach advertises scroll keys it forwards to the child: %q", row)
	}
	// PgUp still reaches the child rather than scrolling locally.
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	*m = *(nm.(*Model))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(f.received()), "\x1b[5~") {
		if time.Now().After(deadline) {
			t.Fatalf("PgUp was not forwarded to the child: %q", f.received())
		}
		time.Sleep(10 * time.Millisecond)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	*m = *(nm.(*Model))
	for m.phase != phaseReady {
		q.next(t, m)
	}
}

// A delivery channel that falls behind also loses lines. That loss is output the
// reader could have seen, so the same indicator states it instead of reporting
// only the cap.
func TestLiveSkippedDeliveryLinesAreAlsoStated(t *testing.T) {
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme()
	resize(t, m, 80, 24)
	// Wire the live screen but do not start the message pump: nothing drains the
	// delivery channel, which is exactly the "renderer fell behind" case.
	if cmd := m.start(live); cmd == nil {
		t.Fatal("start returned no command")
	}
	if m.live == nil {
		t.Fatal("live screen was not set up")
	}
	burst := liveChannelCapacity + 188
	for i := 0; i < burst; i++ {
		fmt.Fprintf(m.live.writer, "burst-%03d\n", i)
	}
	m.live.writer.flush()

	row := indicatorRow(t, m)
	if !strings.Contains(row, fmt.Sprintf(msgLiveDropped, 188)) {
		t.Fatalf("skipped delivery is not stated: %q", row)
	}
	// Nothing is claimable about a window with no drained rows, so the row must
	// not invent a range.
	if strings.Contains(row, "-") && strings.Contains(row, "/400") {
		t.Fatalf("position row invented a range with no drained lines: %q", row)
	}
}

// A log line that carries its own color must not leak that color into the TUI's
// own chrome. The frame closes the style it painted, so the row after an
// application escape sequence is still the TUI's own.
func TestLiveLogColorDoesNotLeakIntoTheTUIChrome(t *testing.T) {
	source := []string{
		"\x1b[31mred without reset",
		"plain after the red line",
	}
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		for _, line := range source {
			fmt.Fprintln(in.IO.Stdout, line)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	m.theme = *plainTheme() // the chrome emits no escape at all
	q := startCmds(m.Init())
	resize(t, m, 80, 24)
	waitView(t, m, q, "plain after the red line")

	lines := viewLines(t, m)
	begin := 1 + m.liveFixedRows()
	for i := begin; i < begin+len(source); i++ {
		if got := strings.TrimRight(lines[i], " "); got != source[i-begin] {
			t.Fatalf("row %d changed:\n got %q\nwant %q", i, got, source[i-begin])
		}
	}
	// The application's escape is the only escape on the page: the plain theme
	// adds none of its own, so a color must never come from the TUI here.
	for i, line := range lines {
		if i != begin {
			if strings.Contains(line, "\x1b") {
				t.Fatalf("TUI chrome added an escape on row %d: %q", i, line)
			}
		}
	}
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
}

// A hostile log line must not reach the sidebar. Its carriage return, tab and
// cursor escapes are removed before the line is measured, so the width check
// sees the cells the terminal draws and the pane keeps its column.
func TestLiveHostileLineDoesNotCrossTheSidebarColumn(t *testing.T) {
	hostile := "proj-web-1  | \x1b[2A\x1b[K100%\r90%\tdone\x1b]0;title\x07"
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		fmt.Fprintln(in.IO.Stdout, hostile)
		fmt.Fprintln(in.IO.Stdout, strings.Repeat("x", 400))
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	q := startCmds(m.Init())
	resize(t, m, 120, 30)
	waitView(t, m, q, "done")

	// Every frame row is a fixed width, and a row that overflowed the body
	// would have moved the sidebar or pushed the line past the terminal. The
	// hostile line is drawn as its sanitized text, so the sidebar's right
	// border is at the same column on every body row.
	border := visual.SidebarWidth(120) - 1
	for i, line := range viewLines(t, m) {
		if i == 0 || i == len(viewLines(t, m))-1 {
			continue
		}
		if got := visual.Width(line); got != 120 {
			t.Fatalf("row %d width = %d, want 120: %q", i, got, line)
		}
		if !strings.Contains(line, "│") {
			continue
		}
		if strings.Count(line[:border+1], "│") == 0 && strings.Contains(line, "│") {
			// A body row that lost the pane border was overwritten.
			t.Fatalf("row %d lost the sidebar border: %q", i, line)
		}
	}

	view := ansi.Strip(m.View())
	if strings.Contains(view, "\x1b") {
		t.Fatalf("an escape reached the frame: %q", view)
	}
	for _, want := range []string{"90%", "done"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sanitized output lost %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "100%") {
		t.Fatalf("the rewritten progress tail survived:\n%s", view)
	}
}

// A child's colour SGR survives into the band, because coloured tool output is
// still readable output. It is the child's paint, never the screen's.
func TestLiveKeepsChildColorInTheBand(t *testing.T) {
	live := streamingLogs(t, func(ctx context.Context, in core.Invocation) (any, error) {
		fmt.Fprintln(in.IO.Stdout, "\x1b[31merror:\x1b[0m connection refused")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := newTestModel(t, live)
	q := startCmds(m.Init())
	resize(t, m, 120, 24)
	waitView(t, m, q, "connection refused")
	if !strings.Contains(m.View(), "\x1b[31m") {
		t.Fatalf("child colour was dropped:\n%q", m.View())
	}
	if !strings.Contains(ansi.Strip(m.View()), "error: connection refused") {
		t.Fatalf("text changed:\n%s", ansi.Strip(m.View()))
	}
	detachLive(t, m, q, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
}
