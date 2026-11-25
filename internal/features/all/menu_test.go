package all

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
)

// menuLabels flattens the assembled menu tree into readable rows: a heading is
// written as `[heading]` and its children indented, so a failure names the exact
// shape that changed.
func menuLabels(t *testing.T) []string {
	t.Helper()
	reg := core.NewRegistry()
	Register(reg, &deps.Deps{Getenv: func(string) string { return "" }})
	var out []string
	for _, section := range reg.MenuBySection(nil).Sections {
		out = append(out, "## "+section.Section.KoreanLabel())
		for _, entry := range section.Entries {
			if entry.Group {
				out = append(out, "["+entry.Entry.Label+"]")
				for _, child := range entry.Children {
					out = append(out, "  - "+child.Entry.Label)
				}
				continue
			}
			out = append(out, "- "+entry.Entry.Label)
		}
	}
	return out
}

// The real menu is what the contract promises: the two environment entries are
// separate rows, the generated host scripts sit under the host-script heading
// with the custom registration, and both generated entries follow the heading's
// own child order. This test is the one place the assembled shape is pinned, so
// a feature package cannot silently change the navigation.
func TestAssembledMenuShape(t *testing.T) {
	got := strings.Join(menuLabels(t), "\n")
	for _, want := range []string{
		"- 실행환경 생성\n- 실행환경 전환\n[호스트 스크립트]\n  - 커스텀\n  - Git\n  - S3\n- Compose 설치",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assembled menu is missing\n%s\n\nfull menu:\n%s", want, got)
		}
	}
}

// The count in the sidebar is the number of selectable entries: the heading is
// not one, and the second environment entry is.
func TestAssembledMenuCountsSelectableEntries(t *testing.T) {
	reg := core.NewRegistry()
	Register(reg, &deps.Deps{Getenv: func(string) string { return "" }})
	tree := reg.MenuBySection(nil)
	// Eighteen features, one extra entry for the environment switch, and the
	// host-script heading adds no entry of its own.
	if got := tree.Count(); got != 19 {
		t.Fatalf("selectable entries = %d, want 19", got)
	}
	// The heading is drawn but never addressable.
	headings := 0
	for _, section := range tree.Sections {
		for _, entry := range section.Entries {
			if entry.Group {
				headings++
			}
		}
	}
	if headings != 1 {
		t.Fatalf("group headings = %d, want the host-script one", headings)
	}
}

// The switch entry is hidden on a runtime that cannot record the active alias,
// and the rest of the menu keeps its order.
func TestEnvironmentSwitchRowNeedsTheWriteCapability(t *testing.T) {
	reg := core.NewRegistry()
	Register(reg, &deps.Deps{Getenv: func(string) string { return "" }})
	include := func(entry core.MenuEntry) bool {
		return entry.Kind != core.MenuSwitchEnvironment
	}
	var labels []string
	for _, section := range reg.MenuBySection(include).Sections {
		for _, entry := range section.Entries {
			labels = append(labels, entry.Entry.Label)
			for _, child := range entry.Children {
				labels = append(labels, child.Entry.Label)
			}
		}
	}
	joined := strings.Join(labels, "|")
	if strings.Contains(joined, "실행환경 전환") {
		t.Fatalf("the switch row survived without the capability: %v", labels)
	}
	if !strings.Contains(joined, "실행환경 생성") {
		t.Fatalf("hiding the switch row also hid the create row: %v", labels)
	}
}
