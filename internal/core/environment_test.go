package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

type fakeEnvironmentResolver struct {
	fakeResolver
	environments []core.Environment
	diags        []core.Diagnostic
	err          error
	got          core.TargetRequest
	cwd          string
}

func (f *fakeEnvironmentResolver) Environments(ctx context.Context, req core.TargetRequest, cwd string) ([]core.Environment, []core.Diagnostic, error) {
	f.got = req
	f.cwd = cwd
	return f.environments, f.diags, f.err
}

func TestEnvironmentDiagnosticsMapToUsageExitCode(t *testing.T) {
	cases := map[core.Category]int{
		core.CategoryEnvironmentRequired: 2,
		core.CategoryEnvironmentUnknown:  2,
		core.CategoryEnvironmentConflict: 2,
		core.CategoryEnvironmentInvalid:  1,
	}
	for category, want := range cases {
		res := core.Result{
			Outcome: core.OutcomeFailure,
			Diagnostics: []core.Diagnostic{{
				Category: category, Severity: core.SeverityError, Message: "x",
			}},
		}
		if got := res.ExitCode(); got != want {
			t.Fatalf("category %q exit = %d, want %d", category, got, want)
		}
	}
}

func TestEngineReportsEnvironmentSupportFromTheResolver(t *testing.T) {
	plain := &fakeResolver{}
	e := core.NewEngine(core.NewRegistry(), plain, &fakePreflight{}, &fakeState{}, &fakeClock{})
	if e.SupportsEnvironments() {
		t.Fatal("plain resolver reported environment support")
	}
	envs, _, err := e.Environments(context.Background(), core.TargetRequest{Path: "/project"})
	if err != nil {
		t.Fatalf("Environments: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("environments = %+v, want none", envs)
	}

	withEnvs := &fakeEnvironmentResolver{environments: []core.Environment{{
		Name: "dev", Files: []string{"/project/compose.yaml"}, Profiles: []string{"dev"},
	}}}
	e = core.NewEngine(core.NewRegistry(), withEnvs, &fakePreflight{}, &fakeState{}, &fakeClock{})
	e.CWD = func() (string, error) { return "/cwd", nil }
	if !e.SupportsEnvironments() {
		t.Fatal("environment resolver not reported as supported")
	}
	envs, _, err = e.Environments(context.Background(), core.TargetRequest{Path: "/project"})
	if err != nil {
		t.Fatalf("Environments: %v", err)
	}
	if len(envs) != 1 || envs[0].Name != "dev" {
		t.Fatalf("environments = %+v", envs)
	}
	if withEnvs.cwd != "/cwd" || withEnvs.got.Path != "/project" {
		t.Fatalf("listing did not forward target/cwd: %+v cwd=%q", withEnvs.got, withEnvs.cwd)
	}
}

func TestEngineEnvironmentListingPropagatesConfigErrors(t *testing.T) {
	wantErr := core.Fail(core.Diagnostic{
		Category: core.CategoryEnvironmentInvalid, Severity: core.SeverityError, Message: "깨진 설정",
	})
	withEnvs := &fakeEnvironmentResolver{err: wantErr}
	e := core.NewEngine(core.NewRegistry(), withEnvs, &fakePreflight{}, &fakeState{}, &fakeClock{})
	e.CWD = func() (string, error) { return "/cwd", nil }
	_, _, err := e.Environments(context.Background(), core.TargetRequest{})
	if err == nil {
		t.Fatal("Environments swallowed the config error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
}

func TestEngineResolveFailureUsesEnvironmentExitCodeAndSkipsPreflight(t *testing.T) {
	reg := core.NewRegistry()
	ran := false
	status := &testFeature{
		desc: readOnlyDescriptor("status"),
		run: func(ctx context.Context, in core.Invocation) (any, error) {
			ran = true
			return nil, nil
		},
	}
	if err := reg.Register(status); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{err: core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentRequired,
		Severity:    core.SeverityError,
		Message:     "실행환경을 선택해야 합니다. 사용 가능한 실행환경: dev, prod",
		NextActions: []string{"--env로 지정하세요."},
	})}
	preflight := &fakePreflight{}
	engine := core.NewEngine(reg, resolver, preflight, &fakeState{}, &fakeClock{now: time.Now()})
	engine.CWD = func() (string, error) { return "/project", nil }

	res := engine.Run(context.Background(), core.Request{Feature: "status", Target: core.TargetRequest{Path: "/project"}}, nil)
	if res.Outcome != core.OutcomeFailure || res.ExitCode() != 2 {
		t.Fatalf("outcome=%q exit=%d, want failure/2", res.Outcome, res.ExitCode())
	}
	if preflight.called {
		t.Fatal("preflight ran before an environment selection failure")
	}
	if ran {
		t.Fatal("feature ran before an environment selection failure")
	}
}
