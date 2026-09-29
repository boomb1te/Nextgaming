// Package svcrun runs a program either as a Windows service or in the
// foreground (console, or on macOS/Linux during development).
package svcrun

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Spec describes a service for installation.
type Spec struct {
	Name        string
	DisplayName string
	Description string
	Args        []string // arguments passed to the executable when the service starts
}

// runForeground runs until Ctrl+C / SIGTERM.
func runForeground(run func(ctx context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}
