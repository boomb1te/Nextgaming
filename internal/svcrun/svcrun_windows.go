//go:build windows

package svcrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// serviceFlag is set when the program was started with -service, which the
// installed service always passes. Detection by parent process alone is not
// reliable everywhere, so the explicit flag wins.
var serviceFlag bool

// MarkService tells svcrun that this process was started by the Service Control Manager.
func MarkService() { serviceFlag = true }

// ServiceArg is added to the command line of every installed service.
const ServiceArg = "-service"

// IsService reports whether the process was started by the Service Control Manager.
func IsService() bool {
	if serviceFlag {
		return true
	}
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// Run runs `run` as a service if started by the SCM, otherwise in the foreground.
func Run(name string, run func(ctx context.Context) error) error {
	if !IsService() {
		return runForeground(run)
	}
	return svc.Run(name, &handler{run: run})
}

type handler struct {
	run func(ctx context.Context) error
}

func (h *handler) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				// Non-zero exit code makes Windows apply the recovery actions (restart).
				return true, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				if c.Cmd == svc.Stop {
					stopRequested.Store(true) // stopped by someone, not by Windows shutting down
				}
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			case svc.SessionChange:
				SessionChanged()
			}
		}
	}
}

var stopRequested atomic.Bool

// StopRequested reports whether the service is stopping because someone
// stopped it (Services, Task Manager, sc stop) rather than Windows shutting down.
func StopRequested() bool { return stopRequested.Load() }

// sessionHook is called when a user logs on/off, so the agent can relaunch the lock screen.
var sessionHook func()

// OnSessionChange registers a callback for user logon/logoff events.
func OnSessionChange(f func()) { sessionHook = f }

// SessionChanged invokes the session-change callback.
func SessionChanged() {
	if sessionHook != nil {
		go sessionHook()
	}
}

// Install registers the current executable as an auto-start service that
// Windows restarts immediately if it stops unexpectedly.
func Install(spec Spec) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as Administrator): %w", err)
	}
	defer m.Disconnect()
	if old, err := m.OpenService(spec.Name); err == nil {
		// Reinstall: remove the old registration (e.g. from an older version) first.
		old.Close()
		if err := removeService(m, spec.Name); err != nil {
			return fmt.Errorf("remove the old %s service: %w", spec.Name, err)
		}
	}
	args := append(append([]string{}, spec.Args...), ServiceArg)
	var s *mgr.Service
	for i := 0; ; i++ {
		s, err = m.CreateService(spec.Name, exe, mgr.Config{
			DisplayName:      spec.DisplayName,
			Description:      spec.Description,
			StartType:        mgr.StartAutomatic,
			DelayedAutoStart: false,
		}, args...)
		// Windows finishes deleting the old service asynchronously.
		if err == windows.ERROR_SERVICE_MARKED_FOR_DELETE && i < 20 {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		break
	}
	if err != nil {
		return err
	}
	defer s.Close()
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 1 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 1 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}
	if err := s.SetRecoveryActions(actions, 60); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	_ = s.SetRecoveryActionsOnNonCrashFailures(true)
	return s.Start()
}

// Uninstall stops and removes the service.
func Uninstall(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as Administrator): %w", err)
	}
	defer m.Disconnect()
	return removeService(m, name)
}

// removeService stops a service (killing it if it does not stop, e.g. when it
// is stuck in "start pending") and deletes it.
func removeService(m *mgr.Mgr, name string) error {
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %s is not installed", name)
	}
	defer s.Close()
	// Clear recovery actions first so Windows does not restart it while we remove it.
	_ = s.ResetRecoveryActions()
	s.Control(svc.Stop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			break
		}
		if time.Now().After(deadline) {
			if st.ProcessId != 0 {
				if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, st.ProcessId); err == nil {
					windows.TerminateProcess(h, 1)
					windows.CloseHandle(h)
				}
			}
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return s.Delete()
}

// IsAdmin reports whether the process runs elevated.
func IsAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID, windows.DOMAIN_ALIAS_RID_ADMINS, 0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}
