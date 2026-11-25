package core

import "context"

// InputKind is the shape of one value the TUI collects before running a
// feature. It is presentation metadata; the feature still validates every
// value when it runs.
type InputKind string

const (
	InputChoice InputKind = "choice"
	InputText   InputKind = "text"
)

// InputOption is one selectable value.
type InputOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// InputField is one value a feature needs from an interactive interface. The
// CLI never prompts: it requires the equivalent flag or argument and fails with
// Korean guidance when it is missing.
type InputField struct {
	Key      string        `json:"key"`
	Label    string        `json:"label"`
	Help     string        `json:"help,omitempty"`
	Kind     InputKind     `json:"kind"`
	Options  []InputOption `json:"options,omitempty"`
	Required bool          `json:"required"`
	// DependsOn and DependsValue hide the field until another answered field
	// has the exact value. The same vocabulary as WizardStep.DependsOn: an
	// empty DependsOn always shows the field, and a hidden field contributes no
	// value and is not validated.
	DependsOn    string `json:"depends_on,omitempty"`
	DependsValue string `json:"depends_value,omitempty"`
}

// Visible reports whether the field is drawn and collected, using answer to read
// the value of the field it depends on. It is the one rule the renderer, the key
// handler and the submit path share, so a hidden field can never be drawn,
// skipped inconsistently or submitted by accident.
func (f InputField) Visible(answer func(key string) string) bool {
	if f.DependsOn == "" {
		return true
	}
	if answer == nil {
		return false
	}
	return answer(f.DependsOn) == f.DependsValue
}

// InputDependsOn is the dependency of one field expressed as a lookup. It is
// kept here so a caller that reads fields does not re-implement the rule.
func InputDependsOn(fields []InputField, key string) (InputField, bool) {
	for _, field := range fields {
		if field.Key == key {
			return field, true
		}
	}
	return InputField{}, false
}

// InputProvider is an optional feature capability for interactive input
// collection. WithInputs applies the collected values to a fresh request; the
// feature owns the mapping, so the UI never learns feature-specific field
// meanings. Both methods are read-only: Inputs must not change the stack.
type InputProvider interface {
	Inputs(ctx context.Context, in Invocation) ([]InputField, error)
	WithInputs(req Request, values map[string]string) (Request, error)
}

// InputsReport is the read-only outcome of Engine.Inputs. It uses the same
// target resolution, preflight requirements, stage model and result shape as
// Plan and Run, but never runs the feature, checks an approval or writes
// history.
type InputsReport struct {
	Feature     string       `json:"feature"`
	Outcome     Outcome      `json:"outcome"`
	Target      Target       `json:"target"`
	Tools       ToolInfo     `json:"tools"`
	Stages      []Stage      `json:"stages"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Fields      []InputField `json:"fields"`
}

// Failed reports whether the fields cannot be offered to the user.
func (r InputsReport) Failed() bool { return r.Outcome != OutcomeSuccess }

// Result converts a failed report into the common result shape so both
// interfaces render the same failure.
func (r InputsReport) Result() Result {
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
