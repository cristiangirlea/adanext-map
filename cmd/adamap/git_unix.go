//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func gitTerminationSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM}
}

func configureGitProcess(cmd *exec.Cmd) {
	// A git child can inherit the pipes or block on input after git itself is killed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
