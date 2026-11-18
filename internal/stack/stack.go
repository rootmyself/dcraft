// Package stack defines the read-only view of a running stack and the backend
// boundary used by features such as status. The Compose adapter implements it;
// UI code only sees these types.
package stack

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
)

// Backend reads the current stack. A future execution backend can implement
// the same boundary without changing the CLI or TUI.
type Backend interface {
	Name() string
	Inspect(ctx context.Context, target core.Target) (Snapshot, error)
}

// ServiceState is the aggregated machine-readable state of a Compose service.
type ServiceState string

const (
	StateNotCreated ServiceState = "not_created"
	StateRunning    ServiceState = "running"
	StateRestarting ServiceState = "restarting"
	StatePaused     ServiceState = "paused"
	StateCreated    ServiceState = "created"
	StateExited     ServiceState = "exited"
	StateUnknown    ServiceState = "unknown"
)

// Health is a healthcheck result. None means "no healthcheck configured or no
// running instance", which must never be presented as verified health.
type Health string

const (
	HealthNone      Health = "none"
	HealthStarting  Health = "starting"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Connection sources.
const (
	SourceRuntime = "runtime"
	SourceConfig  = "config"
)

// Snapshot is a point-in-time read-only view of one stack.
type Snapshot struct {
	Backend     string    `json:"backend"`
	ProjectName string    `json:"project_name"`
	ProjectDir  string    `json:"project_dir"`
	Environment string    `json:"environment,omitempty"`
	Services    []Service `json:"services"`
	Summary     Summary   `json:"summary"`
}

// Service combines Compose configuration with observed container instances.
type Service struct {
	Name string `json:"name"`
	// Configured is false when a container exists for a service that is not
	// part of the active Compose configuration (for example a container from
	// a profile that is not enabled now).
	Configured            bool         `json:"configured"`
	Image                 string       `json:"image,omitempty"`
	Build                 bool         `json:"build"`
	HealthcheckConfigured bool         `json:"healthcheck_configured"`
	State                 ServiceState `json:"state"`
	Health                Health       `json:"health"`
	// AppVerified is true only when every instance is running and every
	// existing healthcheck reports healthy. A service without a healthcheck
	// is never verified.
	AppVerified bool         `json:"app_verified"`
	Instances   []Instance   `json:"instances"`
	Connections []Connection `json:"connections"`
}

// Instance is one container. Service names and container instances are
// distinct concepts; a service can have several instances.
type Instance struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Health   Health `json:"health"`
	Image    string `json:"image,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// Connection is an exposed port binding. Runtime bindings come from running
// containers; config connections describe declared ports of services that are
// not running.
type Connection struct {
	Source        string `json:"source"`
	HostIP        string `json:"host_ip,omitempty"`
	PublishedPort int    `json:"published_port,omitempty"`
	TargetPort    int    `json:"target_port"`
	Protocol      string `json:"protocol"`
}

// Summary is a count summary for quick display and automation.
type Summary struct {
	Services   int `json:"services"`
	Instances  int `json:"instances"`
	Running    int `json:"running"`
	Healthy    int `json:"healthy"`
	Unverified int `json:"unverified"`
	Unhealthy  int `json:"unhealthy"`
	Stopped    int `json:"stopped"`
	NotCreated int `json:"not_created"`
}
