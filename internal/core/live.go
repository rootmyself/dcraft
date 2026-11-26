package core

// LiveMode classifies a feature whose normal output is a live process stream
// instead of a single result document.
type LiveMode string

const (
	// LiveStream is output-only following (logs). Input is not forwarded and
	// quitting the screen stops following without stopping services.
	LiveStream LiveMode = "stream"
	// LiveAttach wires terminal input to the child (exec). The interface may
	// forward keys and must restore its own screen afterwards.
	LiveAttach LiveMode = "attach"
)

// LiveFeature is an optional capability. A live feature's stdout is user data,
// not the result contract, so a machine-readable interface refuses to mix it
// with the final JSON object. The TUI renders it as a live screen and stops it
// by canceling the run context; services are never stopped by leaving a screen.
type LiveFeature interface {
	LiveMode() LiveMode
}

// ServiceScope is an optional capability for a feature whose work names
// services. The generic scope screen asks it whether a selection is required
// instead of deriving the answer from a feature name.
type ServiceScope interface {
	RequiresServiceScope() bool
	// MaxServices caps how many services one screen may collect. Zero means the
	// feature imposes no cap.
	MaxServices() int
}

// CanceledReporter is an optional capability for a feature whose cancellation
// has a specific, honest effect (a detached stream, an atomic install that is
// either absent or complete). The engine keeps the canceled outcome and exit
// 130 but reports this Korean diagnostic instead of the generic partial-effects
// text. Category and severity are normalized to canceled/warning.
type CanceledReporter interface {
	CanceledDiagnostic() Diagnostic
}
