package core

import (
	"context"
	"time"
)

// HookScope declares which stack operations may run registered host scripts.
// The zero value means "never"; hooks are only attached to operations that
// explicitly declare the scope. There is deliberately no filename convention.
type HookScope string

const (
	// HookNone means the operation never runs host hooks (the default).
	HookNone HookScope = ""
	// HookUpUpdate attaches explicitly registered host scripts before and
	// after `up` and `update` only. It must never be set on read-only,
	// inspection, stop, backup, restore or rollback features.
	HookUpUpdate HookScope = "up_update"
)

// HookStage is the execution point of one registered host script.
type HookStage string

const (
	// HookStagePre runs after preflight and before the operation's own work.
	HookStagePre HookStage = "pre"
	// HookStagePost runs after the operation's work and verification succeed.
	HookStagePost HookStage = "post"
)

// HookEntry is one script that will actually run, with its approved content
// identity. Path is the project configuration value; SHA256 is the full
// lowercase content digest.
type HookEntry struct {
	Path    string    `json:"path"`
	Stage   HookStage `json:"stage"`
	SHA256  string    `json:"sha256"`
	Trusted bool      `json:"trusted"`
	// Changed means a previous approval exists for this path and stage with a
	// different content digest, so the old approval cannot be reused.
	Changed bool `json:"changed,omitempty"`
}

// HookPlan is the read-only list of registered scripts for one operation. It
// contains no output and no secret values.
type HookPlan struct {
	Entries []HookEntry `json:"entries"`
	// RequiresApproval is true when at least one entry is new or changed and
	// therefore needs the independent script_trust approval.
	RequiresApproval bool `json:"requires_approval"`
}

// HookRequest is what the hook service needs to execute one approved entry.
type HookRequest struct {
	Action      string
	OperationID string
	Target      Target
	Entry       HookEntry
}

// HookExecution is the result of one script. Output is already redacted and is
// never written to machine-readable stdout directly.
type HookExecution struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

// HookService is the engine-level registration, trust and execution boundary.
// The concrete implementation owns the project config, the machine-local trust
// record and the process-group execution. The engine owns ordering, approvals
// and result reporting.
type HookService interface {
	// Plan lists registered scripts and their current content identity. It
	// must not execute anything and must not write the trust record.
	Plan(ctx context.Context, target Target) (HookPlan, error)
	// Approve records trust for the entries that require a new approval.
	Approve(ctx context.Context, target Target, plan HookPlan, at time.Time) error
	// Execute re-hashes the script immediately before running it. A digest
	// that no longer matches entry.SHA256 must be refused with
	// CategoryHookContentChanged.
	Execute(ctx context.Context, req HookRequest) (HookExecution, error)
}
