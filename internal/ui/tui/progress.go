package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// progressState is the running `up` log: the command's own plain output lines
// and the writer that feeds them from the feature goroutine. It shares the live
// screen's 400-line tail budget so both output views behave the same way.
type progressState struct {
	writer  *liveWriter
	lines   []string
	dropped int
}

// append keeps the updated tail bounded. Dropped lines are counted because the
// viewport must state that older output is no longer reachable.
func (p *progressState) append(line string) {
	// Same one rule as the live log: sanitize at the point the line enters,
	// so the width check and the scrollbar see the drawn cells.
	p.lines = append(p.lines, visual.SanitizeLine(line))
	if over := len(p.lines) - liveLineLimit; over > 0 {
		p.lines = p.lines[over:]
		p.dropped += over
	}
}

func (p *progressState) totalDropped() int {
	if p == nil {
		return 0
	}
	dropped := p.dropped
	if p.writer != nil {
		dropped += p.writer.droppedLines()
	}
	return dropped
}

// setupProgress creates the opt-in writer for one run. It is separate from the
// live screen's stdout/stderr so an `up` run keeps its normal result document.
func (m *Model) setupProgress() {
	ch := make(chan string, liveChannelCapacity)
	writer := &liveWriter{ch: ch}
	m.progress = &progressState{writer: writer, lines: []string{}}
	m.progressCh = ch
	m.progressTop = 0
	m.progressFollow = true
	m.req.IO.Progress = writer
}

// closeProgress flushes the final partial line, closes the delivery channel and
// drops the lines. A completed run keeps no post-run log panel: the result
// document is what the reader came to see.
func (m *Model) closeProgress() {
	if m.progress == nil {
		m.req.IO.Progress = nil
		return
	}
	m.progress.writer.flush()
	m.progress.writer.close()
	close(m.progressCh)
	m.progress = nil
	m.progressCh = nil
	m.req.IO.Progress = nil
}

func waitProgressCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return progressLineMsg{line: line}
	}
}

// progressLayout is one composed running screen: the windowed timeline, the
// optional dropped-line notice and the log viewport.
type progressLayout struct {
	timeline []string
	dropped  string
	viewport visual.Viewport
	showLog  bool
}

// progressGeometry is the single split decision: the timeline takes at most one
// third of the body, the log viewport takes the rest, and the log is hidden when
// fewer than three rows would remain. The viewport, the keys and the hint all
// read this result, so the drawn rows and the scroll position cannot disagree.
func (m *Model) progressGeometry() progressLayout {
	if m.progress == nil || len(m.progress.lines) == 0 {
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
	dropped := m.progress.totalDropped()
	height := remaining
	if dropped > 0 {
		// The notice is pinned above the viewport, so it spends one of the
		// rows the viewport would otherwise use.
		height--
	}
	if height < 3 {
		return progressLayout{}
	}
	viewport := visual.Viewport{Top: m.progressTop, Height: height, Total: len(m.progress.lines)}.Clamp()
	if m.progressFollow {
		viewport.End()
	}
	lay := progressLayout{timeline: timeline, viewport: viewport, showLog: true}
	if dropped > 0 {
		lay.dropped = fmt.Sprintf(msgLiveDropped, dropped)
	}
	return lay
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
	if lay.dropped != "" {
		lines = append(lines, m.hintText(lay.dropped))
	}
	start, end := lay.viewport.Window()
	width := m.bodyWidth()
	for _, line := range m.progress.lines[start:end] {
		// Logs are never rewrapped: a line that does not fit is cut with the
		// explicit marker, keeping column alignment intact. The line was
		// sanitized when it entered the model, so this measures drawn cells.
		if width > 0 {
			line = visual.Fit(line, width, m.theme.Glyphs.Ell)
		}
		lines = append(lines, line)
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
