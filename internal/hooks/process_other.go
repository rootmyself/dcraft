//go:build !unix

package hooks

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op on platforms without Unix process groups. dcraft
// still kills the direct child on cancellation; the limitation is documented.
func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}
