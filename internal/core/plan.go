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
