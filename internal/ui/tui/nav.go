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
// Grouping comes from core.Registry.BySection, the contract's single grouping
// function; a descriptor's declared section decides its heading and the order
// inside one is registration order. The TUI narrows that grouping with the
// active filter and maps it onto renderer items; it never re-groups.
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

// navGroups is the registry grouping narrowed by the active filter. Each member
// carries its registration index so the cursor stays an index into m.features.
func (m *Model) navGroups() []navGroup {
	var groups []navGroup
	for _, section := range m.registrySections() {
		group := navGroup{section: section.Section}
		for _, f := range section.Features {
			d := f.Descriptor()
			if !m.navFilterMatches(d) {
				continue
			}
			group.items = append(group.items, navMember{index: m.featureIndex(d.Name), descriptor: d})
		}
		if len(group.items) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

// navGroup is one rendered heading and its surviving members.
type navGroup struct {
	section core.Section
	items   []navMember
}

// navMember is one feature of a group plus its registration index.
type navMember struct {
	index      int
	descriptor core.Descriptor
}

// registrySections is the contract grouping. A model built without a registry
// (a test that only sets m.features) groups its features through the same
// function, so there is still exactly one grouping rule in the program.
func (m *Model) registrySections() []core.SectionGroup {
	if m.registry != nil {
		return m.registry.BySection()
	}
	reg := core.NewRegistry()
	for _, f := range m.features {
		if err := reg.Register(f); err != nil {
			// A feature that cannot register is already excluded from the
			// menu: navigation shows the valid ones.
			continue
		}
	}
	return reg.BySection()
}

// featureIndex is the registration index of a named feature, or -1.
func (m *Model) featureIndex(name string) int {
	for i, f := range m.features {
		if f.Descriptor().Name == name {
			return i
		}
	}
	return -1
}

// navIndexes is the navigation order: section order, then registration order
// inside each section, narrowed by the active filter.
func (m *Model) navIndexes() []int {
	var out []int
	for _, group := range m.navGroups() {
		for _, member := range group.items {
			if member.index >= 0 {
				out = append(out, member.index)
			}
		}
	}
	return out
}

// nav builds the renderer model from the filtered registry grouping. The
// selected item is the current cursor.
func (m *Model) nav() visual.Nav {
	nav := visual.Nav{
		Title:         fmt.Sprintf(msgNavTitle, len(m.features)),
		Filter:        m.filter,
		FilterActive:  m.filtering,
		Empty:         msgNavEmpty,
		SummaryOffset: m.marquee.offset,
	}
	for _, group := range m.navGroups() {
		items := make([]visual.NavItem, 0, len(group.items))
		for _, member := range group.items {
			d := member.descriptor
			items = append(items, visual.NavItem{
				Name:     d.Name,
				Title:    d.Title,
				Summary:  d.Summary,
				Danger:   d.Destructive,
				Selected: member.index == m.selected,
			})
		}
		nav.Sections = append(nav.Sections, visual.NavSection{Title: group.section.KoreanLabel(), Items: items})
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
