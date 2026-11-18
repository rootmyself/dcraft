package core

// TargetRequest is what a caller (CLI flags or a TUI selection) asks for. The
// resolver turns it into a fully qualified Target.
type TargetRequest struct {
	// Path selects the project folder. Empty means the current working
	// directory. A relative path is resolved against the working directory.
	Path string
	// Files lists explicit Compose files in order. Relative paths are
	// resolved against the working directory, matching `docker compose -f`.
	// Empty means standard discovery inside Path.
	Files []string
	// EnvFiles lists explicit Compose environment files in order.
	EnvFiles []string
	// Profiles lists explicitly enabled Compose profiles.
	Profiles []string
	// ProjectName is the explicit Compose project name. Empty means Compose
	// resolves it from the project folder (ticket #3 later allocates reusable
	// automatic names and populates this field before an operation runs).
	ProjectName string
	// Environment is the explicitly selected named-environment alias. When the
	// project registers aliases in .dcraft.yaml, every operation must select
	// one with --env (CLI) or the TUI selection screen; empty is an error and
	// is never replaced with an implicit choice. With no aliases it stays
	// empty and standard Compose defaults apply.
	Environment string
}

// Target is the resolved stack selection. Every feature receives the same
// Target for status, logs, exec, lifecycle and data operations so that the
// selection cannot drift between commands.
type Target struct {
	ProjectDir  string   `json:"project_dir"`
	Files       []string `json:"files"`
	EnvFiles    []string `json:"env_files"`
	Profiles    []string `json:"profiles"`
	ProjectName string   `json:"project_name,omitempty"`
	Environment string   `json:"environment,omitempty"`
	// DefaultEnvFile is the .env file Compose will load automatically from
	// the first Compose file's directory. It is informational only and is not
	// passed with --env-file; when explicit EnvFiles are present, .env is not
	// loaded and this field is empty.
	DefaultEnvFile string `json:"default_env_file,omitempty"`
}

// ComposeArgs returns the global Compose inputs after the `compose` command in
// the order dcraft always passes them. Relative paths are already absolute.
//
// dcraft deliberately does not pass --project-directory: Compose then keeps its
// own rule that the project directory is the directory of the first file, so
// relative paths and the default .env file resolve exactly as they would for a
// direct `docker compose -f ...` invocation.
func (t Target) ComposeArgs() []string {
	args := []string{"compose"}
	for _, f := range t.Files {
		args = append(args, "-f", f)
	}
	for _, e := range t.EnvFiles {
		args = append(args, "--env-file", e)
	}
	for _, p := range t.Profiles {
		args = append(args, "--profile", p)
	}
	if t.ProjectName != "" {
		args = append(args, "-p", t.ProjectName)
	}
	return args
}

// Normalize replaces nil slices with empty ones so JSON output is stable
// (`[]` rather than `null`).
func (t *Target) Normalize() {
	if t.Files == nil {
		t.Files = []string{}
	}
	if t.EnvFiles == nil {
		t.EnvFiles = []string{}
	}
	if t.Profiles == nil {
		t.Profiles = []string{}
	}
}
