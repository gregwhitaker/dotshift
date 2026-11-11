//go:build unix

package tunnel

import (
	"os/exec"
	"syscall"
)

// Tailscale's macOS PATH entry can be a shell wrapper. Give it a private
// process group so shutdown also reaches the CLI that owns the listener.
func prepareFunnelProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptFunnelProcess(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func killFunnelProcess(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
