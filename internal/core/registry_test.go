package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type descriptorOnlyFeature struct{ desc core.Descriptor }

func (f descriptorOnlyFeature) Descriptor() core.Descriptor { return f.desc }
func (f descriptorOnlyFeature) Run(ctx context.Context, in core.Invocation) (any, error) {
	return nil, nil
}

func validDescriptor(name string) core.Descriptor {
	return core.Descriptor{
		Name:     name,
		Title:    "제목",
		Summary:  "요약",
		Usage:    "dcraft " + name,
		ReadOnly: true,
	}
}

func register(t *testing.T, reg *core.Registry, desc core.Descriptor) error {
	t.Helper()
	return reg.Register(descriptorOnlyFeature{desc: desc})
}

func TestRegistryAcceptsAndLooksUpFeatures(t *testing.T) {
	reg := core.NewRegistry()
	if err := register(t, reg, validDescriptor("status")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	f, ok := reg.Get("status")
	if !ok || f.Descriptor().Name != "status" {
		t.Fatalf("Get = %v, %v", f, ok)
	}
	if got := reg.All(); len(got) != 1 || got[0].Descriptor().Name != "status" {
		t.Fatalf("All = %v", got)
	}
}

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	reg := core.NewRegistry()
	if err := register(t, reg, validDescriptor("status")); err != nil {
		t.Fatal(err)
	}
	err := register(t, reg, validDescriptor("status"))
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestRegistryRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "Status", "two words", "-leading", "up_date", "a/b"} {
		reg := core.NewRegistry()
		if err := register(t, reg, validDescriptor(name)); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
}

func TestRegistryRejectsMissingUserFacingText(t *testing.T) {
	for _, mut := range []func(*core.Descriptor){
		func(d *core.Descriptor) { d.Title = "" },
		func(d *core.Descriptor) { d.Summary = "" },
		func(d *core.Descriptor) { d.Usage = "" },
	} {
		d := validDescriptor("status")
		mut(&d)
		reg := core.NewRegistry()
		if err := register(t, reg, d); err == nil {
			t.Fatalf("descriptor without text accepted: %+v", d)
		}
	}
}

func TestRegistryRejectsReadOnlyFeatureWithApproval(t *testing.T) {
	d := validDescriptor("status")
	d.Approval = core.ApprovalInstall
	reg := core.NewRegistry()
	if err := register(t, reg, d); err == nil {
		t.Fatal("read-only feature with approval accepted")
	}
}

func TestRegistryOwnsPrimarySelection(t *testing.T) {
	reg := core.NewRegistry()
	if _, ok := reg.Primary(); ok {
		t.Fatal("empty registry reported a primary feature")
	}
	primary := validDescriptor("status")
	primary.Primary = true
	if err := register(t, reg, primary); err != nil {
		t.Fatal(err)
	}
	if f, ok := reg.Primary(); !ok || f.Descriptor().Name != "status" {
		t.Fatalf("Primary = %v, %v", f, ok)
	}
	second := validDescriptor("again")
	second.Primary = true
	if err := register(t, reg, second); err == nil {
		t.Fatal("second primary feature accepted")
	}
}

func TestRegistryValidatesFlags(t *testing.T) {
	cases := []struct {
		name string
		flag core.Flag
	}{
		{"reserved shorthand", core.Flag{Name: "follow", Shorthand: "f", Kind: core.FlagBool}},
		{"invalid name", core.Flag{Name: "Follow", Kind: core.FlagBool}},
		{"reserved name", core.Flag{Name: "json", Kind: core.FlagBool}},
		{"reserved environment name", core.Flag{Name: "env", Kind: core.FlagString}},
		{"unknown kind", core.Flag{Name: "odd", Kind: core.FlagKind("odd")}},
		{"bad bool default", core.Flag{Name: "follow", Kind: core.FlagBool, Default: "maybe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor("logs")
			d.Flags = []core.Flag{tc.flag}
			reg := core.NewRegistry()
			if err := register(t, reg, d); err == nil {
				t.Fatalf("invalid flag accepted: %+v", tc.flag)
			}
		})
	}
}

func TestRegistryAcceptsFeatureFlagsAndArgs(t *testing.T) {
	d := validDescriptor("logs")
	d.ReadOnly = true
	d.AcceptsArgs = true
	d.SupportsServices = true
	d.Flags = []core.Flag{
		{Name: "follow", Shorthand: "l", Kind: core.FlagBool, Usage: "계속 출력"},
		{Name: "tail", Kind: core.FlagString, Default: "all", Usage: "마지막 줄 수"},
	}
	reg := core.NewRegistry()
	if err := register(t, reg, d); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestApprovalValuesAreIndependent(t *testing.T) {
	all := []core.ApprovalKind{
		core.ApprovalInstall,
		core.ApprovalScriptTrust,
		core.ApprovalPreviousGood,
		core.ApprovalDataRestore,
		core.ApprovalFileOverwrite,
	}
	seen := map[core.ApprovalKind]bool{}
	for _, a := range all {
		if seen[a] {
			t.Fatalf("approval kind duplicated: %q", a)
		}
		seen[a] = true
	}
	if strings.Join([]string{string(core.ApprovalInstall), string(core.ApprovalScriptTrust)}, ",") != "install,script_trust" {
		t.Fatal("approval kind machine values changed")
	}
}
