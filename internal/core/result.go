package core

import (
	"context"
	"errors"
	"time"
)

// SchemaVersion is the machine-readable result schema version. Within one
// version, changes are additive; breaking changes bump the version.
const SchemaVersion = 1

// Outcome is the final state of an operation.
type Outcome string

const (
	OutcomeSuccess  Outcome = "success"
	OutcomeFailure  Outcome = "failure"
	OutcomeCanceled Outcome = "canceled"
)

// StageStatus is the state of one progress stage.
type StageStatus string

const (
	StageStarted   StageStatus = "started"
	StageSucceeded StageStatus = "succeeded"
	StageFailed    StageStatus = "failed"
	StageCanceled  StageStatus = "canceled"
	StageSkipped   StageStatus = "skipped"
)

// Stage is one ordered progress checkpoint. Name and Status are machine data;
// Title and Detail are Korean guidance.
type Stage struct {
	Name       string      `json:"name"`
	Title      string      `json:"title"`
	Status     StageStatus `json:"status"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
	Detail     string      `json:"detail,omitempty"`
}

// Sink receives stage transitions while an operation runs. JSON output never
// mixes these into the final result; a UI may render them live.
type Sink interface {
	StageChanged(Stage)
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(Stage)

func (f SinkFunc) StageChanged(s Stage) { f(s) }

// Reporter is what a feature uses to publish its own stages. Features should
// use RunStage so begin/end pairing cannot drift.
type Reporter interface {
	Begin(name, title string)
	End(name string, status StageStatus, detail string)
}

// RunStage wraps one feature stage that returns only an error. Cancellation is
// classified from the returned error.
func RunStage(r Reporter, name, title string, fn func() error) error {
	err := stageRun(r, name, title, fn)
	return err
}

// RunStageValue is RunStage for stages that produce a value.
func RunStageValue[T any](r Reporter, name, title string, fn func() (T, error)) (T, error) {
	var zero T
	var value T
	err := stageRun(r, name, title, func() error {
		v, err := fn()
		if err == nil {
			value = v
		}
		return err
	})
	if err != nil {
		return zero, err
	}
	return value, nil
}

func stageRun(r Reporter, name, title string, fn func() error) error {
	if r == nil {
		return fn()
	}
	r.Begin(name, title)
	err := fn()
	switch {
	case err == nil:
		r.End(name, StageSucceeded, "")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		r.End(name, StageCanceled, "")
	default:
		r.End(name, StageFailed, "")
	}
	return err
}

// OperationRecord is written to per-stack history for stack-changing features.
// It contains no resolved secret values.
type OperationRecord struct {
	OperationID string       `json:"operation_id"`
	Action      string       `json:"action"`
	Outcome     Outcome      `json:"outcome"`
	StartedAt   time.Time    `json:"started_at"`
	FinishedAt  time.Time    `json:"finished_at"`
	Stages      []Stage      `json:"stages"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Result is the single outcome contract shared by CLI and TUI. Target, Action,
// Stage and Outcome are separate from Diagnostics; progress is not embedded.
type Result struct {
	SchemaVersion    int          `json:"schema_version"`
	OperationID      string       `json:"operation_id"`
	Action           string       `json:"action"`
	Outcome          Outcome      `json:"outcome"`
	RequiredApproval ApprovalKind `json:"required_approval,omitempty"`
	Target           Target       `json:"target"`
	Tools            ToolInfo     `json:"tools"`
	StartedAt        time.Time    `json:"started_at"`
	FinishedAt       time.Time    `json:"finished_at"`
	Stages           []Stage      `json:"stages"`
	Diagnostics      []Diagnostic `json:"diagnostics"`
	Data             any          `json:"data,omitempty"`
}

// ExitCode maps an outcome to the stable process exit status:
//   - 0 success
//   - 1 operation/environment failure
//   - 2 missing required input or approval, or an invalid explicit selection
//   - 130 canceled by the caller
//
// project_name_conflict and service_unknown mean the caller must supply a
// different decision (a free name, an existing service); they are usage errors,
// not environment failures. dcraft never prompts its way out of them.
func (r Result) ExitCode() int {
	switch r.Outcome {
	case OutcomeSuccess:
		return 0
	case OutcomeCanceled:
		return 130
	default:
		for _, d := range r.Diagnostics {
			switch d.Category {
			case CategoryApprovalRequired,
				CategoryEnvironmentRequired, CategoryEnvironmentUnknown, CategoryEnvironmentConflict,
				CategoryProjectNameConflict, CategoryServiceUnknown,
				CategoryBackupOutputRequired, CategoryBackupVolumeRequired, CategoryBackupVolumeUnknown:
				return 2
			}
		}
		return 1
	}
}
