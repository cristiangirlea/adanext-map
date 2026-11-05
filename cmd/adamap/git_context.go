package main

import (
	"context"
	"os"
	"os/signal"
)

var gitNotifyContext = signal.NotifyContext

func gitInterruptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	// Detached git groups must still stop when adamap is interrupted.
	return gitNotifyContext(ctx, append([]os.Signal{os.Interrupt}, gitTerminationSignals()...)...)
}
