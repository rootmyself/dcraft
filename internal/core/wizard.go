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
	// Steps are answered in order.
	Steps []WizardStep `json:"steps"`
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
	// CustomHint describes the expected free-form value.
	CustomHint string `json:"custom_hint,omitempty"`
	// Default is preselected for a choice step or prefilled for an input step.
	Default string `json:"default,omitempty"`
}

// WizardOption is one selectable value. Value is machine data; Label is Korean
// user-facing text.
type WizardOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
