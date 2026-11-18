package rollback

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// Rollback requires its own approval, records history and declares no hook
// scope: the engine must never run host scripts for it.
func TestDescriptorIsApprovalGatedAndHookFree(t *testing.T) {
	f := New(nil)
	d := f.Descriptor()
	if d.Name != "rollback" {
		t.Fatalf("name = %q", d.Name)
	}
	if d.Approval != core.ApprovalPreviousGood {
		t.Fatalf("approval = %q, want previous_version", d.Approval)
	}
	if !d.RecordsHistory || d.ReadOnly {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.Hooks != core.HookNone {
		t.Fatalf("rollback declares hook scope %q; it must never run host scripts", d.Hooks)
	}
	if d.SupportsServices {
		t.Fatal("rollback restores the recorded deployment, not a service selection")
	}
}
