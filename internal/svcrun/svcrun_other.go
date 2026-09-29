//go:build !windows

package svcrun

import (
	"context"
	"errors"
)

var errWindowsOnly = errors.New("installing as a service is only supported on Windows")

// IsService is always false outside Windows.
func IsService() bool { return false }

// Run runs in the foreground.
func Run(name string, run func(ctx context.Context) error) error { return runForeground(run) }

// OnSessionChange is a no-op outside Windows.
func OnSessionChange(f func()) {}

// Install is Windows-only.
func Install(spec Spec) error { return errWindowsOnly }

// Uninstall is Windows-only.
func Uninstall(name string) error { return errWindowsOnly }

// IsAdmin is always true outside Windows (not needed there).
func IsAdmin() bool { return true }
