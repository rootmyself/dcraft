package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// A double click is two presses on the same target within this window. The
// clock is injectable so tests never wait on a wall clock.
const mouseDoubleClick = 500 * time.Millisecond

// mouseWheelRows is one wheel notch over scrollable content.
const mouseWheelRows = 3

// hitKind is the action a drawn region stands for. Everything is derived from
// the same layout that painted the row; no key handler is duplicated here.
type hitKind int

const (
	hitNavItem hitKind = iota
	hitEnvItem
	hitServiceRow
	hitWizardCard
	hitWizardInput
	hitFormField
	hitFormChoice
	hitResultRow
	hitModalApprove
	hitModalCancel
)

// hitTarget is one resolved click region in terminal cells. index and aux name
// the item inside the screen (feature, environment, field, option).
type hitTarget struct {
	kind       hitKind
	index, aux int
	x, y       int
	w, h       int
}

func (h hitTarget) contains(x, y int) bool {
	return x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h
}

// key identifies the target across two presses, so a double click cannot pair
// two different rows that happen to be adjacent.
func (h hitTarget) key() string {
	return fmt.Sprintf("%d:%d:%d", h.kind, h.index, h.aux)
}

// bodyHit is a target in body coordinates. row is the 0-based body line and x
// is relative to the left edge of the body. A width of zero fills the body.
type bodyHit struct {
	row, x, w, h int
	kind         hitKind
	index, aux   int
}

// mouseTargets resolves the current target set. It reads the same layout
// functions that View paints, so a press cannot land on a row the screen did
// not draw. With an overlay open only its buttons are targets: the dimmed
// screen underneath never receives a click.
func (m *Model) mouseTargets() []hitTarget {
	if spec, ok := m.overlaySpec(); ok {
		rects := visual.ModalButtonRects(m.theme, m.width, m.height, spec)
		out := make([]hitTarget, 0, len(rects))
		for _, r := range rects {
			kind := hitModalApprove
			if r.Index == 1 {
				kind = hitModalCancel
			}
			out = append(out, hitTarget{kind: kind, x: r.X, y: r.Y, w: r.Width, h: r.Height})
		}
		return out
	}

	bodyX := 0
	if m.sidebarVisible() {
		bodyX = visual.SidebarWidth(m.width) + 1
	}
	lay := m.layoutBody()
	// A body taller than the frame is cut by Compose, and its last drawn row
	// becomes the overflow note. Nothing below that is on screen, so no row
	// there may receive a click.
	limit := len(lay.lines)
	bodyRows := visual.BodyRows(m.height)
	if m.height > 0 && bodyRows > 0 {
		if limit > bodyRows {
			limit = bodyRows - 1
		}
		if limit < 0 {
			limit = 0
		}
	}
	out := make([]hitTarget, 0, len(lay.hits)+8)
	for _, hit := range lay.hits {
		if hit.row < 0 || hit.row >= limit {
			continue
		}
		w := hit.w
		if w <= 0 {
			w = m.bodyWidth()
		}
		h := hit.h
		if h <= 0 {
			h = 1
		}
		out = append(out, hitTarget{
			kind: hit.kind, index: hit.index, aux: hit.aux,
			x: bodyX + hit.x, y: 1 + hit.row, w: w, h: h,
		})
	}
	if m.sidebarVisible() {
		// The sidebar is drawn in every phase, so a click on it resolves in
		// every phase. Whether that click moves, asks first, or only reports a
		// running operation is decided by the phase, not by whether a hit
		// exists.
		_, navHits := visual.SidebarWithHits(m.theme, visual.SidebarWidth(m.width), visual.BodyRows(m.height), m.navView())
		for _, nav := range navHits {
			index := m.menuIndexForFeature(nav.Name)
			if index < 0 {
				continue
			}
			if m.height > 0 && nav.Row >= visual.BodyRows(m.height) {
				continue
			}
			out = append(out, hitTarget{
				kind: hitNavItem, index: index,
				x: 0, y: 1 + nav.Row, w: visual.SidebarWidth(m.width), h: 1,
			})
		}
	}
	return out
}

// hitAt is the topmost target under a cell.
func (m *Model) hitAt(x, y int) (hitTarget, bool) {
	targets := m.mouseTargets()
	for i := len(targets) - 1; i >= 0; i-- {
		if targets[i].contains(x, y) {
			return targets[i], true
		}
	}
	return hitTarget{}, false
}

// updateMouse handles a press or wheel. A press selects or focuses; the second
// press on the same target within the double-click window activates it.
func (m *Model) updateMouse(msg tea.MouseMsg) tea.Cmd {
	if isWheel(msg.Button) {
		return m.updateWheel(msg)
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	hit, ok := m.hitAt(msg.X, msg.Y)
	if !ok {
		m.mouseTarget = ""
		return nil
	}
	key := hit.key()
	now := m.mouseNow()
	double := m.mouseTarget == key && !now.Before(m.mouseAt) && now.Sub(m.mouseAt) <= mouseDoubleClick
	m.mouseTarget, m.mouseAt = key, now
	if double {
		// A third press starts a new pair instead of firing twice.
		m.mouseTarget = ""
	}
	return m.activateHit(hit, double)
}

func isWheel(button tea.MouseButton) bool {
	switch button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown,
		tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	}
	return false
}

// activateHit performs the click action. A toggle never fires twice: the
// second press of a double click on a checkbox is consumed, so a double click
// leaves the box checked exactly once.
func (m *Model) activateHit(hit hitTarget, double bool) tea.Cmd {
	switch hit.kind {
	case hitNavItem:
		if hit.index < 0 || !m.menuIndexExists(hit.index) {
			return nil
		}
		return m.activateNav(hit.index, double)
	case hitEnvItem:
		if hit.index < 0 || hit.index >= len(m.envs) {
			return nil
		}
		m.envSelected = hit.index
		if double {
			return m.chooseEnvironment()
		}
	case hitServiceRow:
		if double {
			return nil
		}
		m.selectServiceRow(hit.index)
	case hitWizardCard:
		return m.clickWizardCard(hit.index, double)
	case hitWizardInput:
		if m.phase != phaseWizardStep {
			return nil
		}
		// The new-item row is part of the same list cursor. While a list item
		// is being edited, moving the cursor there would commit the buffer to
		// an index past the items, so the click is ignored like Up/Down.
		if m.wizardEditing {
			return nil
		}
		m.wizardChoice = len(m.wizardItems)
		m.followWizardCursor()
		if double {
			return m.wizardEnter()
		}
	case hitFormField:
		if m.form == nil || hit.index < 0 || hit.index >= len(m.form.fields) {
			return nil
		}
		m.form.focus = hit.index
		m.form.notice = ""
		m.form.noticeField = -1
	case hitFormChoice:
		if m.form == nil || hit.index < 0 || hit.index >= len(m.form.fields) {
			return nil
		}
		field := m.form.fields[hit.index]
		if field.Kind != core.InputChoice || hit.aux < 0 || hit.aux >= len(field.Options) {
			return nil
		}
		m.form.focus = hit.index
		m.form.choices[field.Key] = hit.aux
		m.form.notice = ""
		m.form.noticeField = -1
		if double {
			return m.formEnter()
		}
	case hitResultRow:
		if double && !m.detailOpen && m.hasDetail() {
			m.openDetail()
		}
	case hitModalApprove:
		if double {
			return m.modalKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
	case hitModalCancel:
		if double {
			return m.modalKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
	}
	return nil
}

// selectServiceRow moves the scope cursor and toggles the clicked service. The
// whole-stack row clears every individual choice, exactly like Space on it.
func (m *Model) selectServiceRow(index int) {
	if index < 0 || index >= len(m.serviceOptions) {
		if m.serviceScope != scopeWholeStack {
			return
		}
		m.serviceCursor = 0
		m.serviceWhole = true
		for i := range m.serviceChecked {
			m.serviceChecked[i] = false
		}
		m.followServiceCursor()
		return
	}
	m.serviceCursor = index + m.wholeRowOffset()
	if m.serviceOptions[index].Configured && index < len(m.serviceChecked) {
		if !m.serviceChecked[index] && m.serviceCap > 0 && countChecked(m.serviceChecked) >= m.serviceCap {
			m.notice = fmt.Sprintf(msgServiceCap, m.serviceCap)
			m.followServiceCursor()
			return
		}
		m.notice = ""
		m.serviceChecked[index] = !m.serviceChecked[index]
		m.serviceWhole = m.serviceScope != scopeRequired && !anyChecked(m.serviceChecked)
	}
	m.followServiceCursor()
}

// clickWizardCard handles one card of the current question. A multi-select
// card toggles once per click; the second press of a double click is ignored.
// A single-choice card selects on one click and advances on two.
func (m *Model) clickWizardCard(index int, double bool) tea.Cmd {
	if m.phase != phaseWizardStep || m.wizardStep < 0 || m.wizardStep >= len(m.wizardSpec.Steps) {
		return nil
	}
	step := m.wizardSpec.Steps[m.wizardStep]
	if step.List {
		if index < 0 || index >= len(m.wizardItems) {
			return nil
		}
		// The keyboard blocks Up/Down while editing, so a click must not move
		// the selection either: the next Enter would write the buffer into a
		// different item. The click is ignored, the same as the keys.
		if m.wizardEditing {
			return nil
		}
		m.wizardChoice = index
		m.followWizardCursor()
		if double {
			return m.wizardEnter()
		}
		return nil
	}
	if index < 0 || index >= len(step.Options) {
		return nil
	}
	m.wizardChoice = index
	m.followWizardCursor()
	if step.Multi {
		if double {
			// The first press already toggled; never undo it.
			return nil
		}
		value := step.Options[index].Value
		m.wizardChecked[value] = !m.wizardChecked[value]
		return nil
	}
	if double {
		return m.wizardEnter()
	}
	return nil
}

// wizardEnter is Enter on the current question: the same path the keyboard
// takes, so mouse and keys share one validation and one advance rule.
func (m *Model) wizardEnter() tea.Cmd {
	if m.phase != phaseWizardStep {
		return nil
	}
	return m.updateWizard(tea.KeyMsg{Type: tea.KeyEnter})
}

func (m *Model) formEnter() tea.Cmd {
	if m.form == nil {
		return nil
	}
	_, cmd := m.updateFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

func (m *Model) modalKey(msg tea.KeyMsg) tea.Cmd {
	_, cmd := m.updateConfirmKey(msg)
	return cmd
}

// updateWheel scrolls the viewport under the pointer, or moves the cursor when
// the pointer is over a selectable list. A wheel never selects or runs.
func (m *Model) updateWheel(msg tea.MouseMsg) tea.Cmd {
	down := msg.Button == tea.MouseButtonWheelDown
	if !down && msg.Button != tea.MouseButtonWheelUp {
		return nil
	}
	delta := 1
	if !down {
		delta = -1
	}
	if m.overlayOpen() {
		m.scrollOverlay(delta * mouseWheelRows)
		return nil
	}
	if m.sidebarVisible() && msg.X < visual.SidebarWidth(m.width) {
		// The wheel over the pane always means the same thing: move the menu
		// cursor. It never scrolls the body underneath, on any screen.
		m.moveSelection(delta)
		return nil
	}
	switch m.phase {
	case phaseSelectingEnvironment:
		if len(m.envs) > 0 {
			if m.envSelected < 0 {
				if down {
					m.envSelected = 0
				} else {
					m.envSelected = len(m.envs) - 1
				}
			} else {
				m.envSelected = (m.envSelected + delta + len(m.envs)) % len(m.envs)
			}
		}
		return nil
	case phaseSelectingServices:
		total := m.serviceRowCount()
		m.serviceCursor = (m.serviceCursor + delta + total) % total
		m.followServiceCursor()
		return nil
	}
	if m.live != nil {
		if m.liveScrolls() {
			m.liveScrollBy(delta * mouseWheelRows)
		}
		return nil
	}
	if m.phase == phaseWizardStep && m.wizardStep < len(m.wizardSpec.Steps) && !m.wizardEditing {
		step := m.wizardSpec.Steps[m.wizardStep]
		if m.wizardStepIsList() {
			// A list is a selectable list for the wheel: items plus the
			// new-item row, clamped instead of wrapping like the arrows.
			total := len(m.wizardItems) + 1
			next := m.wizardChoice + delta
			if next < 0 {
				next = 0
			}
			if next >= total {
				next = total - 1
			}
			m.wizardChoice = next
			m.followWizardCursor()
			return nil
		}
		if !m.wizardTextInput() {
			total := len(step.Options)
			if total > 0 {
				m.wizardChoice = (m.wizardChoice + delta + total) % total
				m.followWizardCursor()
			}
			return nil
		}
	}
	if m.bodyScrolls() {
		m.applyScroll(func(v *visual.Viewport) { v.Move(delta * mouseWheelRows) })
	}
	return nil
}

// liveScrollBy moves the log window by rows and keeps the follow state.
func (m *Model) liveScrollBy(delta int) {
	v := m.liveViewport()
	v.Move(delta)
	m.liveTop = v.Top
	m.liveFollow = v.Top == v.MaxTop()
}
