//go:build !unix

package jobs

import (
	"os"
	"os/exec"
)

func detach(cmd *exec.Cmd) {}

func terminate(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return p.Kill()
}

// alive can't be checked cheaply here, so assume the runner is still there.
func alive(pid int) bool { return pid > 0 }
