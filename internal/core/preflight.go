package core

import "context"

// ToolInfo records the external tools dcraft relied on for an operation. It is
// shown to users and included in machine-readable results.
type ToolInfo struct {
	DockerVersion  string `json:"docker_version,omitempty"`
	ComposeVersion string `json:"compose_version,omitempty"`
	EngineVersion  string `json:"engine_version,omitempty"`
	// Endpoint is the local Docker endpoint dcraft verified (for example a
	// unix socket). Remote endpoints are refused before engine access.
	Endpoint     string `json:"endpoint,omitempty"`
	EndpointKind string `json:"endpoint_kind,omitempty"`
}

// Requirement names one local capability that must be checked before a feature
// runs. A feature declares the capabilities it truly needs in
// Descriptor.Requires; the engine hands that set to Preflight. Missing
// capabilities produce the same stable diagnostics as the historical full
// check, so a feature cannot bypass a check by declaring fewer requirements.
type Requirement string

const (
	// RequirementDockerCLI means the docker executable must be present and
	// runnable. It says nothing about the engine or Compose.
	RequirementDockerCLI Requirement = "docker_cli"
	// RequirementEngine means a reachable local Docker Engine is required.
	RequirementEngine Requirement = "docker_engine"
	// RequirementCompose means an installed, supported Docker Compose is
	// required.
	RequirementCompose Requirement = "compose"
)

// Requirements is the set of preflight capabilities a feature needs.
type Requirements []Requirement

// Has reports whether the set contains a requirement.
func (r Requirements) Has(x Requirement) bool {
	for _, item := range r {
		if item == x {
			return true
		}
	}
	return false
}

// DefaultRequirements is the full local environment check every feature gets
// unless it declares a narrower set. It preserves pre-ticket behavior.
var DefaultRequirements = Requirements{
	RequirementDockerCLI,
	RequirementEngine,
	RequirementCompose,
}

// NormalizeRequirements replaces an empty declaration with the full default
// and makes the implicit Docker CLI dependency explicit. An engine or Compose
// check cannot run without the CLI, so declaring only one of them must never
// disable the CLI lookup (and therefore every later check). A future feature
// that needs no Docker capability extends this contract with an explicit
// "none" value; an empty slice never means "check nothing".
func NormalizeRequirements(reqs Requirements) Requirements {
	if len(reqs) == 0 {
		return DefaultRequirements
	}
	out := make(Requirements, 0, len(reqs)+1)
	seen := map[Requirement]bool{}
	for _, r := range reqs {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	if (seen[RequirementEngine] || seen[RequirementCompose]) && !seen[RequirementDockerCLI] {
		out = append(Requirements{RequirementDockerCLI}, out...)
	}
	return out
}

// PreflightReport is the result of checking the local Docker/Compose
// environment before an operation. Error diagnostics block the operation;
// warnings and info do not.
type PreflightReport struct {
	Tools       ToolInfo     `json:"tools"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Preflight is the environment-check boundary. The production implementation
// talks to the Docker CLI; tests provide deterministic fakes.
type Preflight interface {
	Check(ctx context.Context, requires Requirements) PreflightReport
}
