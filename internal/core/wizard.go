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
	// preview screen and, when non-empty, routes the apply through the generic
	// plan + confirmation screen so the engine collects exactly these kinds;
	// the feature must implement core.Planner. The engine still enforces them.
	// Empty means none.
	RequiredApprovals []ApprovalKind `json:"required_approvals,omitempty"`
	// EnvironmentAnswer names the step whose answer becomes the current
	// environment after a successful apply. It lets the TUI select the newly
	// issued alias without knowing the feature or its step names. Empty means
	// the apply does not issue an environment.
	EnvironmentAnswer string `json:"environment_answer,omitempty"`
	// Notice is a warning the flow must show before its first question, such
	// as an existing configuration a save would replace. It is the feature's
	// fact, not the screen's guess, and an empty value means no warning.
	Notice string `json:"notice,omitempty"`
	// NoticeConfirm is how a wizard with a Notice asks to continue. Empty means
	// the notice is informational and the first step follows directly.
	NoticeConfirm bool `json:"notice_confirm,omitempty"`
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
	// List collects ordered, newline-separated free-form items.
	List bool `json:"list,omitempty"`
	// CustomHint describes the expected free-form value.
	CustomHint string `json:"custom_hint,omitempty"`
	// Default is preselected for a choice step or prefilled for an input step.
	Default string `json:"default,omitempty"`
	// Optional allows an empty custom answer. The answer is stored as an
	// empty string and the flow advances; it is used for optional selectors
	// such as an AWS field/region/profile or init runtime fields.
	Optional bool `json:"optional,omitempty"`
	// DependsOn and DependsValue hide the step until another answered step
	// has the exact value. Empty DependsOn always shows the step. Hidden
	// steps are skipped by navigation and do not contribute an answer.
	DependsOn    string `json:"depends_on,omitempty"`
	DependsValue string `json:"depends_value,omitempty"`
}

// WizardOption is one selectable value. Value is machine data; Label is Korean
// user-facing text.
type WizardOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
