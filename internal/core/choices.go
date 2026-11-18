package core

import "context"

// OptionChoice is one concrete value an interactive interface can offer for a
// feature flag. Value is what the feature receives; Label and Detail are
// Korean display text.
type OptionChoice struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// OptionChooser is an optional feature capability. Before such a feature runs,
// a UI may ask for the valid values of one of its declared flags. This is the
// same read-only discovery a plan uses; the CLI always takes explicit flag
// values instead.
type OptionChooser interface {
	Choices(ctx context.Context, in Invocation, flag string) ([]OptionChoice, error)
}

// ChoicesReport is the read-only outcome of Engine.Choices. It deliberately
// reuses the plan/run target and preflight path so interactive selection cannot
// address a different stack.
type ChoicesReport struct {
	Feature     string         `json:"feature"`
	Flag        string         `json:"flag"`
	Outcome     Outcome        `json:"outcome"`
	Target      Target         `json:"target"`
	Stages      []Stage        `json:"stages"`
	Diagnostics []Diagnostic   `json:"diagnostics"`
	Choices     []OptionChoice `json:"choices"`
}

// Failed reports whether the choices could not be read.
func (r ChoicesReport) Failed() bool { return r.Outcome != OutcomeSuccess }

// Result converts a failed lookup into the common result shape so both
// interfaces render the same failure.
func (r ChoicesReport) Result() Result {
	outcome := r.Outcome
	if outcome == "" {
		outcome = OutcomeFailure
	}
	res := Result{
		SchemaVersion: SchemaVersion,
		Action:        r.Feature,
		Outcome:       outcome,
		Target:        r.Target,
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
