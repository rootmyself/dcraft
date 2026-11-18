package core

import "context"

// Planner is an optional feature capability for operations that need an
// independent approval (installing, script trust, rollback, data restore). The
// engine calls Plan before an interface collects the approval, so the user can
// see the exact effect first. Plan must not change anything: no downloads, no
// file writes, no stack mutation. It may read remote release metadata.
type Planner interface {
	Plan(ctx context.Context, in Invocation) (Plan, error)
}

// Plan is the human-readable preview of an approval-gated operation. Text is
// Korean and must state the concrete source, version, destination, permission
// need and effect. It never contains fetched secret values.
type Plan struct {
	Text string `json:"text"`
}

// PlanReport is the read-only outcome of Engine.Plan. It reuses the same
// target resolution, preflight requirements and stage model as Run, but it
// never checks or consumes an approval and never records history.
type PlanReport struct {
	Feature     string       `json:"feature"`
	Outcome     Outcome      `json:"outcome"`
	Target      Target       `json:"target"`
	Tools       ToolInfo     `json:"tools"`
	Stages      []Stage      `json:"stages"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Text        string       `json:"text,omitempty"`
}

// Failed reports whether this plan cannot be offered to the user.
func (r PlanReport) Failed() bool { return r.Outcome != OutcomeSuccess }

// Result converts a failed plan into the common result shape so both
// interfaces can render the same failure. A successful plan is a preview, not
// an operation result; callers use its Text before running the feature.
func (r PlanReport) Result() Result {
	outcome := r.Outcome
	if outcome == "" {
		outcome = OutcomeFailure
	}
	res := Result{
		SchemaVersion: SchemaVersion,
		Action:        r.Feature,
		Outcome:       outcome,
		Target:        r.Target,
		Tools:         r.Tools,
		Stages:        r.Stages,
		Diagnostics:   r.Diagnostics,
	}
	if res.Stages == nil {
		res.Stages = []Stage{}
	}
	if res.Diagnostics == nil {
		res.Diagnostics = []Diagnostic{}
	}
	res.Target.Normalize()
	return res
}
