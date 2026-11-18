package core_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type flagFeature struct {
	desc core.Descriptor
}

func (f *flagFeature) Descriptor() core.Descriptor { return f.desc }

func (f *flagFeature) Run(ctx context.Context, in core.Invocation) (any, error) { return nil, nil }

// A repeatable-string flag is a supported descriptor flag. Ticket #16 adds it
// so multi-volume selection does not need a Core change per feature.
func TestRegistryAcceptsARepeatableStringFlag(t *testing.T) {
	reg := core.NewRegistry()
	desc := core.Descriptor{
		Name: "backup", Title: "백업", Summary: "요약", Usage: "dcraft backup",
		Flags: []core.Flag{
			{Name: "output", Shorthand: "o", Kind: core.FlagString, Usage: "출력 폴더"},
			{Name: "volume", Kind: core.FlagList, Usage: "볼륨 키"},
		},
	}
	if err := reg.Register(&flagFeature{desc: desc}); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRegistryStillRejectsAnUnsupportedFlagKind(t *testing.T) {
	reg := core.NewRegistry()
	desc := core.Descriptor{
		Name: "backup", Title: "백업", Summary: "요약", Usage: "dcraft backup",
		Flags: []core.Flag{{Name: "volume", Kind: core.FlagKind("map"), Usage: "볼륨"}},
	}
	if err := reg.Register(&flagFeature{desc: desc}); err == nil {
		t.Fatal("unsupported flag kind was accepted")
	}
}

func TestInvocationReadsOptionLists(t *testing.T) {
	in := core.Invocation{OptionLists: map[string][]string{"volume": {"data", "logs"}}}
	if got := in.OptionList("volume"); len(got) != 2 || got[0] != "data" || got[1] != "logs" {
		t.Fatalf("OptionList = %v", got)
	}
	if got := in.OptionList("missing"); got != nil {
		t.Fatalf("missing OptionList = %v", got)
	}
}
