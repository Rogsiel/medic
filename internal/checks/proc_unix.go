//go:build !windows

package checks

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes cancellation kill the whole process tree (sh -c children too).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
