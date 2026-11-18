package rollback

import (
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// RecordUnchanged is the only record status this version reports. Rollback
// keeps the single previous-good record byte-identical so a repeated rollback is
// idempotent and no older version is invented.
const RecordUnchanged = "unchanged"

// Report is the machine-readable data of a successful rollback, published as
// data.rollback. It separates what was applied from what was verified and
// states explicitly what was not restored.
type Report struct {
	ProjectName      string                   `json:"project_name"`
	Environment      string                   `json:"environment,omitempty"`
	Scope            string                   `json:"scope"`
	Services         []string                 `json:"services"`
	Files            []string                 `json:"files"`
	EnvFiles         []string                 `json:"env_files"`
	Profiles         []string                 `json:"profiles"`
	RecordedAt       time.Time                `json:"recorded_at"`
	Images           []stack.RollbackPin      `json:"images"`
	Applied          bool                     `json:"applied"`
	Verified         bool                     `json:"verified"`
	ImageIDsVerified bool                     `json:"image_ids_verified"`
	AlreadyAtVersion bool                     `json:"already_at_version"`
	Readiness        []stack.ServiceReadiness `json:"readiness"`
	RecordStatus     string                   `json:"record_status"`
	// DataRestored is always false: rollback never restores DB/volume data.
	DataRestored bool `json:"data_restored"`
	// HooksRun is always false: rollback never runs deploy hooks.
	HooksRun bool     `json:"hooks_run"`
	Notices  []string `json:"notices"`
}

// Payload is the feature result payload. JSON consumers read data.rollback.
type Payload struct {
	Rollback Report `json:"rollback"`
}

// plan is the validated rollback target produced by precheck.
type plan struct {
	target           core.Target
	services         []string
	defs             []stack.ServiceDefinition
	pins             []stack.RollbackPin
	baseline         map[string]int
	alreadyAtVersion bool
	extraServices    []string
	orphanServices   []string
	notices          []string
}
