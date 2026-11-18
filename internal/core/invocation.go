package core

import "time"

// Clock is injected so stage timestamps and history are testable.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// StateStore owns dcraft's per-computer state. The state directory defaults to
// the OS user config dir plus "dcraft" and can be overridden with
// DCRAFT_STATE_DIR. Layout is per stack and per environment:
//
//	<state>/stacks/<stack-key>/
//	    history.jsonl   (operation history; later tickets add more files)
//	    state.json      (reserved for name allocation and good-version state)
type StateStore interface {
	// StackDir returns the directory for a target (folder + environment).
	StackDir(target Target) (string, error)
	// AppendOperation appends one operation record to stack history.
	AppendOperation(target Target, rec OperationRecord) error
}

// Request is a common operation request from either interface.
type Request struct {
	Feature string
	Target  TargetRequest
	// Services explicitly selects Compose services; empty means the stack.
	Services []string
	// Args are positional arguments for features that accept them.
	Args []string
	// Options are parsed feature-owned flag values.
	Options map[string]string
	// OptionLists are parsed repeatable-string flag values. They are kept
	// separate so values are never split or joined by the UI layer.
	OptionLists map[string][]string
	// Approvals are explicit approvals supplied by the caller. The engine,
	// not the UI, decides whether they are sufficient.
	Approvals []ApprovalKind
	// ApprovedHooks is the hook plan the caller showed the user and received
	// approval for. When present, script_trust only covers those exact path,
	// stage and content identities; a changed script needs a new approval.
	// An empty slice means the caller supplied the approval kind without a
	// specific plan (for example JSON automation after checking
	// `dcraft hooks --list`).
	ApprovedHooks []HookEntry
	// PlanFingerprint is the PlanReport.Fingerprint of the preview the caller
	// showed and the user approved. CLI and TUI always pass it when a plan was
	// displayed; Run refuses a changed selection with plan_stale (exit 2)
	// before any change. Empty means no preview was shown (direct JSON
	// automation), so there is nothing to bind.
	PlanFingerprint string
	// Interactive reports whether prompts are possible. It never bypasses an
	// approval requirement.
	Interactive bool
	// IO carries the caller's live terminal streams for a feature that
	// attaches to a process. Nil fields mean the process standard streams.
	IO IO
	// JSON is a UI hint: the caller asked for a single machine-readable
	// result. It never changes safety rules; a live feature uses it only to
	// choose capture mode over streaming.
	JSON bool
}

// Invocation is what a feature receives after the engine resolved the target,
// checked approvals and completed preflight.
type Invocation struct {
	Target   Target
	Services []string
	Args     []string
	Options  map[string]string
	// Approvals are the explicit approvals the caller supplied. The engine
	// enforces approval policy before Run; a feature that must re-check a
	// condition at its own save time (for example a file that appeared after
	// the approval inspection) reads the same list here. It never bypasses the
	// engine.
	Approvals   []ApprovalKind
	OptionLists map[string][]string
	Interactive bool
	Preflight   PreflightReport
	IO          IO
	JSON        bool
	Reporter    Reporter
	Clock       Clock
	State       StateStore
}

// Option returns a feature flag value, or "" when absent.
func (in Invocation) Option(name string) string { return in.Options[name] }

// OptionList returns the values of a repeatable feature flag.
func (in Invocation) OptionList(name string) []string { return in.OptionLists[name] }

// BoolOption reads a bool feature flag.
func (in Invocation) BoolOption(name string) bool {
	switch in.Options[name] {
	case "true", "1":
		return true
	default:
		return false
	}
}
