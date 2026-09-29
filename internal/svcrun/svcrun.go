// Package svcrun runs a program either as a Windows service or in the
// foreground (console, or on macOS/Linux during development).
package svcrun

import (
	"context"
	"io"
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

// LogWriter returns where logs should go: always the file, plus the console
// when there is one. A service has no console, and a failing console write
// must never stop the file from being written.
func LogWriter(file io.Writer) io.Writer {
	if IsService() {
		return file
	}
	return teeWriter{file, os.Stderr}
}

type teeWriter struct{ primary, secondary io.Writer }

func (t teeWriter) Write(p []byte) (int, error) {
	n, err := t.primary.Write(p)
	t.secondary.Write(p) // best effort
	return n, err
}
