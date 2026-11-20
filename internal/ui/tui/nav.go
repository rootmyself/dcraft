package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// Navigation is derived entirely from the descriptors: the section each
// feature declares, its Korean title/summary and the Destructive flag. No
// feature name appears here, so adding a feature never edits this file.
//
// m.selected stays an index into m.features (registration order), because the
// rest of the model and the tests address features that way. Only the display
// and the arrow-key order are sectioned.

// navMenuVisible reports whether the feature menu owns the keyboard. During a
// run, a confirmation or a form, arrow keys and "/" belong to that screen.
func (m *Model) navMenuVisible() bool {
	return m.phase == phaseReady && len(m.features) > 1
}

// navFilter matches a descriptor's title or summary. Both are Korean user
// text; lowercasing only matters for machine names typed in Latin letters.
func (m *Model) navFilterMatches(d core.Descriptor) bool {
	needle := strings.ToLower(strings.TrimSpace(m.filter))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(d.Title), needle) ||
		strings.Contains(strings.ToLower(d.Summary), needle)
}

// navIndexes is the navigation order: section order, then registration order
// inside each section, narrowed by the active filter.
func (m *Model) navIndexes() []int {
	var out []int
	for _, section := range core.Sections() {
		for i, f := range m.features {
			d := f.Descriptor()
			if d.Section != section || !m.navFilterMatches(d) {
				continue
			}
			out = append(out, i)
		}
	}
	return out
}

// nav builds the renderer model. Sections come from the descriptors; the
// selected item is the current cursor.
func (m *Model) nav() visual.Nav {
	nav := visual.Nav{
		Title:        fmt.Sprintf(msgNavTitle, len(m.features)),
		Filter:       m.filter,
		FilterActive: m.filtering,
		Empty:        msgNavEmpty,
	}
	for _, section := range core.Sections() {
		var items []visual.NavItem
		for i, f := range m.features {
			d := f.Descriptor()
			if d.Section != section || !m.navFilterMatches(d) {
				continue
			}
			items = append(items, visual.NavItem{
				Name:     d.Name,
				Title:    d.Title,
				Summary:  d.Summary,
				Danger:   d.Destructive,
				Selected: i == m.selected,
			})
		}
		if len(items) == 0 {
			continue
		}
		nav.Sections = append(nav.Sections, visual.NavSection{Title: section.KoreanLabel(), Items: items})
	}
	if len(m.navIndexes()) == 0 {
		nav.Sections = nil
	}
	return nav
}

// selectedFeature is the feature the cursor points at, if any.
func (m *Model) selectedFeature() core.Feature {
	if m.selected < 0 || m.selected >= len(m.features) {
		return nil
	}
	return m.features[m.selected]
}

// moveSelection walks the visible (sectioned, filtered) menu with wrap-around.
// The cursor is never left on a feature the filter hid.
func (m *Model) moveSelection(delta int) {
	order := m.navIndexes()
	if len(order) == 0 {
		return
	}
	pos := -1
	for i, idx := range order {
		if idx == m.selected {
			pos = i
			break
		}
	}
	if pos < 0 {
		m.selected = order[0]
		return
	}
	pos = (pos + delta + len(order)) % len(order)
	m.selected = order[pos]
}

// keepSelectionVisible moves the cursor onto a match when the filter hid it,
// so enter can never run something that is not on screen.
func (m *Model) keepSelectionVisible() {
	order := m.navIndexes()
	if len(order) == 0 {
		return
	}
	for _, idx := range order {
		if idx == m.selected {
			return
		}
	}
	m.selected = order[0]
}

// updateFilterKey edits the "/" filter. It runs instead of the global key
// handling, so typing q or r in the filter never quits or refreshes.
func (m *Model) updateFilterKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// esc clears the filter and leaves the menu whole: the filter is a
		// temporary view of the registry, not a saved setting.
		m.filtering = false
		m.filter = ""
		m.keepSelectionVisible()
		return nil
	case "enter":
		// Enter commits the filter and hands the keyboard back so the
		// narrowed list can be navigated and run.
		m.filtering = false
		m.keepSelectionVisible()
		return nil
	case "backspace":
		if m.filter != "" {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
		}
		m.keepSelectionVisible()
		return nil
	case "up":
		m.moveSelection(-1)
		return nil
	case "down":
		m.moveSelection(1)
		return nil
	case "space":
		m.filter += " "
		m.keepSelectionVisible()
		return nil
	}
	// Every other printable key is filter text. j/k are letters here, not
	// menu moves, so a search can contain any word.
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
		m.keepSelectionVisible()
	}
	return nil
}
