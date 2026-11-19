//go:build unix

package hooks

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup makes the hook and every child it starts share a process
// group, so cancellation can terminate the whole group instead of orphaning a
// background child.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil {
		return process.Kill()
	}
	return nil
}
