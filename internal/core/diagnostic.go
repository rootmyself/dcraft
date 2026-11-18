// Package core defines the contract shared by every dcraft interface (CLI and
// TUI) and every feature: target inputs, diagnostics, the operation seam,
// progress/result/cancellation models, approval policy and feature
// registration. UI packages must not add safety rules that live here.
package core

import (
	"errors"
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
	// Data backup (ticket #16). The three *_required/*_unknown values are
	// missing or invalid caller input and map to exit code 2.
	CategoryBackupOutputRequired     Category = "backup_output_required"
	CategoryBackupVolumeRequired     Category = "backup_volume_required"
	CategoryBackupVolumeUnknown      Category = "backup_volume_unknown"
	CategoryBackupVolumeUnsupported  Category = "backup_volume_unsupported"
	CategoryBackupOutputInvalid      Category = "backup_output_invalid"
	CategoryBackupConsumerUnsafe     Category = "backup_consumer_unsafe"
	CategoryBackupFailed             Category = "backup_failed"
	CategoryBackupIntegrityFailed    Category = "backup_integrity_failed"
	CategoryBackupStateRestoreFailed Category = "backup_state_restore_failed"
	CategoryBackupSelectionChanged   Category = "backup_selection_changed"
	// Bind-folder backup (ticket #17). These are additive v1 categories.
	// backup_bind_unknown is invalid caller input (exit 2); bind_invalid and
	// bind_unsafe are operation failures (exit 1).
	CategoryBackupBindUnknown Category = "backup_bind_unknown"
	CategoryBackupBindInvalid Category = "backup_bind_invalid"
	CategoryBackupBindUnsafe  Category = "backup_bind_unsafe"
	// Data restore (ticket #18). restore_backup_required and
	// restore_volume_unknown are missing/invalid caller input (exit 2); the
	// rest are operation failures (exit 1). The values are additive.
	CategoryRestoreBackupRequired    Category = "restore_backup_required"
	CategoryRestoreVolumeUnknown     Category = "restore_volume_unknown"
	CategoryRestoreArtifactInvalid   Category = "restore_artifact_invalid"
	CategoryRestoreIntegrityFailed   Category = "restore_integrity_failed"
	CategoryRestoreTargetMismatch    Category = "restore_target_mismatch"
	CategoryRestoreTargetUnsupported Category = "restore_target_unsupported"
	CategoryRestoreTargetInvalid     Category = "restore_target_invalid"
	CategoryRestoreConsumerUnsafe    Category = "restore_consumer_unsafe"
	CategoryRestoreSafetyFailed      Category = "restore_safety_failed"
	CategoryRestoreFailed            Category = "restore_failed"
	CategoryRestoreVerifyFailed      Category = "restore_verification_failed"
	CategoryRestoreStateFailed       Category = "restore_state_restore_failed"
	CategoryStackBusy                Category = "stack_busy"
	// CategoryPlanStale means the selection changed after the plan was shown
	// (files, env files, .dcraft.yaml, name map or resolved identity). It is a
	// usage input problem (exit 2) and no stack change was made.
	CategoryPlanStale       Category = "plan_stale"
	CategoryLifecycleFailed Category = "lifecycle_failed"
	// Host-script generators (tickets #14/#15). generator_input_invalid is
	// missing or unsafe caller input (exit 2); generator_failed is a refused
	// or failed script-file write (exit 1). Neither category carries script
	// content or credentials.
	CategoryGeneratorInputInvalid Category = "generator_input_invalid"
	CategoryGeneratorFailed       Category = "generator_failed"
	// Host scripts (ticket #12). hook_invalid is a registration/file problem;
	// hook_failed is a pre-hook process failure that blocked the operation;
	// hook_content_changed means the approved content identity no longer
	// matches and a new approval is required; hook_post_failed is a warning
	// after completed work; hook_output carries redacted script output.
	CategoryHookInvalid        Category = "hook_invalid"
	CategoryHookInputInvalid   Category = "hook_input_invalid"
	CategoryHookFailed         Category = "hook_failed"
	CategoryHookContentChanged Category = "hook_content_changed"
	CategoryHookPostFailed     Category = "hook_post_failed"
	CategoryHookOutput         Category = "hook_output"
	// Service/instance selection and live terminal operations (ticket #5).
	// selection_required and instance_unknown mean the caller must supply a
	// different explicit selection; instance_stopped, logs_failed and
	// exec_failed are operation failures.
	CategorySelectionRequired Category = "selection_required"
	CategoryInstanceUnknown   Category = "instance_unknown"
	CategoryInstanceStopped   Category = "instance_stopped"
	CategoryLogsFailed        Category = "logs_failed"
	CategoryExecFailed        Category = "exec_failed"
	CategoryUpdateFailed      Category = "update_failed"
	CategoryReadinessFailed   Category = "readiness_failed"
	CategoryImageMissing      Category = "image_missing"
	CategoryInvalidOption     Category = "invalid_option"
	// Previous-good rollback (ticket 11). Additive categories: the values
	// and meanings are part of the machine contract.
	CategoryRollbackUnavailable       Category = "rollback_unavailable"
	CategoryRollbackRecordInvalid     Category = "rollback_record_invalid"
	CategoryRollbackInputMissing      Category = "rollback_input_missing"
	CategoryRollbackImageMissing      Category = "rollback_image_missing"
	CategoryRollbackSecretUnavailable Category = "rollback_secret_unavailable"
	CategoryRollbackFailed            Category = "rollback_failed"
	CategoryRollbackVerifyFailed      Category = "rollback_verify_failed"
	CategoryStateFailed               Category = "state_failed"
	CategoryVerificationFailed        Category = "verification_failed"
	CategoryCanceled                  Category = "canceled"
	CategoryHistoryFailed             Category = "history_failed"
	CategorySecretInvalid             Category = "secret_invalid"
	CategorySecretInputInvalid        Category = "secret_input_invalid"
	CategorySecretUnsupported         Category = "secret_unsupported"
	CategorySecretSource              Category = "secret_source_unavailable"
	CategorySecretApplyFailed         Category = "secret_apply_failed"
	// AWS Secrets Manager sources (ticket #10). The AWS CLI keeps the
	// credential chain outside dcraft; these categories separate a missing
	// tool, missing credentials/region, service authorization, an unknown
	// secret or field, a network failure and an unusable value. All are
	// operation failures (exit 1); only invalid reference syntax is input.
	CategorySecretAWSCLIMissing         Category = "secret_aws_cli_missing"
	CategorySecretAWSCredentialsMissing Category = "secret_aws_credentials_missing"
	CategorySecretAWSRegionMissing      Category = "secret_aws_region_missing"
	CategorySecretAWSAccessDenied       Category = "secret_aws_access_denied"
	CategorySecretAWSSecretNotFound     Category = "secret_aws_secret_not_found"
	CategorySecretAWSFieldInvalid       Category = "secret_aws_field_invalid"
	CategorySecretAWSNetwork            Category = "secret_aws_network"
	CategorySecretAWSValueInvalid       Category = "secret_aws_value_invalid"
	CategorySecretAWSFailed             Category = "secret_aws_failed"
	// CategoryBuildServiceInvalid means a build-phase secret reference names a
	// service without a Compose build section. The build would silently do
	// nothing, so it is a usage error (exit 2), never a silent skip.
	CategoryBuildServiceInvalid Category = "build_service_invalid"
	CategoryInternal            Category = "internal_error"
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
	// ExitCode is the external process status when this diagnostic describes
	// one (for example a failed `exec` command). It is additive: dcraft's own
	// process exit code stays the common 0/1/2/130 contract, so a tool's exit
	// 2 or 130 can never be confused with a dcraft usage or cancellation.
	ExitCode *int `json:"exit_code,omitempty"`
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

// AllDiagnostics returns this error's diagnostics plus diagnostics of nested
// *Error causes. A caller that wraps a classified error (for example the
// lifecycle controller wrapping a secret preparation error) keeps both the
// outer user-facing diagnostic and the specific machine category.
func (e *Error) AllDiagnostics() []Diagnostic {
	if e == nil {
		return nil
	}
	out := append([]Diagnostic{}, e.Diagnostics...)
	var nested *Error
	if errors.As(e.Cause, &nested) && nested != e {
		out = append(out, nested.AllDiagnostics()...)
	}
	return out
}

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
