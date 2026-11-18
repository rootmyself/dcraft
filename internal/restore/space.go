package restore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SpaceChecker reports free bytes on the filesystem that actually holds the
// safety backup directory. The default checks the host filesystem with `df`
// because the safety archive is written through a host bind mount; named
// volume capacity itself is checked by the helper's own failure.
type SpaceChecker interface {
	AvailableBytes(path string) (int64, error)
}

// DiskSpace is the production SpaceChecker.
type DiskSpace struct{}

func (DiskSpace) AvailableBytes(path string) (int64, error) {
	dir := nearestExistingDir(path)
	out, err := exec.Command("df", "-Pk", dir).Output()
	if err != nil {
		return 0, fmt.Errorf("df %s: %w", dir, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("df %s: unexpected output", dir)
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, fmt.Errorf("df %s: unexpected output %q", dir, lines[len(lines)-1])
	}
	kib, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("df %s: available %q: %w", dir, fields[3], err)
	}
	return kib * 1024, nil
}

func nearestExistingDir(path string) string {
	cur := filepath.Clean(path)
	for {
		if info, err := os.Stat(cur); err == nil {
			if info.IsDir() {
				return cur
			}
			return filepath.Dir(cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur
		}
		cur = parent
	}
}

// requiredSpace is a conservative estimate: the current content plus a 10%
// margin and one MiB for the archive metadata and filesystem overhead.
func requiredSpace(current ContentSummary) int64 {
	base := current.Bytes
	if base < 0 {
		base = 0
	}
	return base + base/10 + 1<<20
}
