package core_test

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// A descriptor without a section cannot be placed in the TUI navigation, so
// registration must reject it instead of silently defaulting.
func TestRegistryRejectsMissingSection(t *testing.T) {
	d := validDescriptor("status")
	d.Section = ""
	err := register(t, core.NewRegistry(), d)
	if err == nil || !strings.Contains(err.Error(), `unknown section ""`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryRejectsUnknownSection(t *testing.T) {
	d := validDescriptor("status")
	d.Section = core.Section("settings")
	err := register(t, core.NewRegistry(), d)
	if err == nil || !strings.Contains(err.Error(), `unknown section "settings"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSectionKoreanLabels(t *testing.T) {
	want := map[core.Section]string{
		core.SectionInspect: "조회",
		core.SectionOperate: "운영",
		core.SectionData:    "데이터",
		core.SectionConfig:  "설정",
	}
	for section, label := range want {
		if got := section.KoreanLabel(); got != label {
			t.Fatalf("%q label = %q, want %q", section, got, label)
		}
	}
	if got := core.Section("").KoreanLabel(); got != "" {
		t.Fatalf("empty section label = %q", got)
	}
}

func TestRegistryRejectsReadOnlyDestructiveFeature(t *testing.T) {
	d := validDescriptor("status")
	d.Destructive = true
	err := register(t, core.NewRegistry(), d)
	if err == nil || !strings.Contains(err.Error(), "read-only features cannot be destructive") {
		t.Fatalf("error = %v", err)
	}
}

// Destructive is independent of Approval: a mutating feature may destroy data
// without needing an approval, and an approval may be required without the
// operation being destructive.
func TestRegistryAcceptsDestructiveMutatingFeature(t *testing.T) {
	d := validDescriptor("down")
	d.ReadOnly = false
	d.RecordsHistory = true
	d.Destructive = true
	if err := register(t, core.NewRegistry(), d); err != nil {
		t.Fatalf("Register: %v", err)
	}
	plain := validDescriptor("install")
	plain.ReadOnly = false
	plain.Approval = core.ApprovalInstall
	if err := register(t, core.NewRegistry(), plain); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// BySection groups by the declared section in the fixed order and keeps
// registration order inside each group. All() itself is unchanged: the CLI and
// other callers still see one flat registration-ordered list.
func TestRegistryBySectionGroupsInFixedOrderPreservingRegistrationOrder(t *testing.T) {
	reg := core.NewRegistry()
	mk := func(name string, section core.Section) core.Descriptor {
		d := validDescriptor(name)
		d.Section = section
		return d
	}
	registered := []core.Descriptor{
		mk("status", core.SectionInspect),
		mk("down", core.SectionOperate),
		mk("backup", core.SectionData),
		mk("logs", core.SectionInspect),
		mk("up", core.SectionOperate),
		mk("init", core.SectionConfig),
	}
	for _, d := range registered {
		if err := register(t, reg, d); err != nil {
			t.Fatalf("Register %s: %v", d.Name, err)
		}
	}

	var flat []string
	for _, f := range reg.All() {
		flat = append(flat, f.Descriptor().Name)
	}
	want := []string{"status", "down", "backup", "logs", "up", "init"}
	for i := range want {
		if flat[i] != want[i] {
			t.Fatalf("All order = %v, want %v", flat, want)
		}
	}

	groups := reg.BySection()
	if len(groups) != 4 {
		t.Fatalf("groups = %d, want 4", len(groups))
	}
	sections := []core.Section{core.SectionInspect, core.SectionOperate, core.SectionData, core.SectionConfig}
	for i, wantSection := range sections {
		if groups[i].Section != wantSection {
			t.Fatalf("group %d = %q, want %q", i, groups[i].Section, wantSection)
		}
	}
	names := func(features []core.Feature) []string {
		out := make([]string, 0, len(features))
		for _, f := range features {
			out = append(out, f.Descriptor().Name)
		}
		return out
	}
	if got := names(groups[0].Features); len(got) != 2 || got[0] != "status" || got[1] != "logs" {
		t.Fatalf("inspect group = %v, want [status logs]", got)
	}
	if got := names(groups[1].Features); len(got) != 2 || got[0] != "down" || got[1] != "up" {
		t.Fatalf("operate group = %v, want [down up]", got)
	}
	if got := names(groups[3].Features); len(got) != 1 || got[0] != "init" {
		t.Fatalf("config group = %v, want [init]", got)
	}
}

// A section with no features is omitted rather than rendered as an empty
// heading, so adding and removing features stays a local change.
func TestRegistryBySectionOmitsEmptySections(t *testing.T) {
	reg := core.NewRegistry()
	d := validDescriptor("status")
	d.Section = core.SectionInspect
	if err := register(t, reg, d); err != nil {
		t.Fatal(err)
	}
	groups := reg.BySection()
	if len(groups) != 1 || groups[0].Section != core.SectionInspect {
		t.Fatalf("groups = %+v", groups)
	}
}

// The section order is a published contract; a caller must not be able to
// reorder it for the rest of the process.
func TestSectionsReturnsACopy(t *testing.T) {
	got := core.Sections()
	if len(got) != 4 {
		t.Fatalf("sections = %v", got)
	}
	got[0] = core.SectionConfig
	again := core.Sections()
	if again[0] != core.SectionInspect {
		t.Fatalf("caller mutated the contract order: %v", again)
	}
}
