package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func plannedNames(stages []core.Stage) string {
	names := make([]string, 0, len(stages))
	for _, st := range stages {
		names = append(names, st.Name)
	}
	return strings.Join(names, ",")
}

func TestPlannedStagesFollowTheEngineSequence(t *testing.T) {
	readOnly := &testFeature{desc: readOnlyDescriptor("status")}
	if got, want := plannedNames(core.PlannedStages(readOnly)), "resolve_target,preflight,execute"; got != want {
		t.Fatalf("read-only sequence = %q, want %q", got, want)
	}

	hooked := &testFeature{desc: core.Descriptor{
		Name: "up", Title: "스택 시작", Summary: "스택을 시작합니다.", Usage: "dcraft up",
		RecordsHistory: true, Hooks: core.HookUpUpdate,
	}}
	if got, want := plannedNames(core.PlannedStages(hooked)),
		"resolve_target,preflight,hooks_pre,revalidate_inputs,execute,hooks_post,history"; got != want {
		t.Fatalf("hook/history sequence = %q, want %q", got, want)
	}

	verifying := &verifyingFeature{
		testFeature: &testFeature{desc: readOnlyDescriptor("status")},
		verify:      func(context.Context, core.Invocation, any) error { return nil },
	}
	if got, want := plannedNames(core.PlannedStages(verifying)), "resolve_target,preflight,execute,verify"; got != want {
		t.Fatalf("verifying sequence = %q, want %q", got, want)
	}
}

// The preview must use the engine's own Korean titles, never a UI-rewritten
// wording, and it must never claim a status: a planned stage has not run.
func TestPlannedStagesCarryTheEngineTitlesAndNoStatus(t *testing.T) {
	stages := core.PlannedStages(&testFeature{desc: readOnlyDescriptor("status")})
	if len(stages) != 3 {
		t.Fatalf("stages = %d", len(stages))
	}
	for _, want := range []string{"대상 확인", "환경 확인", "작업 실행"} {
		found := false
		for _, st := range stages {
			if st.Title == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("planned stage titles missing %q: %+v", want, stages)
		}
	}
	for _, st := range stages {
		if st.Status != "" {
			t.Fatalf("planned stage %q carries status %q", st.Name, st.Status)
		}
	}
}

func TestPlannedStagesOfNoFeatureIsEmpty(t *testing.T) {
	if got := core.PlannedStages(nil); len(got) != 0 {
		t.Fatalf("nil feature planned %+v", got)
	}
}
