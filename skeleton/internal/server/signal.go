package server

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// notifyShutdown derives a context that ends when the process receives a
// termination signal. SIGTERM is what an orchestrator sends before it removes
// a container; SIGINT is the same request from a terminal.
func notifyShutdown(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT, os.Interrupt)
}
