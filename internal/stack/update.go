package stack

import (
	"context"
	"fmt"

	"github.com/rootmyself/dcraft/internal/core"
)

// Updater is the write-capable update boundary published by ticket #7. It is
// implemented by internal/compose and consumed by internal/update. It owns
// exactly the Compose calls an explicit update needs: model read, live
// container read, cached build, image pull, detached apply and retention
// tagging. Health decisions and good-version recording stay in dcraft.
//
// None of these methods waits for readiness: `up --wait` is not proof (it
// returns 0 for crash-looping and no-healthcheck containers), so the caller
// observes the real runtime state itself.
type Updater interface {
	// UpdateModel reads the resolved Compose model: service keys, image
	// references, build presence and healthcheck presence. It never reads
	// environment values.
	UpdateModel(ctx context.Context, target core.Target) (UpdateModel, error)
	// Containers reads the project's containers from the engine, including
	// the exact image ID, state, health and restart count.
	Containers(ctx context.Context, target core.Target) ([]Container, error)
	// Build builds the local services with the normal cache unless NoCache is
	// set. Pull refreshes base images for the build; it does not pull service
	// image references.
	Build(ctx context.Context, target core.Target, services []string, opts BuildOptions) (OpResult, error)
	// Pull fetches the configured image references of image-only services.
	Pull(ctx context.Context, target core.Target, services []string) (OpResult, error)
	// Apply runs a detached up with the given pull policy and optional forced
	// recreation. It does not wait; the caller verifies.
	Apply(ctx context.Context, target core.Target, services []string, opts ApplyOptions) (OpResult, error)
	// ImageID resolves a local image reference to its full image ID. A missing
	// local image is *ImageMissingError, not a generic failure.
	ImageID(ctx context.Context, ref string) (string, error)
	// TagImage adds the dcraft-owned retention tag to an image and verifies
	// that the tag resolves to the same image.
	TagImage(ctx context.Context, imageID, tag string) error
}

// UpdateModel is the resolved Compose configuration an update acts on.
type UpdateModel struct {
	ProjectName string
	Services    []ServiceDefinition
}

// ServiceDefinition is one configured Compose service. Image is the configured
// reference (empty for build-only services); Build and Healthcheck are presence
// flags, never environment values.
type ServiceDefinition struct {
	Name        string `json:"name"`
	Image       string `json:"image,omitempty"`
	Build       bool   `json:"build"`
	Healthcheck bool   `json:"healthcheck_configured"`
}

// BuildOptions are the explicit build choices. Both are independent.
type BuildOptions struct {
	NoCache bool
	Pull    bool
}

// ApplyOptions are the explicit apply choices.
type ApplyOptions struct {
	ForceRecreate bool
	// PullPolicy is Compose's --pull value: "missing" (default) or "never".
	PullPolicy string
}

// Container is one live container read from the engine. ImageID is the exact
// full image ID (from docker inspect .Image), not the platform manifest digest
// Compose stores in container labels.
type Container struct {
	ID           string
	Name         string
	Service      string
	ImageID      string
	ImageRef     string
	State        string
	Running      bool
	Restarting   bool
	ExitCode     int
	RestartCount int
	Health       string
	HasHealth    bool
	Created      string
}

// ImageMissingError means a local image reference does not resolve. It is
// separate from a tool failure so update can stop before changing anything.
type ImageMissingError struct {
	Ref    string
	Detail string
}

func (e *ImageMissingError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("local image %q is missing", e.Ref)
	}
	return fmt.Sprintf("local image %q is missing: %s", e.Ref, e.Detail)
}

// Readiness is the bounded post-update classification of one service.
type Readiness string

const (
	// ReadinessHealthy means a configured healthcheck passed within the bound.
	ReadinessHealthy Readiness = "healthy"
	// ReadinessRunningUnverified means the container is running but the app
	// defines no healthcheck; it is never reported as verified health.
	ReadinessRunningUnverified Readiness = "running_unverified"
	ReadinessUnhealthy         Readiness = "unhealthy"
	ReadinessExited            Readiness = "exited"
	ReadinessRestarting        Readiness = "restarting"
	ReadinessNotStarted        Readiness = "not_started"
	ReadinessNotCreated        Readiness = "not_created"
	ReadinessTimeout           Readiness = "timeout"
)

// Good reports whether the service may be part of a completed update. Only
// healthy and running_unverified qualify; running_unverified still warns that
// the app itself was not verified.
func (r Readiness) Good() bool {
	return r == ReadinessHealthy || r == ReadinessRunningUnverified
}

// ReadinessInstance is the observed state of one container instance.
type ReadinessInstance struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Health       string `json:"health,omitempty"`
	ExitCode     int    `json:"exit_code"`
	RestartCount int    `json:"restart_count"`
	ImageID      string `json:"image_id,omitempty"`
}

// ServiceReadiness is the published readiness result of one service.
type ServiceReadiness struct {
	Service               string              `json:"service"`
	Status                Readiness           `json:"status"`
	HealthcheckConfigured bool                `json:"healthcheck_configured"`
	AppVerified           bool                `json:"app_verified"`
	Instances             []ReadinessInstance `json:"instances"`
	Detail                string              `json:"detail,omitempty"`
}
