package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
)

// progressFeature writes its scripted lines to the invocation's opt-in
// progress writer and then holds the run open until released. Only the writer
// is used: no stdout/stderr and no live screen is involved.
type progressFeature struct {
	desc  core.Descriptor
	lines []string
	gate  chan struct{}
	once  sync.Once
}

func newProgressFeature(lines ...string) *progressFeature {
	d := descriptor("up", true)
	d.Title = "스택 시작"
	d.StreamsProgress = true
	// Registration requires a read-only primary; the run is a mutating one, so
	// the harness flips the flag after the registry accepted it.
	d.ReadOnly = true
	return &progressFeature{desc: d, lines: lines, gate: make(chan struct{})}
}

func (f *progressFeature) mutate() { f.desc.ReadOnly = false }

func (f *progressFeature) Descriptor() core.Descriptor { return f.desc }

func (f *progressFeature) release() { f.once.Do(func() { close(f.gate) }) }

func (f *progressFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if in.IO.Progress == nil {
		return nil, fmt.Errorf("no progress writer")
	}
	for _, line := range f.lines {
		fmt.Fprintln(in.IO.Progress, line)
	}
	select {
	case <-f.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return "payload", nil
}

func (f *progressFeature) Present(core.Result, int) string { return "시작 결과" }

// startProgressModel builds a running model whose feature has already produced
// the scripted lines, and pumps until want appears.
func startProgressModel(t *testing.T, lines []string, want string) (*Model, *progressFeature, *msgQueue) {
	t.Helper()
	f := newProgressFeature(lines...)
	t.Cleanup(f.release)
	m := newTestModel(t, f)
	f.mutate()
	m.theme = *plainTheme()
	resize(t, m, 80, 24)
	q := startCmds(m.begin(f))
	waitView(t, m, q, want)
	return m, f, q
}

// pump runs queued messages for a short settle window. It is used when the
// assertion is that a line is absent, where waiting for text would hang. A
// deadline keeps animation ticks from extending the window indefinitely.
func pump(t *testing.T, m *Model, q *msgQueue, settle time.Duration) {
	t.Helper()
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		select {
		case msg := <-q.ch:
			nm, cmd := m.Update(msg)
			*m = *(nm.(*Model))
			q.launch(cmd)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func logRows(view string) int {
	rows := 0
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "line-") {
			rows++
		}
	}
	return rows
}

func TestProgressLogViewportShowsTheTailUnderTheTimeline(t *testing.T) {
	lines := numberedLines(30)
	m, _, _ := startProgressModel(t, lines, "line-29")

	view := m.View()
	if !strings.Contains(view, "작업을 실행하는 중") {
		t.Fatalf("working heading is gone:\n%s", view)
	}
	if !strings.Contains(view, "line-29") {
		t.Fatalf("the newest line is not visible:\n%s", view)
	}
	if strings.Contains(view, "line-00") {
		t.Fatalf("the whole log is drawn instead of a tail viewport:\n%s", view)
	}
	// 80x24 leaves 22 body rows: the title block (2), heading (1) and the
	// three-row stage plan leave the log viewport 16 rows.
	if got := logRows(view); got != 16 {
		t.Fatalf("log rows = %d, want 16:\n%s", got, view)
	}
	if !strings.Contains(view, "line-14") || strings.Contains(view, "line-13") {
		t.Fatalf("the viewport is not the last 16 lines:\n%s", view)
	}
	// The keys that move the viewport are advertised once it overflows.
	if !strings.Contains(view, "PgUp/PgDn") {
		t.Fatalf("footer does not advertise the log scroll keys:\n%s", view)
	}
}

func TestProgressLogPinsOnPgUpAndResumesOnEnd(t *testing.T) {
	lines := numberedLines(30)
	m, _, q := startProgressModel(t, lines, "line-29")

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	view := m.View()
	if !strings.Contains(view, "line-00") || strings.Contains(view, "line-29") {
		t.Fatalf("PgUp did not move the viewport:\n%s", view)
	}

	// New output must not drag a pinned reader away from what they are reading.
	if _, err := fmt.Fprintln(m.progress.writer, "line-30"); err != nil {
		t.Fatal(err)
	}
	q.next(t, m)
	view = m.View()
	if !strings.Contains(view, "line-00") || strings.Contains(view, "line-30") {
		t.Fatalf("new lines forced the pinned view to jump:\n%s", view)
	}

	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	view = m.View()
	if !strings.Contains(view, "line-30") {
		t.Fatalf("G did not resume the tail:\n%s", view)
	}
}

func TestProgressLogHidesWhenFewerThanThreeRowsRemain(t *testing.T) {
	f := newProgressFeature(numberedLines(8)...)
	t.Cleanup(f.release)
	m := newTestModel(t, f)
	f.mutate()
	m.theme = *plainTheme()
	resize(t, m, 80, 9)
	q := startCmds(m.begin(f))
	pump(t, m, q, 300*time.Millisecond)

	view := m.View()
	if strings.Contains(view, "line-") {
		t.Fatalf("log viewport was drawn with no readable rows left:\n%s", view)
	}
	if !strings.Contains(view, "작업을 실행하는 중") {
		t.Fatalf("timeline screen was not kept:\n%s", view)
	}
	if m.progress == nil || len(m.progress.lines) == 0 {
		t.Fatal("the progress lines were not collected")
	}
}

func TestProgressLogStatesDroppedLines(t *testing.T) {
	lines := numberedLines(450)
	m, _, _ := startProgressModel(t, lines, "line-449")

	view := m.View()
	if !strings.Contains(view, fmt.Sprintf(msgLiveDropped, 50)) {
		t.Fatalf("dropped-line notice missing:\n%s", view)
	}
	if !strings.Contains(view, "line-449") {
		t.Fatalf("the retained tail is not visible:\n%s", view)
	}
	if strings.Contains(view, "line-00") {
		t.Fatalf("the tail cap was not applied:\n%s", view)
	}
}

func TestProgressLogIsDiscardedWhenTheRunCompletes(t *testing.T) {
	f := newProgressFeature(numberedLines(5)...)
	m := newTestModel(t, f)
	f.mutate()
	m.theme = *plainTheme()
	resize(t, m, 80, 24)
	q := startCmds(m.begin(f))
	waitView(t, m, q, "line-04")

	f.release()
	deadline := time.Now().Add(5 * time.Second)
	for m.phase != phaseReady {
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete:\n%s", m.View())
		}
		q.next(t, m)
	}
	view := m.View()
	if strings.Contains(view, "line-04") {
		t.Fatalf("completed run kept the log viewport:\n%s", view)
	}
	if strings.Contains(view, "마지막 실시간 출력") {
		t.Fatalf("completed up run left a post-run log panel:\n%s", view)
	}
	if m.progress != nil {
		t.Fatal("progress state survived the completed run")
	}
	if !strings.Contains(view, "시작 결과") {
		t.Fatalf("result body is missing:\n%s", view)
	}
}
