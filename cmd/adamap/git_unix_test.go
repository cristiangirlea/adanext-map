//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGitTerminationSignal(t *testing.T) {
	signals := gitTerminationSignals()
	if len(signals) != 1 || signals[0] != syscall.SIGTERM {
		t.Fatalf("termination signals = %v, want SIGTERM", signals)
	}
}

func TestGitIgnoreFIFOsTimeOut(t *testing.T) {
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("mkfifo unavailable")
	}
	for _, target := range []string{"excludes", ".gitignore"} {
		t.Run(target, func(t *testing.T) {
			repo := t.TempDir()
			git(t, repo, "init", "-q")
			writeFile(t, filepath.Join(repo, "a.txt"), "one")
			git(t, repo, "add", ".")
			git(t, repo, "commit", "-qm", "one")
			fifo := filepath.Join(repo, target)
			if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo: %v: %s", err, out)
			}
			if target == "excludes" {
				git(t, repo, "config", "core.excludesFile", fifo)
			}
			old := gitTimeout
			gitTimeout = 500 * time.Millisecond
			defer func() { gitTimeout = old }()
			start := time.Now()
			_, _, err := resolveCommit(repo, "")
			if err == nil || !strings.Contains(err.Error(), "git status: timed out") {
				t.Fatalf("error = %v", err)
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("timeout took %v", took)
			}
		})
	}
}

func TestGitTimeoutKillsChildren(t *testing.T) {
	oldTimeout, oldCommand := gitTimeout, gitCommand
	gitTimeout = 500 * time.Millisecond
	readyFile, doneFile := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "done")
	gitCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `(sleep 2; echo survived > "$2") & echo ready > "$1"; wait`, "sh", readyFile, doneFile)
	}
	defer func() { gitTimeout, gitCommand = oldTimeout, oldCommand }()
	_, err := gitOutput(t.TempDir(), "--version")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("child was not started: %v", err)
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(doneFile); !os.IsNotExist(err) {
		t.Fatalf("child survived timeout: %v", err)
	}
}
