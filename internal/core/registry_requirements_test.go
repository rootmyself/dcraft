package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type requirementFeature struct {
	desc core.Descriptor
}

func (f *requirementFeature) Descriptor() core.Descriptor { return f.desc }
func (f *requirementFeature) Run(context.Context, core.Invocation) (any, error) {
	return nil, nil
}

func TestRegistryRejectsUnknownPreflightRequirement(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "sample", Title: "샘플", Summary: "샘플입니다.", Usage: "dcraft sample",
		ReadOnly: true,
		Requires: core.Requirements{"docker"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unknown preflight requirement") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryAcceptsDeclaredRequirements(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "sample", Title: "샘플", Summary: "샘플입니다.", Usage: "dcraft sample",
		Approval: core.ApprovalInstall,
		Requires: core.Requirements{core.RequirementDockerCLI},
	}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
}
