package restore

import (
	"context"
	"fmt"
	"os"

	"github.com/rootmyself/dcraft/internal/backup"
	"github.com/rootmyself/dcraft/internal/core"
)

// SafetyBackup is one completed and verified v1 backup of the current target
// data, created before the replacement starts.
type SafetyBackup struct {
	Path     string
	Manifest backup.Manifest
}

// CreateSafetyBackup archives the current destination content into safetyRoot
// and publishes it only after the same completion/integrity checks as any v1
// backup. A failed, incomplete or unverifiable attempt never returns a usable
// path, and the caller must not replace the target.
func CreateSafetyBackup(ctx context.Context, dest Destination, safetyRoot string, stack backup.StackState, tool backup.Tool, clock core.Clock) (SafetyBackup, error) {
	if clock == nil {
		clock = core.SystemClock{}
	}
	now := clock.Now()
	base := backup.ArtifactName(dest.ManifestSource(), now)
	final, err := backup.UniqueArtifactPath(safetyRoot, base)
	if err != nil {
		return SafetyBackup{Path: final}, err
	}
	partial := final + backup.PartialSuffix
	if err := backup.EnsureEmptyDir(partial); err != nil {
		return SafetyBackup{Path: partial}, err
	}
	fail := func(err error) (SafetyBackup, error) {
		kept := partial
		if removeErr := os.RemoveAll(partial); removeErr == nil {
			kept = ""
		}
		if kept != "" {
			return SafetyBackup{Path: kept}, fmt.Errorf("%w (부분 출력: %s)", err, kept)
		}
		return SafetyBackup{Path: partial}, err
	}
	res, err := dest.SafetyBackup(ctx, partial)
	if err != nil {
		if res.Output != "" {
			err = fmt.Errorf("%v; %s", err, res.Output)
		}
		return fail(err)
	}
	if _, err := backup.BuildArtifact(partial, dest.ManifestSource(), stack, tool, now); err != nil {
		return fail(err)
	}
	if err := os.Rename(partial, final); err != nil {
		return fail(err)
	}
	verified, err := backup.Verify(final)
	if err != nil {
		_ = os.Rename(final, partial)
		return SafetyBackup{Path: partial}, err
	}
	return SafetyBackup{Path: final, Manifest: verified}, nil
}

// safetyStack is the audit identity written into the safety backup. It records
// the services observed before this restore, not a consistency promise.
func safetyStack(project string, affected, running, stopped []string) backup.StackState {
	return backup.StackState{
		ProjectName:      project,
		Services:         append([]string{}, affected...),
		RunningBefore:    append([]string{}, running...),
		StoppedForBackup: append([]string{}, stopped...),
	}
}
