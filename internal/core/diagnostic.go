// Package core defines the contract shared by every dcraft interface (CLI and
// TUI) and every feature: target inputs, diagnostics, the operation seam,
// progress/result/cancellation models, approval policy and feature
// registration. UI packages must not add safety rules that live here.
package core

import (
	"strings"
)

// Category identifies a diagnostic in a stable, machine-readable form. The
// string values are part of the published JSON contract; add new categories,
// never rename existing ones.
type Category string

const (
	CategoryDockerMissing         Category = "docker_missing"
	CategoryEngineUnreachable     Category = "engine_unreachable"
	CategoryEnginePermission      Category = "engine_permission"
	CategoryRemoteEngine          Category = "remote_engine"
	CategoryEngineEndpointUnknown Category = "engine_endpoint_unknown"
	CategoryComposeMissing        Category = "compose_missing"
	CategoryComposeUnsupported    Category = "compose_unsupported"
	// Compose installation (ticket #7). These are additive categories; the
	// values and meanings are part of the machine contract.
	CategoryComposeReleaseUnavailable Category = "compose_release_unavailable"
	CategoryComposeInstallManual      Category = "compose_install_manual"
	CategoryComposeInstallExists      Category = "compose_install_exists"
	CategoryComposeDownloadFailed     Category = "compose_download_failed"
	CategoryComposeIntegrityUnavail   Category = "compose_integrity_unavailable"
	CategoryComposeIntegrityMismatch  Category = "compose_integrity_mismatch"
	CategoryComposeArtifactInvalid    Category = "compose_artifact_invalid"
	CategoryComposeArchMismatch       Category = "compose_arch_mismatch"
	CategoryComposeInstallPermission  Category = "compose_install_permission"
	CategoryComposeInstallFailed      Category = "compose_install_failed"
	CategoryComposeRecheckFailed      Category = "compose_recheck_failed"
	CategoryNoConfiguration           Category = "no_configuration"
	CategoryInvalidConfiguration      Category = "invalid_configuration"
	CategoryTargetInvalid             Category = "target_invalid"
	CategoryMultipleConfigFiles       Category = "multiple_configuration_files"
	CategoryApprovalRequired          Category = "approval_required"
	CategoryEnvironmentRequired       Category = "environment_required"
	CategoryEnvironmentUnknown        Category = "environment_unknown"
	CategoryEnvironmentInvalid        Category = "environment_invalid"
	CategoryEnvironmentConflict       Category = "environment_input_conflict"
	CategoryEnvironmentLimits         Category = "environment_isolation_limits"
	CategoryEnvironmentOverlap        Category = "environment_overlap"
	CategoryProjectNameShared         Category = "project_name_shared"
	CategoryProjectNameConflict       Category = "project_name_conflict"
	CategoryIdentityUnreadable        Category = "identity_unreadable"
	CategoryServiceUnknown            Category = "service_unknown"
	CategoryStackBusy                 Category = "stack_busy"
	CategoryLifecycleFailed           Category = "lifecycle_failed"
	CategoryUpdateFailed              Category = "update_failed"
	CategoryReadinessFailed           Category = "readiness_failed"
	CategoryImageMissing              Category = "image_missing"
	CategoryInvalidOption             Category = "invalid_option"
	CategoryStateFailed               Category = "state_failed"
	CategoryVerificationFailed        Category = "verification_failed"
	CategoryCanceled                  Category = "canceled"
	CategoryHistoryFailed             Category = "history_failed"
	CategoryInternal                  Category = "internal_error"
)

// Severity separates blocking problems from information that still allows the
// operation to proceed.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is a user-facing problem or warning. Message and NextActions are
// Korean guidance for humans; Category, Severity and Detail are stable machine
// data. Detail carries tool output, never resolved secret values.
type Diagnostic struct {
	Category    Category `json:"category"`
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Detail      string   `json:"detail,omitempty"`
	NextActions []string `json:"next_actions,omitempty"`
}

// Error carries one or more diagnostics through Go's error interface.
type Error struct {
	Diagnostics []Diagnostic
	Cause       error
}

func (e *Error) Error() string {
	if len(e.Diagnostics) == 0 {
		if e.Cause != nil {
			return e.Cause.Error()
		}
		return "dcraft error"
	}
	var parts []string
	for _, d := range e.Diagnostics {
		parts = append(parts, string(d.Category)+": "+d.Message)
	}
	msg := strings.Join(parts, "; ")
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Cause }

// Fail builds an error from diagnostics without an underlying cause.
func Fail(diags ...Diagnostic) *Error { return &Error{Diagnostics: diags} }

// FailCause builds an error from diagnostics and an underlying cause.
func FailCause(cause error, diags ...Diagnostic) *Error {
	return &Error{Diagnostics: diags, Cause: cause}
}

// HasError reports whether any diagnostic blocks the operation.
func HasError(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}
