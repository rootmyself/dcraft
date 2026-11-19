package core_test

import (
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// A narrower declaration must never disable the Docker CLI lookup. Compose and
// engine checks are meaningless without the CLI, so normalization makes the
// dependency explicit.
func TestNormalizeRequirementsImpliesDockerCLI(t *testing.T) {
	cases := []struct {
		name string
		in   core.Requirements
		want core.Requirements
	}{
		{
			name: "compose implies docker_cli",
			in:   core.Requirements{core.RequirementCompose},
			want: core.Requirements{core.RequirementDockerCLI, core.RequirementCompose},
		},
		{
			name: "docker_engine implies docker_cli",
			in:   core.Requirements{core.RequirementEngine},
			want: core.Requirements{core.RequirementDockerCLI, core.RequirementEngine},
		},
		{
			name: "compose and engine keep one docker_cli",
			in:   core.Requirements{core.RequirementCompose, core.RequirementDockerCLI, core.RequirementEngine},
			want: core.Requirements{core.RequirementCompose, core.RequirementDockerCLI, core.RequirementEngine},
		},
		{
			name: "docker_cli alone stays minimal",
			in:   core.Requirements{core.RequirementDockerCLI},
			want: core.Requirements{core.RequirementDockerCLI},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := core.NormalizeRequirements(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("requirements = %v, want %v", got, tc.want)
			}
			for _, want := range tc.want {
				if !got.Has(want) {
					t.Fatalf("requirements = %v, missing %q", got, want)
				}
			}
		})
	}
}

func TestNormalizeRequirementsKeepsFullDefaultForEmptyDeclaration(t *testing.T) {
	got := core.NormalizeRequirements(nil)
	for _, want := range []core.Requirement{
		core.RequirementDockerCLI, core.RequirementEngine, core.RequirementCompose,
	} {
		if !got.Has(want) {
			t.Fatalf("default requirements = %v, missing %q", got, want)
		}
	}
}

func TestNormalizeRequirementsDropsDuplicates(t *testing.T) {
	got := core.NormalizeRequirements(core.Requirements{
		core.RequirementCompose, core.RequirementCompose, core.RequirementDockerCLI,
	})
	if len(got) != 2 {
		t.Fatalf("requirements = %v, want one docker_cli and one compose", got)
	}
}
