//go:build !unix

package tunnel

import (
	"os"
	"os/exec"
)

func prepareFunnelProcess(cmd *exec.Cmd) {}

func interruptFunnelProcess(cmd *exec.Cmd) { _ = cmd.Process.Signal(os.Interrupt) }

func killFunnelProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
