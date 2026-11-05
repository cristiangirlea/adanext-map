//go:build !unix && !windows

package main

import (
	"os"
	"os/exec"
)

func gitTerminationSignals() []os.Signal { return nil }

func configureGitProcess(cmd *exec.Cmd) {
	// Platforms without Unix process groups retain CommandContext's process cancellation.
}
