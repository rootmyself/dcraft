package data

import "context"

// VolumeUsage is the observed content summary of one named volume. Bytes is
// the allocated content size as reported by the helper (KiB rounded up to
// bytes); Entries is the number of paths below the volume root.
type VolumeUsage struct {
	Bytes   int64
	Entries int64
}

// VolumeRestoreBackend is the complete named-volume restore capability: the
// read-only data plane plus the destructive/exact restore operations.
type VolumeRestoreBackend interface {
	Backend
	VolumeRestorer
}

// VolumeRestorer is the published named-volume half of the safe-replace
// contract introduced by ticket #18 for bind-folder restore (#19). It adds
// only the destructive/exact operations that ArchiveVolume does not provide.
// The backup format and the surrounding safety sequence are shared; a bind
// destination (#19) implements the equivalent host-folder operations and the
// same restore.Executor orchestration.
type VolumeRestorer interface {
	// VolumeExists reports whether the named engine volume exists.
	VolumeExists(ctx context.Context, volumeName string) (bool, error)
	// VolumeUsage summarizes the current volume content read-only.
	VolumeUsage(ctx context.Context, volumeName string) (VolumeUsage, error)
	// EmptyVolume removes every entry below /data, including dotfiles and
	// hidden directories, and never removes the mount root.
	EmptyVolume(ctx context.Context, volumeName string) (OpResult, error)
	// ExtractVolume extracts the artifact data file into /data as root so
	// uid/gid, modes and hardlinks are preserved. The destination must have
	// been emptied first; extraction is a replace, not a merge.
	ExtractVolume(ctx context.Context, volumeName, artifactDir string) (OpResult, error)
	// VerifyVolume runs a generated verification script against the restored
	// volume read-only and returns non-zero output on the first mismatch.
	VerifyVolume(ctx context.Context, volumeName, scriptPath string) (OpResult, error)
}
