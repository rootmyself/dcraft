package core

import "context"

// Environment is one named environment alias. It only supplies Compose inputs
// (file order, explicit env files and profiles); it is not a merge language, a
// deployment specification or a project-name source.
//
// Ticket #4 resolves an alias into a Target: Target.Environment carries Name
// and Target.Files/EnvFiles/Profiles carry these inputs, so every later feature
// consumes the same resolved target instead of branching on environments again.
type Environment struct {
	// Name is the alias exactly as written in the project config. It is the
	// machine-readable environment id and part of the state identity.
	Name string `json:"name"`
	// Files are absolute Compose files in the configured order. Empty means
	// the standard Compose discovery inside the project folder.
	Files []string `json:"files"`
	// EnvFiles are absolute explicit --env-file inputs in configured order.
	EnvFiles []string `json:"env_files"`
	// Profiles are the Compose profiles enabled by the alias.
	Profiles []string `json:"profiles"`
}

// EnvironmentSource lists the named environments registered for a target
// folder. The target resolver uses it to expand an explicit selection; the TUI
// uses it to ask for a selection without touching the filesystem itself.
type EnvironmentSource interface {
	Environments(ctx context.Context, req TargetRequest, cwd string) ([]Environment, []Diagnostic, error)
}
