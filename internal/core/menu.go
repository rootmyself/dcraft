package core

import (
	"fmt"
	"strings"
)

// MenuKind is what a menu entry does when the reader presses enter on it.
//
// A feature declares its menu entries instead of the TUI deriving one entry per
// feature: one feature can offer two entry points (create and switch an
// environment), and one entry can be a heading for the entries under it.
type MenuKind string

const (
	// MenuRun runs the entry's feature exactly as the menu always has.
	MenuRun MenuKind = "run"
	// MenuGroup is a heading, not a target. The cursor skips it and a click on
	// it does nothing; the entries naming it as Parent are drawn under it.
	MenuGroup MenuKind = "group"
	// MenuSwitchEnvironment opens the environment switch list for the entry's
	// feature instead of running it. The feature's CLI behaviour is unchanged.
	MenuSwitchEnvironment MenuKind = "switch-environment"
)

// MenuEntry is one selectable entry (or one heading) in the TUI menu. The
// machine identity of an entry is the (Kind, Feature) pair, so a feature that
// offers two entry points does not need to invent names for them.
type MenuEntry struct {
	Kind MenuKind
	// Feature is the registered feature this entry points at. A group names
	// its own feature too, so a heading is always traceable to its owner.
	Feature string
	// Label is the short Korean title shown in the menu. The screen a reader
	// opens keeps its own title; a menu label never renames a screen.
	Label string
	// Summary is the one-line description the sidebar band shows.
	Summary string
	// Parent is the Feature name of the group heading this entry belongs
	// under. Empty means the entry sits directly under its section heading.
	Parent string
	// Order sorts entries that share a Parent. Equal values fall back to
	// registration order, so the declaration stays stable.
	Order int
}

// menuEntries is the descriptor's declared entries, or the implicit single entry
// that keeps every feature registered before the menu contract existed working
// unchanged.
func (d Descriptor) menuEntries() []MenuEntry {
	if len(d.Menu) > 0 {
		return d.Menu
	}
	return []MenuEntry{{
		Kind:    MenuRun,
		Feature: d.Name,
		Label:   d.Title,
		Summary: d.Summary,
	}}
}

// Menu is one resolved entry of the navigation tree: a declared entry, the
// feature it points at, and the section it belongs to.
type Menu struct {
	Entry   MenuEntry
	Feature Feature
	Section Section
	// Group reports that the entry is a heading. The renderer draws it as a
	// sub-heading and leaves it out of the cursor and the click targets.
	Group bool
	// Children are the entries this group heading owns, already ordered.
	Children []Menu
}

// MenuTree is the resolved navigation: sections in contract order, and inside a
// section the entries in menu order.
type MenuTree struct {
	Sections []MenuSection
}

// MenuSection is one section heading and its entries.
type MenuSection struct {
	Section Section
	// Entries are the top-level entries of this section. A group entry owns
	// its children in Children.
	Entries []Menu
}

// Count is how many selectable entries the tree holds. A group heading is not
// selectable, so it is not counted.
func (t MenuTree) Count() int {
	total := 0
	for _, section := range t.Sections {
		for _, entry := range section.Entries {
			if entry.Group {
				total += len(entry.Children)
				continue
			}
			total++
		}
	}
	return total
}

// MenuBySection resolves every registered feature's declared entries into the
// one navigation tree. Group order is Sections(); inside a section, order is the
// parent's registration position, then Order, then registration order. A group
// heading is placed at its own feature's registration position, so the heading
// never drifts away from the feature that owns it.
//
// The optional include predicate hides an entry the current runtime cannot
// serve (for example the switch list on an engine that cannot read
// environments). A hidden entry is left out of the tree entirely, so the cursor
// and the count never point at a row that is not drawn.
func (r *Registry) MenuBySection(include func(MenuEntry) bool) MenuTree {
	var tree MenuTree
	for _, section := range sectionOrder {
		sectionEntries := r.menuSection(section, include)
		if len(sectionEntries) == 0 {
			continue
		}
		tree.Sections = append(tree.Sections, MenuSection{Section: section, Entries: sectionEntries})
	}
	return tree
}

// menuSection resolves one section's entries. A child's Parent names a group
// entry's Feature; that group is the only place the child can appear.
func (r *Registry) menuSection(section Section, include func(MenuEntry) bool) []Menu {
	visible := func(entry MenuEntry) bool {
		return include == nil || include(entry)
	}
	var roots []Menu
	groups := map[string]int{}
	for _, f := range r.features {
		d := f.Descriptor()
		if d.Section != section {
			continue
		}
		for _, entry := range d.menuEntries() {
			if !visible(entry) {
				continue
			}
			menu := Menu{Entry: entry, Feature: f, Section: section, Group: entry.Kind == MenuGroup}
			if entry.Parent != "" {
				// A child is collected into its group on the second pass, so a
				// child declared before its parent is still placed correctly.
				continue
			}
			if menu.Group {
				groups[entry.Feature] = len(roots)
			}
			// A top-level row sits where its feature was registered. Order is
			// a rule inside one parent, so it never reorders a section: an
			// entry declared second stays second.
			roots = append(roots, menu)
		}
	}
	for _, f := range r.features {
		d := f.Descriptor()
		if d.Section != section {
			continue
		}
		for _, entry := range d.menuEntries() {
			if entry.Parent == "" || !visible(entry) {
				continue
			}
			index, ok := groups[entry.Parent]
			if !ok {
				// Registration validation rejects a dangling parent, so this
				// is unreachable for a registered feature.
				continue
			}
			roots[index].Children = append(roots[index].Children, Menu{
				Entry: entry, Feature: f, Section: section,
			})
		}
	}
	for i := range roots {
		sortMenuEntries(roots[i].Children, r.featureOrder)
	}
	// A top-level entry declares its Order the same way a child does; entries
	// that declare none keep registration order, which is the published default.
	sortMenuEntries(roots, r.featureOrder)
	// A heading with no visible child is not a heading for anything. It is
	// dropped rather than turned into a target: the feature's own entry, when
	// it declares one, is already the row a reader can run.
	kept := roots[:0]
	for _, entry := range roots {
		if entry.Group && len(entry.Children) == 0 {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// featureOrder is the registration index of a feature, used as the stable
// tie-breaker after Order.
func (r *Registry) featureOrder(name string) int {
	for i, f := range r.features {
		if f.Descriptor().Name == name {
			return i
		}
	}
	return len(r.features)
}

// sortMenuEntries orders entries by Order, then registration order, then the
// entry's own declaration position so two entries of one feature stay stable.
func sortMenuEntries(entries []Menu, order func(string) int) {
	if len(entries) < 2 {
		return
	}
	// Stable insertion sort: the list is a handful of rows and keeping it
	// stable means the declaration order is the visible tie-breaker.
	for i := 1; i < len(entries); i++ {
		current := entries[i]
		j := i - 1
		for j >= 0 && menuLess(current, entries[j], order) {
			entries[j+1] = entries[j]
			j--
		}
		entries[j+1] = current
	}
}

func menuLess(a, b Menu, order func(string) int) bool {
	if a.Entry.Order != b.Entry.Order {
		return a.Entry.Order < b.Entry.Order
	}
	if a.Entry.Feature != b.Entry.Feature {
		return order(a.Entry.Feature) < order(b.Entry.Feature)
	}
	return false
}

// validateMenuEntries checks the menu contract of one descriptor. The TUI menu
// is derived data, so a declaration it cannot place is a registration error
// instead of a row that silently disappears.
func validateMenuEntries(d Descriptor) error {
	if len(d.Menu) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for i, entry := range d.Menu {
		switch entry.Kind {
		case MenuRun, MenuGroup, MenuSwitchEnvironment:
		default:
			return fmt.Errorf("feature %q: menu entry %d has unknown kind %q", d.Name, i, entry.Kind)
		}
		if strings.TrimSpace(entry.Label) == "" {
			return fmt.Errorf("feature %q: menu entry %d needs a Korean label", d.Name, i)
		}
		if strings.TrimSpace(entry.Summary) == "" {
			return fmt.Errorf("feature %q: menu entry %d needs a Korean summary", d.Name, i)
		}
		// One feature declares only its own entries. A feature naming another
		// feature's would let two packages claim the same row.
		if entry.Feature != d.Name {
			return fmt.Errorf("feature %q: menu entry %d points at feature %q instead of itself", d.Name, i, entry.Feature)
		}
		if entry.Kind == MenuGroup && entry.Parent != "" {
			return fmt.Errorf("feature %q: menu group %q cannot declare a parent", d.Name, entry.Label)
		}
		key := string(entry.Kind) + "/" + entry.Feature
		if seen[key] {
			return fmt.Errorf("feature %q: duplicate menu entry %s", d.Name, key)
		}
		seen[key] = true
	}
	return nil
}

// validateMenuLinks checks the cross-feature part of the menu contract: every
// Parent names a group entry in the same section, and every group owns at least
// one child. It runs over the whole registry because a parent lives in another
// descriptor.
func (r *Registry) validateMenuLinks() error {
	groups := map[string]Section{}
	children := map[string]int{}
	for _, f := range r.features {
		d := f.Descriptor()
		for _, entry := range d.menuEntries() {
			if entry.Kind == MenuGroup && entry.Parent == "" {
				groups[entry.Feature] = d.Section
			}
		}
	}
	for _, f := range r.features {
		d := f.Descriptor()
		for _, entry := range d.menuEntries() {
			if entry.Parent == "" {
				continue
			}
			section, ok := groups[entry.Parent]
			if !ok {
				return fmt.Errorf("feature %q: menu entry %q has unknown parent %q", d.Name, entry.Label, entry.Parent)
			}
			if section != d.Section {
				return fmt.Errorf("feature %q: menu entry %q belongs to a group in another section", d.Name, entry.Label)
			}
			children[entry.Parent]++
		}
	}
	for name := range groups {
		if children[name] == 0 {
			return fmt.Errorf("feature %q: menu group has no children", name)
		}
	}
	return nil
}
