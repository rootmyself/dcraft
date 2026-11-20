package core

// Stage names that belong to the shared operation seam rather than to a
// feature. They are the same machine values the engine emits, so a planned
// stage and an observed stage can be matched by name.
const (
	StageNameResolveTarget = "resolve_target"
	StageNamePreflight     = "preflight"
	StageNamePlan          = "plan"
	StageNameExecute       = "execute"
	StageNameVerify        = "verify"
	StageNameHooksPre      = "hooks_pre"
	StageNameHooksPost     = "hooks_post"
	StageNameRevalidate    = "revalidate_inputs"
	StageNameHistory       = "history"
)

// PlannedStages lists the shared stages one Run of the feature will pass
// through, in engine order. Entries carry a Korean title and no status: they
// have not started, so a renderer must not report them as completed or as a
// progress point until the sink delivers them.
//
// The feature's own work is a single placeholder (StageNameExecute). The engine
// emits that stage only when a feature publishes no stages of its own; a
// renderer therefore replaces the placeholder with whatever observed stages
// appear in its position (see the TUI timeline). The list is a plan, not a
// promise: host scripts run only when the hook plan has entries, and the
// history stage is skipped for a wizard's read-only steps run.
func PlannedStages(f Feature) []Stage {
	if f == nil {
		return nil
	}
	d := f.Descriptor()
	out := []Stage{
		planned(StageNameResolveTarget, msgStageResolveTarget),
		planned(StageNamePreflight, msgStagePreflight),
	}
	if d.Hooks != HookNone {
		out = append(out,
			planned(StageNameHooksPre, msgStageHooksPre),
			planned(StageNameRevalidate, msgStageRevalidateInput),
		)
	}
	out = append(out, planned(StageNameExecute, msgStageExecute))
	if _, ok := f.(Verifier); ok {
		out = append(out, planned(StageNameVerify, msgStageVerify))
	}
	if d.Hooks != HookNone {
		out = append(out, planned(StageNameHooksPost, msgStageHooksPost))
	}
	if d.RecordsHistory {
		out = append(out, planned(StageNameHistory, msgStageHistory))
	}
	return out
}

// PlannedPlanStages lists the shared stages one Plan of the feature will pass
// through. A plan resolves the target and checks the environment before it asks
// the feature for its preview; the plan stage is itself a feature fallback and
// is replaced the same way a renderer replaces the execute placeholder.
func PlannedPlanStages(f Feature) []Stage {
	if f == nil {
		return nil
	}
	out := []Stage{
		planned(StageNameResolveTarget, msgStageResolveTarget),
		planned(StageNamePreflight, msgStagePreflight),
	}
	if _, ok := f.(Planner); ok {
		out = append(out, planned(StageNamePlan, msgStagePlan))
	}
	return out
}

// planned builds one not-yet-started stage. Status stays empty so "planned" can
// never be confused with StageStarted, StageSkipped or a completed status.
func planned(name, title string) Stage {
	return Stage{Name: name, Title: title}
}

// IsSharedStage reports whether name belongs to the shared seam rather than to a
// feature. A feature's own stages replace the shared execute/plan placeholder.
func IsSharedStage(name string) bool {
	switch name {
	case StageNameResolveTarget, StageNamePreflight, StageNamePlan, StageNameExecute,
		StageNameVerify, StageNameHooksPre, StageNameHooksPost, StageNameRevalidate,
		StageNameHistory:
		return true
	}
	return false
}
