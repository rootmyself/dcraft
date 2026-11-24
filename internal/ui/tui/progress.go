package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/termview"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// progressState is the running `up` output. It is the same grid the live screen
// uses, so a compose progress line, a carriage return and a colour are read by
// one rule instead of two.
type progressState struct {
	writer *liveWriter
	view   *termview.View
}

// setupProgress creates the opt-in writer for one run. It is separate from the
// live screen's stdout/stderr so an `up` run keeps its normal result document.
func (m *Model) setupProgress() {
	// The running screen splits its body between the stage timeline and this
	// log, so the grid is sized to the same band the log is drawn in.
	cols := m.bodyWidth()
	if cols < 1 {
		cols = 80
	}
	rows := m.progressLogRows()
	if rows < 1 {
		rows = 8
	}
	view := termview.NewPipe(cols, rows)
	ch := make(chan struct{}, liveNotifyCapacity)
	writer := &liveWriter{view: view, notify: ch}
	m.progress = &progressState{writer: writer, view: view}
	m.progressCh = ch
	m.progressTop = 0
	m.progressFollow = true
	m.req.IO.Progress = writer
}

// progressLogRows is the row count the running screen gives the log viewport,
// before a WindowSizeMsg has settled the geometry.
func (m *Model) progressLogRows() int {
	budget := m.bodyBudget()
	if budget <= 0 {
		return 0
	}
	fixed := m.navRowCount() + m.titleRows() + 1
	return budget - fixed
}

// closeProgress closes the delivery channel and drops the log. A completed run
// keeps no post-run log panel: the result document is what the reader came to
// see.
func (m *Model) closeProgress() {
	if m.progress == nil {
		m.req.IO.Progress = nil
		return
	}
	if m.progress.view != nil {
		_ = m.progress.view.Close()
	}
	close(m.progressCh)
	m.progress = nil
	m.progressCh = nil
	m.req.IO.Progress = nil
}

// progressLayout is one composed running screen: the windowed timeline, the
// optional dropped-line notice and the log viewport.
type progressLayout struct {
	timeline []string
	viewport visual.Viewport
	showLog  bool
}

// progressGeometry is the single split decision: the timeline takes at most one
// third of the body, the log viewport takes the rest, and the log is hidden when
// fewer than three rows would remain. The viewport, the keys and the hint all
// read this result, so the drawn rows and the scroll position cannot disagree.
func (m *Model) progressGeometry() progressLayout {
	if m.progress == nil || m.progress.view == nil {
		return progressLayout{}
	}
	total := len(m.progress.view.Lines())
	if total == 0 {
		return progressLayout{}
	}
	budget := m.bodyBudget()
	if budget <= 0 {
		return progressLayout{}
	}
	fixed := m.navRowCount() + m.titleRows() + 1 // heading
	available := budget - fixed
	if available < 1 {
		available = 1
	}
	limit := budget / 3
	if limit < 1 {
		limit = 1
	}
	timeline := m.timelineLinesLimited(limit)
	remaining := available - len(timeline)
	if remaining < 3 {
		return progressLayout{}
	}
	height := remaining
	if height < 1 {
		height = 1
	}
	v := visual.Viewport{Top: m.progressTop, Height: height, Total: total}.Clamp()
	if m.progressFollow {
		v.End()
	}
	return progressLayout{timeline: timeline, viewport: v, showLog: true}
}

// progressBody is the running screen when the log viewport is visible. The
// second return is false when the log must stay hidden and the caller keeps the
// ordinary timeline body.
func (m *Model) progressBody() (bodyLayout, bool) {
	lay := m.progressGeometry()
	if !lay.showLog {
		return bodyLayout{}, false
	}
	var lines []string
	if nav := m.navLine(); nav != "" {
		lines = append(lines, nav)
	}
	if title := m.screenTitle(); title != "" {
		lines = append(lines, m.titleRow(title, ""), "")
	}
	lines = append(lines, m.progressHeading(msgWorking))
	lines = append(lines, lay.timeline...)
	start, end := lay.viewport.Window()
	all := m.progress.view.Lines()
	if start > len(all) {
		start = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	palette := m.livePalette()
	for _, line := range all[start:end] {
		lines = append(lines, termview.Paint(line.Cells, palette))
	}
	m.progressTop = lay.viewport.Top
	return bodyLayout{lines: lines, viewport: lay.viewport, scrolls: false}, true
}

// progressHeading keeps the spinner on the heading when no running stage is
// visible, exactly as the plain working screen does.
func (m *Model) progressHeading(heading string) string {
	if m.currentStage != nil && m.currentStage.Status == core.StageStarted {
		return heading
	}
	return m.theme.Paint(visual.TokenAccent, m.theme.Glyphs.SpinnerFrame(m.spinnerFrame)) + " " + heading
}

// progressViewport is the log window taken from the same layout pass that draws
// it.
func (m *Model) progressViewport() visual.Viewport {
	return m.progressGeometry().viewport
}

// progressScrolls reports whether the log viewport overflows. It drives the
// footer chips and the key handler.
func (m *Model) progressScrolls() bool {
	lay := m.progressGeometry()
	return lay.showLog && lay.viewport.Overflow()
}

// progressShown reports whether the running screen is drawing a log viewport.
// PgUp/PgDn and the end keys belong to it while it is drawn.
func (m *Model) progressShown() bool {
	return m.progressGeometry().showLog
}

// updateProgressKey moves the log window. The keys only exist while the
// viewport is drawn; a pinned reader stays where they are when new lines arrive
// and End/G resumes the tail.
func (m *Model) updateProgressKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "g", "G":
	default:
		return false
	}
	if !m.progressShown() {
		return false
	}
	v := m.progressViewport()
	switch msg.String() {
	case "pgup":
		v.PgUp()
	case "pgdown":
		v.PgDn()
	case "home", "g":
		v.Home()
	case "end", "G":
		v.End()
	}
	m.progressTop = v.Top
	m.progressFollow = v.Top == v.MaxTop()
	return true
}

// timelineLinesLimited renders the timeline in at most limit display rows. It
// windows whole stage rows around the running (or next) stage and states the
// omitted rows, so a long plan never pushes the log viewport below its share.
func (m *Model) timelineLinesLimited(limit int) []string {
	rows := buildTimeline(m.stages, m.planStages)
	if len(rows) == 0 {
		return nil
	}
	render := func(window []timelineRow) []string {
		out := []string{}
		for _, row := range window {
			out = append(out, m.timelineRowLines(row)...)
		}
		return out
	}
	all := render(rows)
	if limit <= 0 || len(all) <= limit {
		return all
	}
	focus := timelineFocus(rows)
	for size := min(len(rows), limit); size >= 1; size-- {
		start := focus - size/2
		if start < 0 {
			start = 0
		}
		end := start + size
		if end > len(rows) {
			end = len(rows)
			start = end - size
		}
		out := []string{}
		if start > 0 {
			out = append(out, m.hintText(fmt.Sprintf(msgMenuMoreAbove, m.theme.Glyphs.Ell)))
		}
		out = append(out, render(rows[start:end])...)
		if end < len(rows) {
			out = append(out, m.hintText(fmt.Sprintf(msgMenuMoreBelow, m.theme.Glyphs.Ell)))
		}
		if len(out) <= limit {
			return out
		}
	}
	// Even one stage row is taller than the allowance: show its first line.
	lines := render(rows[focus : focus+1])
	if limit < len(lines) {
		lines = lines[:limit]
	}
	return lines
}

// timelineRowLines renders one stage row and its wrapped continuation lines.
// The timeline rows and the split viewport use this one implementation.
func (m *Model) timelineRowLines(row timelineRow) []string {
	line := m.timelineRowLine(row)
	width := m.bodyWidth()
	if width <= 4 {
		return []string{line}
	}
	wrapped := visual.Wrap(strings.TrimPrefix(line, "  "), width-4)
	out := make([]string, 0, len(wrapped))
	for i, part := range wrapped {
		if i == 0 {
			out = append(out, "  "+part)
		} else {
			out = append(out, "    "+part)
		}
	}
	return out
}

// waitProgressCmd asks for one repaint of the running log. The grid is written
// by the feature's goroutine, so a burst of compose output costs one repaint
// rather than one message per line.
func waitProgressCmd(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return progressTickMsg{}
	}
}
