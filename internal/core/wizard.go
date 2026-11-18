package core

// Wizard capability lets a feature offer a guided, keyboard-driven setup flow
// in the TUI without moving business rules into the UI. The feature still
// validates and saves through the same Run path as the CLI; the wizard only
// collects answers.
//
// The TUI asks the feature to run with Options["wizard"]="steps", renders the
// returned WizardSpec, then runs the feature again with the collected answers
// and Options["wizard"]="apply". A feature that implements WizardPreview can
// show the resulting effect before the user confirms.
type Wizard interface {
	// WizardPreview renders the effect of the collected answers. It is a pure
	// description and must not save anything or read secret values. An error
	// means the answers are not yet valid and the TUI keeps the user in the
	// flow instead of applying them.
	WizardPreview(answers map[string]string) (string, error)
}

// WizardSpec is the payload of a wizard "steps" run.
type WizardSpec struct {
	// Title is Korean guidance for the whole flow.
	Title string `json:"title"`
	// Steps are answered in order. A step may be conditional on an earlier
	// answer.
	Steps []WizardStep `json:"steps"`
	// RequiredApprovals are the independent approvals the final apply run
	// needs in addition to Descriptor.Approval. The TUI shows them on the
	// preview screen and passes exactly these kinds with the "apply" request;
	// the engine still enforces them. Empty means none.
	RequiredApprovals []ApprovalKind `json:"required_approvals,omitempty"`
}

// WizardStep is one question. Options are rendered as a keyboard-selectable
// list. A step with AllowCustom also accepts typed input.
type WizardStep struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	// Options are precomputed for the current target (for example Compose
	// service names).
	Options []WizardOption `json:"options,omitempty"`
	// AllowCustom enables typed input in addition to Options.
	AllowCustom bool `json:"allow_custom,omitempty"`
	// Multi renders Options as a checkbox list. Space toggles the highlighted
	// value; Enter confirms and joins the selected values with "\n". Enter with
	// nothing checked selects the highlighted option, preserving the simple
	// one-choice flow.
	Multi bool `json:"multi,omitempty"`
	// CustomHint describes the expected free-form value.
	CustomHint string `json:"custom_hint,omitempty"`
	// Default is preselected for a choice step or prefilled for an input step.
	Default string `json:"default,omitempty"`
	// Optional lets an input step accept an empty answer. A choice step
	// represents "none" with an empty option value instead.
	Optional bool `json:"optional,omitempty"`
	// DependsOn makes the step conditional on an earlier answer. The TUI skips
	// the step unless the named answer is one of Values.
	DependsOn *WizardCondition `json:"depends_on,omitempty"`
}

// WizardCondition selects a step by an earlier answer.
type WizardCondition struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// WizardOption is one selectable value. Value is machine data; Label is Korean
// user-facing text.
type WizardOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
