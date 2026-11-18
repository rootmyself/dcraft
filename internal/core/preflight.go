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
	Check(ctx context.Context) PreflightReport
}
