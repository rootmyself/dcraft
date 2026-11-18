// Package data defines the narrow data-plane boundary for stack data
// operations. Ticket #16 publishes named-volume sources and the volume archive
// helper; bind-folder sources (#17) and restore (#18/#19) extend the same
// boundary instead of inventing a second one.
package data

import (
	"context"

	"github.com/rootmyself/dcraft/internal/core"
)

// Kind identifies what a data source is.
type Kind string

const (
	KindVolume Kind = "volume"
	KindBind   Kind = "bind"
)

// DataSource is one selectable data source of a stack as resolved from the
// Compose configuration. Volume.Key is the Compose key; Name is the actual
// engine volume name. For a bind mount, Name is the absolute host path.
// Anonymous volumes have no Key and no Compose-declared Name.
type DataSource struct {
	Kind Kind
	Key  string
	// Name is the actual engine volume name for a volume, or the
	// symlink-resolved absolute host path for a bind mount.
	Name string
	// Declared is the bind source exactly as the Compose configuration
	// declared it (absolute but not symlink-resolved). It is empty for
	// volumes. Consumer queries use both spellings because Docker may record
	// either one as the mount source.
	Declared string
	// DeclaredPaths carries every distinct Compose spelling that resolves to
	// the same host folder. It is used only for bind consumer queries.
	DeclaredPaths []string
	MountTargets  []string
	External      bool
	Anonymous     bool
	ReadOnly      bool
	Services      []string
}

// ServiceRuntime is the observed state of one Compose service before a data
// operation. State is the Compose ps state ("running", "exited", ...).
type ServiceRuntime struct {
	Service  string
	State    string
	ID       string
	ExitCode int
}

// ContainerConsumer is one container that references a data source. Project
// and Service are Compose labels and can be empty for containers created
// outside Compose.
type ContainerConsumer struct {
	Name    string
	Project string
	Service string
	State   string
}

// OpResult is the observable output of one data-plane command. A non-zero
// helper exit is returned as an error with this output attached.
type OpResult struct {
	Output string
}

// ArchiveDataFile is the fixed data payload name inside one backup artifact.
// The backup format contract (#16) and later restore tickets use it.
const ArchiveDataFile = "data.tar.gz"

// VolumeHelperImage is the default image whose tar archives a named volume.
// It is announced in the backup plan and can be overridden with
// DCRAFT_BACKUP_IMAGE.
const VolumeHelperImage = "alpine:3.20"

// Backend is the Docker/Compose data-plane boundary consumed by the backup
// feature. It is implemented by internal/compose and replaced by a fake in
// tests. Every method is read-only except StopServices, StartServices and
// ArchiveVolume.
type Backend interface {
	// ProjectName is Compose's resolved project name for the target. It is
	// used when no explicit, declared or recorded name exists.
	ProjectName(ctx context.Context, target core.Target) (string, error)
	// Sources resolves every volume and bind mount of the Compose project.
	Sources(ctx context.Context, target core.Target) ([]DataSource, error)
	// ServiceRuntime reports the observed state of the project's services,
	// including stopped containers.
	ServiceRuntime(ctx context.Context, target core.Target) ([]ServiceRuntime, error)
	// VolumeConsumers lists containers that reference the actual engine
	// volume name, including stopped ones. The target supplies the project
	// context for redaction.
	VolumeConsumers(ctx context.Context, target core.Target, volumeName string) ([]ContainerConsumer, error)
	// BindConsumers lists containers whose bind mount refers to hostPath,
	// including stopped ones. hostPath may be the host spelling or the
	// Docker-visible spelling (for example /host_mnt... on Docker Desktop);
	// the implementation tries the platform mapping and returns the union.
	BindConsumers(ctx context.Context, target core.Target, hostPath string) ([]ContainerConsumer, error)
	// StopServices stops only the named Compose services.
	StopServices(ctx context.Context, target core.Target, services []string) (OpResult, error)
	// StartServices starts only the named, previously running services.
	StartServices(ctx context.Context, target core.Target, services []string) (OpResult, error)
	// ArchiveVolume writes data.tar.gz into hostDir from a read-only mount of
	// the named volume. The helper runs as root so the archive preserves
	// uid/gid and modes.
	ArchiveVolume(ctx context.Context, volumeName, hostDir string) (OpResult, error)
	// HelperImage is the image used by ArchiveVolume; it is shown in plans.
	HelperImage() string
	// Redact removes values referenced by the project's saved secret
	// references from tool output before it becomes a diagnostic or history
	// record. It is best-effort and never returns a value itself.
	Redact(ctx context.Context, target core.Target, text string) string
}
