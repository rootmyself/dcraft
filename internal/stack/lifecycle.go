package stack

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
)

// Lifecycle is the write-capable stack boundary published by ticket #3. It
// covers exactly the four stack lifecycle operations. Update, pull, build,
// health and data operations belong to later tickets and are not added here.
//
// The Compose adapter implements it. Services are Compose service names, never
// container instances; an empty selection means the whole stack. Down is
// always project-wide and never removes data volumes.
type Lifecycle interface {
	Up(ctx context.Context, target core.Target, services []string) (OpResult, error)
	Stop(ctx context.Context, target core.Target, services []string) (OpResult, error)
	Down(ctx context.Context, target core.Target) (OpResult, error)
	Restart(ctx context.Context, target core.Target, services []string) (OpResult, error)
}

// OpResult is the observable tool output of one lifecycle invocation. Output
// is already trimmed for diagnostics. A non-zero Compose exit is returned with
// an error; cancellation is returned as a context error.
type OpResult struct {
	Output string
}

// ConfigInfo is the narrow part of the resolved Compose model dcraft needs for
// stack identity and service validation. It never carries environment values,
// labels or commands.
type ConfigInfo struct {
	// EffectiveName is Compose's resolved project name (a `name:`, -p,
	// COMPOSE_PROJECT_NAME or the folder-derived default).
	EffectiveName string
	// Services are the configured Compose service names, sorted.
	Services []string
}

// ProjectInfo is one Compose project that already exists on the local engine.
type ProjectInfo struct {
	Name        string
	Status      string
	ConfigFiles []string
	// ProjectDir is the directory of the first config file, which is
	// Compose's project directory and therefore identifies its folder.
	ProjectDir string
}

// Identity is the narrow project-identity boundary consumed by automatic name
// allocation. It is implemented by internal/compose and reads only names and
// service keys; it never requests resolved environment values.
type Identity interface {
	ConfigInfo(ctx context.Context, target core.Target) (ConfigInfo, error)
	// DerivedProjectName returns Compose's own default project name for the
	// target folder, without honoring a declared name. It is the readable
	// base of an automatic name.
	DerivedProjectName(ctx context.Context, target core.Target) (string, error)
	// Projects lists existing Compose projects on the local engine.
	Projects(ctx context.Context) ([]ProjectInfo, error)
}
