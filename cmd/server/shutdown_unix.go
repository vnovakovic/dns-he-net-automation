//go:build !windows

package main

import (
	"context"
	"os/signal"
	"syscall"
)

// makeShutdownCtx returns a context cancelled on SIGTERM or SIGINT.
// This is the non-Windows implementation; the Windows version is in shutdown_windows.go.
func makeShutdownCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}
