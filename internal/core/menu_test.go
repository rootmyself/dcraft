package core

import (
	"context"
	"strings"
	"testing"
)

// menuFeature is a minimal feature that declares menu entries.
type menuFeature struct {
	desc Descriptor
}

func (f *menuFeature) Descriptor() Descriptor { return f.desc }
func (f *menuFeature) Run(_ context.Context, _ Invocation) (any, error) {
	return nil, nil
}

func menuDescriptor(name string, section Section, entries ...MenuEntry) Descriptor {
	return Descriptor{
		Name: name, Title: name, Summary: name + " 요약", Usage: "dcraft " + name,
		Section: section, ReadOnly: true, Menu: entries,
	}
}

func menuRun(feature, label string, order int, parent string) MenuEntry {
	return MenuEntry{Kind: MenuRun, Feature: feature, Label: label, Summary: label + " 요약", Parent: parent, Order: order}
}

func menuGroup(feature, label string) MenuEntry {
	return MenuEntry{Kind: MenuGroup, Feature: feature, Label: label, Summary: label + " 요약"}
}

func registerMenu(t *testing.T, descriptors ...Descriptor) *Registry {
	t.Helper()
	reg := NewRegistry()
	for i := range descriptors {
		reg.MustRegister(&menuFeature{desc: descriptors[i]})
	}
	if err := reg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return reg
}

// A feature with no declared entries still has exactly one: the menu contract
// is additive, so every pre-existing feature keeps its row.
func TestUndeclaredMenuBecomesOneRunEntry(t *testing.T) {
	d := menuDescriptor("status", SectionInspect)
	d.Title = "스택 상태"
	d.Summary = "상태 확인."
	reg := registerMenu(t, d)
	tree := reg.MenuBySection(nil)
	if got := tree.Count(); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	entry := tree.Sections[0].Entries[0]
	if entry.Entry.Kind != MenuRun || entry.Entry.Label != "스택 상태" || entry.Entry.Summary != "상태 확인." {
		t.Fatalf("implicit entry = %+v", entry.Entry)
	}
}

// A group heading is drawn at its own feature's position and its children follow
// it in Order order, regardless of registration order.
func TestMenuGroupsChildrenUnderTheirParent(t *testing.T) {
	git := menuDescriptor("generate-git-hook", SectionConfig, menuRun("generate-git-hook", "Git", 1, "hooks"))
	s3 := menuDescriptor("generate-s3-hook", SectionConfig, menuRun("generate-s3-hook", "S3", 2, "hooks"))
	hooks := menuDescriptor("hooks", SectionConfig,
		menuGroup("hooks", "호스트 스크립트"),
		menuRun("hooks", "커스텀", 0, "hooks"),
	)
	other := menuDescriptor("secrets", SectionConfig)
	reg := registerMenu(t, git, s3, hooks, other)

	tree := reg.MenuBySection(nil)
	entries := tree.Sections[0].Entries
	if len(entries) != 2 {
		t.Fatalf("top-level entries = %d, want the group and secrets", len(entries))
	}
	group := entries[0]
	if !group.Group || group.Entry.Label != "호스트 스크립트" {
		t.Fatalf("first entry is not the heading: %+v", group.Entry)
	}
	if len(group.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(group.Children))
	}
	var labels []string
	for _, child := range group.Children {
		labels = append(labels, child.Entry.Label)
	}
	if strings.Join(labels, "|") != "커스텀|Git|S3" {
		t.Fatalf("child order = %v", labels)
	}
	// The heading is not selectable, so the count is the children plus the
	// ungrouped feature.
	if got := tree.Count(); got != 4 {
		t.Fatalf("count = %d, want 4", got)
	}
}

// Hiding every child hides the heading too: a heading with nothing under it is
// not a heading.
func TestMenuDropsAGroupWithNoVisibleChildren(t *testing.T) {
	git := menuDescriptor("generate-git-hook", SectionConfig, menuRun("generate-git-hook", "Git", 1, "hooks"))
	hooks := menuDescriptor("hooks", SectionConfig,
		menuGroup("hooks", "호스트 스크립트"),
		menuRun("hooks", "커스텀", 0, "hooks"),
	)
	reg := registerMenu(t, git, hooks)
	tree := reg.MenuBySection(func(entry MenuEntry) bool {
		return entry.Parent == ""
	})
	// Every child and the heading are gone, and the section has no rows left.
	if len(tree.Sections) != 0 {
		t.Fatalf("a heading survived with no child: %+v", tree.Sections[0].Entries)
	}
	if got := tree.Count(); got != 0 {
		t.Fatalf("count = %d, want 0", got)
	}
}

// A broken declaration fails at registration instead of drawing a row in the
// wrong place.
func TestMenuValidationRejectsBrokenDeclarations(t *testing.T) {
	cases := map[string]Descriptor{
		"unknown kind": {
			Name: "status", Title: "상태", Summary: "요약", Usage: "dcraft status",
			Section: SectionInspect,
			Menu:    []MenuEntry{{Kind: "bogus", Feature: "status", Label: "라벨", Summary: "요약"}},
		},
		"empty label": {
			Name: "status", Title: "상태", Summary: "요약", Usage: "dcraft status",
			Section: SectionInspect,
			Menu:    []MenuEntry{{Kind: MenuRun, Feature: "status", Summary: "요약"}},
		},
		"other feature": {
			Name: "status", Title: "상태", Summary: "요약", Usage: "dcraft status",
			Section: SectionInspect,
			Menu:    []MenuEntry{{Kind: MenuRun, Feature: "logs", Label: "라벨", Summary: "요약"}},
		},
		"group with parent": {
			Name: "hooks", Title: "훅", Summary: "요약", Usage: "dcraft hooks",
			Section: SectionConfig,
			Menu:    []MenuEntry{{Kind: MenuGroup, Feature: "hooks", Label: "그룹", Summary: "요약", Parent: "hooks"}},
		},
		"empty summary": {
			Name: "status", Title: "상태", Summary: "요약", Usage: "dcraft status",
			Section: SectionInspect,
			Menu:    []MenuEntry{{Kind: MenuRun, Feature: "status", Label: "라벨"}},
		},
		"duplicate entry": {
			Name: "status", Title: "상태", Summary: "요약", Usage: "dcraft status",
			Section: SectionInspect,
			Menu: []MenuEntry{
				{Kind: MenuRun, Feature: "status", Label: "라벨", Summary: "요약"},
				{Kind: MenuRun, Feature: "status", Label: "다른 라벨", Summary: "요약"},
			},
		},
	}
	for name, d := range cases {
		if err := validateDescriptor(d); err == nil {
			t.Fatalf("%s: broken menu was accepted", name)
		}
	}
}

// The cross-feature half: a child naming a heading that does not exist, a child
// in another section, and a heading nobody names are all rejected.
func TestRegistryValidateRejectsBrokenLinks(t *testing.T) {
	cases := map[string][]Descriptor{
		"unknown parent": {
			menuDescriptor("git", SectionConfig, menuRun("git", "Git", 0, "hooks")),
		},
		"parent in another section": {
			menuDescriptor("hooks", SectionConfig, menuGroup("hooks", "그룹"), menuRun("hooks", "커스텀", 0, "hooks")),
			menuDescriptor("git", SectionInspect, menuRun("git", "Git", 0, "hooks")),
		},
		"group with no children": {
			menuDescriptor("hooks", SectionConfig, menuGroup("hooks", "그룹")),
		},
	}
	for name, descriptors := range cases {
		reg := NewRegistry()
		for i := range descriptors {
			reg.MustRegister(&menuFeature{desc: descriptors[i]})
		}
		if err := reg.Validate(); err == nil {
			t.Fatalf("%s: broken link was accepted", name)
		}
	}
}

// Entry order inside one parent is Order, then registration order, so a
// declaration stays stable without depending on file order.
func TestMenuOrderTiesBreakByRegistration(t *testing.T) {
	group := menuDescriptor("group", SectionConfig, menuGroup("group", "그룹"))
	first := menuDescriptor("first", SectionConfig, menuRun("first", "둘째", 1, "group"))
	second := menuDescriptor("second", SectionConfig, menuRun("second", "첫째", 0, "group"))
	third := menuDescriptor("third", SectionConfig, menuRun("third", "셋째", 1, "group"))
	reg := registerMenu(t, group, first, second, third)
	entries := reg.MenuBySection(nil).Sections[0].Entries
	if len(entries) != 1 || !entries[0].Group {
		t.Fatalf("top-level entries = %d, want one heading", len(entries))
	}
	var labels []string
	for _, child := range entries[0].Children {
		labels = append(labels, child.Entry.Label)
	}
	// Order 0 first, then the two Order 1 entries in registration order.
	if strings.Join(labels, "|") != "첫째|둘째|셋째" {
		t.Fatalf("child order = %v", labels)
	}
}
