//go:build unix

package jobs

import (
	"errors"
	"os/exec"
	"syscall"
)

// detach puts the agent in its own process group so cancelling a job stops
// everything the agent started, and Ctrl-C in the terminal reaches only
// dotshift, which then cancels the job itself.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminate(cmd.Process.Pid) }
}

// terminate sends SIGTERM to the agent's process group.
func terminate(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// killGroup is the final cleanup for a supervised goal step, including any
// children that ignored SIGTERM or outlived their parent.
func killGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// alive reports whether a process exists.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
