package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// VolumeDestination is the named-volume implementation of Destination. It
// consumes only the published data.Backend/data.VolumeRestorer capabilities;
// no concrete Compose type is assumed.
type VolumeDestination struct {
	backend     data.VolumeRestoreBackend
	source      data.DataSource
	target      core.Target
	project     string
	environment string
	projectDir  string
}

// NewVolumeDestination builds one named-volume destination. target is the
// resolved Compose target used for consumer and redaction calls.
func NewVolumeDestination(backend data.VolumeRestoreBackend, source data.DataSource, target core.Target, project string) *VolumeDestination {
	return &VolumeDestination{
		backend: backend, source: source, target: target, project: project,
		environment: target.Environment, projectDir: target.ProjectDir,
	}
}

func (d *VolumeDestination) Kind() data.Kind { return data.KindVolume }

func (d *VolumeDestination) Key() string { return d.source.Key }

func (d *VolumeDestination) Name() string { return d.source.Name }

func (d *VolumeDestination) Services() []string { return append([]string{}, d.source.Services...) }

func (d *VolumeDestination) MountTargets() []string {
	return append([]string{}, d.source.MountTargets...)
}

func (d *VolumeDestination) ManifestSource() backup.Source {
	return backup.SourceFrom(d.source, d.project, d.environment, d.projectDir)
}

func (d *VolumeDestination) Match(m backup.Manifest, explicit bool) ([]string, error) {
	return MatchSource(m, d.ManifestSource(), explicit)
}

// Outside is always true for a named volume: the target has no host path that
// an artifact or safety directory could overlap.
func (d *VolumeDestination) Outside(string) (bool, error) { return true, nil }

func (d *VolumeDestination) Prepare(ctx context.Context) error {
	exists, err := d.backend.VolumeExists(ctx, d.source.Name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: volume %s", ErrTargetMissing, d.source.Name)
	}
	return nil
}

// UnsafeConsumers uses the actual engine name, never the Compose key, and
// includes stopped containers because they still hold the volume reference.
func (d *VolumeDestination) UnsafeConsumers(ctx context.Context) ([]data.ContainerConsumer, error) {
	consumers, err := d.backend.VolumeConsumers(ctx, d.target, d.source.Name)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, svc := range d.source.Services {
		allowed[svc] = true
	}
	var outside []data.ContainerConsumer
	for _, consumer := range consumers {
		if consumer.Project == d.project && consumer.Service != "" && allowed[consumer.Service] {
			continue
		}
		outside = append(outside, consumer)
	}
	return outside, nil
}

func (d *VolumeDestination) Usage(ctx context.Context) (ContentSummary, error) {
	usage, err := d.backend.VolumeUsage(ctx, d.source.Name)
	if err != nil {
		return ContentSummary{}, err
	}
	return ContentSummary{Bytes: usage.Bytes, Entries: usage.Entries}, nil
}

func (d *VolumeDestination) SafetyBackup(ctx context.Context, hostDir string) (data.OpResult, error) {
	return d.backend.ArchiveVolume(ctx, d.source.Name, hostDir)
}

func (d *VolumeDestination) Empty(ctx context.Context) (data.OpResult, error) {
	return d.backend.EmptyVolume(ctx, d.source.Name)
}

func (d *VolumeDestination) Extract(ctx context.Context, artifactDir string) (data.OpResult, error) {
	return d.backend.ExtractVolume(ctx, d.source.Name, artifactDir)
}

func (d *VolumeDestination) Verify(ctx context.Context, manifest backup.Manifest) (data.OpResult, error) {
	dir, err := os.MkdirTemp("", "dcraft-restore-verify-")
	if err != nil {
		return data.OpResult{}, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "check.sh")
	if err := WriteVerifyScript(script, manifest); err != nil {
		return data.OpResult{}, err
	}
	return d.backend.VerifyVolume(ctx, d.source.Name, script)
}
