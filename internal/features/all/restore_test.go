package all

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
)

func TestRestoreRegistersExactlyOnceWithIndependentApprovalAndNoHooks(t *testing.T) {
	reg := core.NewRegistry()
	Register(reg, &deps.Deps{})
	var matches []core.Feature
	for _, f := range reg.All() {
		if f.Descriptor().Name == "restore" {
			matches = append(matches, f)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("restore registrations = %d, want 1", len(matches))
	}
	desc := matches[0].Descriptor()
	if desc.Approval != core.ApprovalDataRestore {
		t.Fatalf("approval = %q, want data_restore", desc.Approval)
	}
	if desc.Hooks != core.HookNone {
		t.Fatalf("restore declares hooks: %v", desc.Hooks)
	}
	if !desc.Wizard {
		t.Fatal("restore must use the guided TUI flow")
	}
	if !desc.RecordsHistory {
		t.Fatal("restore must record stack history")
	}
}
