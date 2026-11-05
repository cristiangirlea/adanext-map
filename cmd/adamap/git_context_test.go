package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestGitSignalCancelsCommandContext(t *testing.T) {
	oldNotify, oldCommand := gitNotifyContext, gitCommand
	defer func() { gitNotifyContext, gitCommand = oldNotify, oldCommand }()
	var commandContext context.Context
	var stopped bool
	gitNotifyContext = func(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		if len(signals) == 0 {
			t.Fatal("no interruption signal registered")
		}
		ctx, cancel := context.WithCancel(ctx)
		cancel() // Deliver the signal through the notification seam before git starts.
		return ctx, func() { stopped = true; cancel() }
	}
	gitCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		commandContext = ctx
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	}
	_, err := gitOutput(t.TempDir(), "--version")
	if err == nil || commandContext.Err() != context.Canceled {
		t.Fatalf("git error = %v, context error = %v", err, commandContext.Err())
	}
	if !stopped {
		t.Fatal("signal notification was not stopped")
	}
}

func TestGitInterruptContextParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	child, stop := gitInterruptContext(ctx)
	defer stop()
	cancel()
	select {
	case <-child.Done():
	case <-time.After(time.Second):
		t.Fatal("git context ignored parent cancellation")
	}
}
