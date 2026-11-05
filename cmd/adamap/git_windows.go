package main

import (
	"os"
	"os/exec"
)

func gitTerminationSignals() []os.Signal { return nil }

func configureGitProcess(cmd *exec.Cmd) {
	// CommandContext kills only git on Windows; WaitDelay bounds inherited pipe waits.
}
