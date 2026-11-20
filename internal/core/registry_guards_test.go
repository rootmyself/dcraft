package core_test

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func TestRegistryRejectsReadOnlyFeatureThatRecordsHistory(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "sample", Title: "샘플", Summary: "샘플입니다.", Usage: "dcraft sample",
		Section:  core.SectionInspect,
		ReadOnly: true, RecordsHistory: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryRejectsMutatingPrimaryWithoutApproval(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "sample", Title: "샘플", Summary: "샘플입니다.", Usage: "dcraft sample",
		Section: core.SectionInspect,
		Primary: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryAcceptsReadOnlyPrimary(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "status", Title: "상태", Summary: "상태입니다.", Usage: "dcraft status",
		Section:  core.SectionInspect,
		ReadOnly: true, Primary: true,
	}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRegistryAcceptsMutatingPrimaryWithApproval(t *testing.T) {
	reg := core.NewRegistry()
	err := reg.Register(&requirementFeature{desc: core.Descriptor{
		Name: "restore", Title: "복원", Summary: "복원합니다.", Usage: "dcraft restore",
		Section: core.SectionData,
		Primary: true, Approval: core.ApprovalDataRestore,
	}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
}
