package all

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/deps"
)

// registeredFeatures is the real registry as the application assembles it.
func registeredFeatures(t *testing.T) []core.Feature {
	t.Helper()
	reg := core.NewRegistry()
	Register(reg, &deps.Deps{})
	return reg.All()
}

// The spec fixes which section every feature appears in. Inside a section the
// order is registration order, which is why these lists are not the visual
// order of the spec table.
func TestEveryFeatureDeclaresTheIntendedSection(t *testing.T) {
	want := map[core.Section][]string{
		core.SectionInspect: {"exec", "logs", "status"},
		core.SectionOperate: {"down", "restart", "stop", "up", "update"},
		core.SectionData:    {"backup", "restore", "rollback"},
		core.SectionConfig: {
			"environments", "generate-git-hook", "hooks", "install-compose",
			"generate-s3-hook", "secrets", "init",
		},
	}
	features := registeredFeatures(t)
	if len(features) != 18 {
		t.Fatalf("registered features = %d, want 18", len(features))
	}
	for section, names := range want {
		var got []string
		for _, f := range features {
			if f.Descriptor().Section == section {
				got = append(got, f.Descriptor().Name)
			}
		}
		if len(got) != len(names) {
			t.Fatalf("%s section = %v, want %v", section, got, names)
		}
		for i := range names {
			if got[i] != names[i] {
				t.Fatalf("%s section = %v, want %v", section, got, names)
			}
		}
	}
	for _, f := range features {
		if f.Descriptor().Section == "" {
			t.Fatalf("%s has no section", f.Descriptor().Name)
		}
	}
}

// Exactly the three irreversible actions carry the risk badge. Everything else
// is safe enough to render without one, including the approval-gated
// file-overwrite and install features: approval and destruction are separate
// channels.
func TestExactlyThreeFeaturesAreDestructive(t *testing.T) {
	want := map[string]bool{"down": true, "restore": true, "rollback": true}
	for _, f := range registeredFeatures(t) {
		d := f.Descriptor()
		if d.Destructive != want[d.Name] {
			t.Fatalf("%s destructive = %v, want %v", d.Name, d.Destructive, want[d.Name])
		}
		if d.Destructive && d.ReadOnly {
			t.Fatalf("%s is read-only and destructive", d.Name)
		}
	}
}

// Registration order is a published contract: the CLI usage text and the
// e2e menu walk depend on it. Grouping is a view, not a reordering.
func TestRegistrationOrderIsUnchanged(t *testing.T) {
	want := []string{
		"backup", "down", "environments", "exec", "generate-git-hook", "hooks",
		"install-compose", "logs", "restart", "restore", "rollback",
		"generate-s3-hook", "secrets", "status", "stop", "up", "update", "init",
	}
	features := registeredFeatures(t)
	if len(features) != len(want) {
		t.Fatalf("registered features = %d, want %d", len(features), len(want))
	}
	for i, name := range want {
		if got := features[i].Descriptor().Name; got != name {
			t.Fatalf("position %d = %q, want %q", i, got, name)
		}
	}
}
