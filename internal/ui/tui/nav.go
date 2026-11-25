package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// Navigation is derived entirely from the descriptors: the menu entries each
// one declares, its Korean label/summary and the Destructive flag. No feature
// name appears here, so adding a feature or a menu entry never edits this file.
//
// Grouping comes from core.Registry.MenuBySection, the contract's single tree
// builder; a descriptor's declared section and menu entries decide the headings
// and the order. The TUI narrows that tree with the active filter and maps it
// onto renderer items; it never re-groups.
//
// m.selected is an index into the visible menu entries, in navigation order.
// The renderer, the arrow keys, the count and the selected entry all come from
// one resolved list, so the cursor can never point at a row that is not drawn.

// navMenuVisible reports whether the feature menu owns the keyboard. During a
// run, a confirmation or a form, arrow keys and "/" belong to that screen.
func (m *Model) navMenuVisible() bool {
	return m.phase == phaseReady && m.menuCount() > 1
}

// navFilterMatches reports whether an entry survives the active filter. A match
// on a group's own label keeps every child, and a matching child keeps its
// parent heading, so a filtered menu still shows where the entry lives.
func (m *Model) navFilterMatches(entry core.MenuEntry) bool {
	needle := strings.ToLower(strings.TrimSpace(m.filter))
	if needle == "" {
		return true
	}
	return menuEntryContains(entry, needle)
}

func menuEntryContains(entry core.MenuEntry, needle string) bool {
	return strings.Contains(strings.ToLower(entry.Label), needle) ||
		strings.Contains(strings.ToLower(entry.Summary), needle)
}

// navMenu is the resolved navigation list: the flattened rows the renderer
// draws, in draw order, with the headings marked and the selectable entries
// addressable by index.
type navMenu struct {
	// sections are the filtered sections, each with its top-level rows.
	sections []navSection
	// entries is the selectable order: every non-group entry, flattened.
	entries []tuiMenuEntry
}

// navSection is one rendered section heading and its rows.
type navSection struct {
	section core.Section
	rows    []tuiMenuRow
}

// tuiMenuRow is one drawn row: either a heading or a selectable entry. An entry
// row stores its index into navMenu.entries, not a pointer, so a later append
// that reallocates the slice cannot leave the row pointing at a stale copy.
type tuiMenuRow struct {
	entryIndex int // -1 for a heading
	group      *tuiMenuGroup
}

// tuiMenuGroup is a drawn sub-heading.
type tuiMenuGroup struct {
	label   string
	summary string
}

// tuiMenuEntry is one selectable menu row plus the navigation index it holds.
type tuiMenuEntry struct {
	index  int
	menu   core.Menu
	parent string
}

// menuTree is the contract tree narrowed to what this model can serve. A model
// built without a registry (a test that only sets m.features) resolves its
// features through the same builder, so there is still exactly one tree rule in
// the program.
func (m *Model) menuTree() core.MenuTree {
	if m.registry != nil {
		return m.registry.MenuBySection(m.menuInclude)
	}
	reg := core.NewRegistry()
	for _, f := range m.features {
		if err := reg.Register(f); err != nil {
			// A feature that cannot register is already excluded from the
			// menu: navigation shows the valid ones.
			continue
		}
	}
	return reg.MenuBySection(m.menuInclude)
}

// menuInclude hides an entry the current runtime cannot serve: the switch list
// needs an engine that can read environments, so the row is absent instead of
// being drawn and refused. Every other entry is always visible.
func (m *Model) menuInclude(entry core.MenuEntry) bool {
	if entry.Kind != core.MenuSwitchEnvironment {
		return true
	}
	return m.engine != nil && m.engine.SupportsEnvironments()
}

// nav builds the filtered navigation. A group whose label or any child matches
// the filter keeps its heading and only the matching children.
func (m *Model) nav() navMenu {
	var out navMenu
	for _, section := range m.menuTree().Sections {
		rows := make([]tuiMenuRow, 0, len(section.Entries))
		for i := range section.Entries {
			entry := section.Entries[i]
			if !entry.Group {
				if !m.navFilterMatches(entry.Entry) {
					continue
				}
				out.appendEntry(&rows, entry, "")
				continue
			}
			groupMatches := m.navFilterMatches(entry.Entry)
			kept := make([]core.Menu, 0, len(entry.Children))
			for _, child := range entry.Children {
				if groupMatches || m.navFilterMatches(child.Entry) {
					kept = append(kept, child)
				}
			}
			if len(kept) == 0 {
				continue
			}
			rows = append(rows, tuiMenuRow{group: &tuiMenuGroup{
				label:   entry.Entry.Label,
				summary: entry.Entry.Summary,
			}})
			for _, child := range kept {
				out.appendEntry(&rows, child, entry.Entry.Feature)
			}
		}
		if len(rows) > 0 {
			out.sections = append(out.sections, navSection{section: section.Section, rows: rows})
		}
	}
	return out
}

// appendEntry adds one selectable row and records its navigation index. The
// index is assigned in draw order, so the cursor is an index into the visible
// list rather than into registration order.
func (n *navMenu) appendEntry(rows *[]tuiMenuRow, menu core.Menu, parent string) {
	entry := tuiMenuEntry{index: len(n.entries), menu: menu, parent: parent}
	n.entries = append(n.entries, entry)
	*rows = append(*rows, tuiMenuRow{entryIndex: entry.index})
}

// menuCount is how many selectable entries the navigation holds, before and
// without the filter. It decides whether the menu exists at all.
func (m *Model) menuCount() int {
	return m.menuTree().Count()
}

// visualNav converts the navigation into the renderer model. selected is the
// navigation index of the current cursor.
func (m *Model) visualNav(nav navMenu, selected int) visual.Nav {
	out := visual.Nav{
		Title:         fmt.Sprintf(msgNavTitle, nav.count()),
		Filter:        m.filter,
		FilterActive:  m.filtering,
		Empty:         msgNavEmpty,
		SummaryOffset: m.marquee.offset,
	}
	for _, section := range nav.sections {
		items := make([]visual.NavItem, 0, len(section.rows))
		for _, row := range section.rows {
			if row.group != nil {
				items = append(items, visual.NavItem{
					Title:   row.group.label,
					Summary: row.group.summary,
					Group:   true,
				})
				continue
			}
			if row.entryIndex < 0 || row.entryIndex >= len(nav.entries) {
				continue
			}
			entry := nav.entries[row.entryIndex]
			d := entry.menu.Feature.Descriptor()
			items = append(items, visual.NavItem{
				Name:     d.Name,
				Title:    entry.menu.Entry.Label,
				Summary:  entry.menu.Entry.Summary,
				Danger:   d.Destructive,
				Selected: entry.index == selected,
				Child:    entry.parent != "",
			})
		}
		out.Sections = append(out.Sections, visual.NavSection{Title: section.section.KoreanLabel(), Items: items})
	}
	return out
}

// count is how many selectable entries the filtered navigation holds.
func (n navMenu) count() int { return len(n.entries) }

// navView is the renderer model for the current navigation. Every renderer call
// site uses it, so the tree is resolved once per call and the cursor is passed
// in one place.
func (m *Model) navView() visual.Nav {
	return m.visualNav(m.nav(), m.selected)
}

// navIndexes is the navigation order, already filtered. Each value is a
// navigation index into the resolved list.
func (m *Model) navIndexes() []int {
	nav := m.nav()
	out := make([]int, 0, len(nav.entries))
	for _, entry := range nav.entries {
		out = append(out, entry.index)
	}
	return out
}

// selectedMenu is the resolved entry the cursor points at, in navigation order.
func (m *Model) selectedMenu() (tuiMenuEntry, bool) {
	nav := m.nav()
	for _, entry := range nav.entries {
		if entry.index == m.selected {
			return entry, true
		}
	}
	return tuiMenuEntry{}, false
}

// selectedFeature is the feature the cursor points at, if any.
func (m *Model) selectedFeature() core.Feature {
	entry, ok := m.selectedMenu()
	if !ok {
		return nil
	}
	return entry.menu.Feature
}

// menuEntryAt is the resolved entry with one navigation index.
func (m *Model) menuEntryAt(index int) (tuiMenuEntry, bool) {
	for _, entry := range m.nav().entries {
		if entry.index == index {
			return entry, true
		}
	}
	return tuiMenuEntry{}, false
}

// menuIndexExists reports whether one navigation index is drawn.
func (m *Model) menuIndexExists(index int) bool {
	_, ok := m.menuEntryAt(index)
	return ok
}

// menuIndexForFeature is the navigation index of a feature's first entry, or -1.
// It is how the opening cursor and a post-switch cursor find the primary
// feature without knowing any feature name.
func (m *Model) menuIndexForFeature(name string) int {
	for _, entry := range m.nav().entries {
		if entry.menu.Entry.Feature == name {
			return entry.index
		}
	}
	return -1
}

// menuIndexFor is the navigation index of one exact entry, or -1.
func (m *Model) menuIndexFor(kind core.MenuKind, feature string) int {
	for _, entry := range m.nav().entries {
		if entry.menu.Entry.Kind == kind && entry.menu.Entry.Feature == feature {
			return entry.index
		}
	}
	return -1
}

// moveSelection walks the visible menu with wrap-around. The cursor is never
// left on an entry the filter hid, and headings are skipped because they are
// not in navIndexes at all.
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
		m.selected = -1
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
	}
	// Every other printable key is filter text, and a space is one of them: it
	// arrives as KeySpace (whose string is " "), not as a separate word, so the
	// query must accept it or a label with a space can never be typed.
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
		m.keepSelectionVisible()
	}
	if msg.Type == tea.KeySpace {
		m.filter += " "
		m.keepSelectionVisible()
	}
	return nil
}
