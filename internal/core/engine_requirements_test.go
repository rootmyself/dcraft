package core_test

import (
	"context"
	"testing"

	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

// A feature that declares only {compose} must still fail with the Docker CLI
// diagnostic on a host without Docker instead of silently passing preflight.
func TestEngineComposeRequirementFailsWhenDockerIsMissing(t *testing.T) {
	feature := &testFeature{
		desc: core.Descriptor{
			Name: "sample", Title: "샘플", Summary: "샘플입니다.", Usage: "dcraft sample",
			Section:  core.SectionInspect,
			Requires: core.Requirements{core.RequirementCompose},
		},
		run: func(context.Context, core.Invocation) (any, error) { return "ran", nil },
	}
	reg := core.NewRegistry()
	if err := reg.Register(feature); err != nil {
		t.Fatalf("Register: %v", err)
	}
	preflight := compose.New(&execxtest.Runner{LookPathErr: context.Canceled}, nil)
	engine := core.NewEngine(reg, &fakeResolver{target: core.Target{ProjectDir: "/project"}}, preflight, &fakeState{}, &fakeClock{})
	engine.CWD = func() (string, error) { return "/project", nil }
	engine.NewOperationID = func() string { return "op_test" }

	res := engine.Run(context.Background(), core.Request{Feature: "sample"}, nil)
	if res.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q, want failure", res.Outcome)
	}
	if !hasCategory(res, core.CategoryDockerMissing) {
		t.Fatalf("diagnostics = %+v, want docker_missing", res.Diagnostics)
	}
}
