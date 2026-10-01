//go:build windows

package platform

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"nextgaming/internal/svcrun"
)

const noSession = 0xFFFFFFFF

// NewLauncher keeps the lock screen running in the logged-in user's session.
//
// When the agent runs as a Windows service (session 0, no desktop), the
// client is started inside the active user session. With asSystem=true it runs
// under the SYSTEM account there, so a restricted gamer account cannot kill it;
// otherwise it runs as the logged-in user.
func NewLauncher(clientPath string, args []string, asSystem bool) Launcher {
	l := &winLauncher{path: clientPath, args: args, asSystem: asSystem, session: noSession}
	killStale(filepath.Base(clientPath))
	return l
}

type winLauncher struct {
	mu       sync.Mutex
	path     string
	args     []string
	asSystem bool

	proc      windows.Handle
	pid       uint32
	session   uint32
	deaths    []time.Time
	lastStart time.Time
}

func (l *winLauncher) running() bool {
	if l.proc == 0 {
		return false
	}
	ev, err := windows.WaitForSingleObject(l.proc, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

func (l *winLauncher) Alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running()
}

func (l *winLauncher) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.proc != 0 {
		windows.TerminateProcess(l.proc, 0)
		windows.CloseHandle(l.proc)
		l.proc, l.pid = 0, 0
	}
}

// Ensure is called every second by the agent's watchdog.
func (l *winLauncher) Ensure() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	service := svcrun.IsService()
	session := uint32(0)
	if service {
		session = windows.WTSGetActiveConsoleSessionId()
		if session == noSession || session == 0 {
			return false, "" // nobody is logged on at the console yet
		}
	}

	restarted, detail := false, ""
	if l.proc != 0 {
		if l.running() && (!service || session == l.session) {
			return false, ""
		}
		if !l.running() {
			var code uint32
			windows.GetExitCodeProcess(l.proc, &code)
			// Only report tampering if the same user session is still there.
			if !service || session == l.session {
				restarted = true
				detail = fmt.Sprintf("exit code %d", code)
			}
		} else {
			// The user session changed (log off / log on): move the client.
			windows.TerminateProcess(l.proc, 0)
		}
		windows.CloseHandle(l.proc)
		l.proc, l.pid = 0, 0
	}

	// Back off if the client keeps crashing, so we do not spin.
	now := time.Now()
	recent := l.deaths[:0]
	for _, t := range l.deaths {
		if now.Sub(t) < 10*time.Second {
			recent = append(recent, t)
		}
	}
	l.deaths = recent
	if restarted {
		l.deaths = append(l.deaths, now)
	}
	// If it keeps dying (a genuine crash loop, or someone killing it on purpose),
	// do not give up — just slow to one relaunch every ~2s so we neither spin the
	// CPU nor ever leave the desktop exposed for long.
	if len(l.deaths) >= 5 {
		if now.Sub(l.lastStart) < 2*time.Second {
			return restarted, detail
		}
	}

	var (
		h   windows.Handle
		pid uint32
		err error
	)
	if service {
		h, pid, err = startInSession(l.path, l.args, session, l.asSystem)
		if err != nil && l.asSystem {
			log.Printf("launcher: start as SYSTEM failed (%v); trying as the user", err)
			h, pid, err = startInSession(l.path, l.args, session, false)
		}
	} else {
		h, pid, err = startLocal(l.path, l.args)
	}
	if err != nil {
		log.Printf("launcher: cannot start %s: %v", l.path, err)
		return restarted, detail
	}
	l.proc, l.pid, l.session = h, pid, session
	l.lastStart = now
	return restarted, detail
}

func quoteArgs(path string, args []string) string {
	parts := []string{syscall.EscapeArg(path)}
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}

// startLocal runs the client in the agent's own session (console mode).
func startLocal(path string, args []string) (windows.Handle, uint32, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	if err := cmd.Start(); err != nil {
		return 0, 0, err
	}
	pid := uint32(cmd.Process.Pid)
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, pid)
	go cmd.Wait()
	if err != nil {
		return 0, 0, err
	}
	return h, pid, nil
}

// startInSession starts the client on the interactive desktop of a user session.
func startInSession(path string, args []string, session uint32, asSystem bool) (windows.Handle, uint32, error) {
	var token windows.Token
	if asSystem {
		var self windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(),
			windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_SESSIONID|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY, &self); err != nil {
			return 0, 0, fmt.Errorf("open own token: %w", err)
		}
		defer self.Close()
		if err := windows.DuplicateTokenEx(self, windows.MAXIMUM_ALLOWED, nil, windows.SecurityIdentification, windows.TokenPrimary, &token); err != nil {
			return 0, 0, fmt.Errorf("duplicate token: %w", err)
		}
		sid := session
		if err := windows.SetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&sid)), uint32(unsafe.Sizeof(sid))); err != nil {
			token.Close()
			return 0, 0, fmt.Errorf("set token session: %w", err)
		}
	} else {
		var user windows.Token
		if err := windows.WTSQueryUserToken(session, &user); err != nil {
			return 0, 0, fmt.Errorf("query user token: %w", err)
		}
		defer user.Close()
		if err := windows.DuplicateTokenEx(user, windows.MAXIMUM_ALLOWED, nil, windows.SecurityIdentification, windows.TokenPrimary, &token); err != nil {
			return 0, 0, fmt.Errorf("duplicate token: %w", err)
		}
	}
	defer token.Close()

	var env *uint16
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if !asSystem {
		if err := windows.CreateEnvironmentBlock(&env, token, false); err == nil {
			defer windows.DestroyEnvironmentBlock(env)
		} else {
			env = nil
		}
	}
	if env == nil {
		flags &^= windows.CREATE_UNICODE_ENVIRONMENT
	}

	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	cmdline, err := windows.UTF16PtrFromString(quoteArgs(path, args))
	if err != nil {
		return 0, 0, err
	}
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(path))
	si := &windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(*si))
	pi := &windows.ProcessInformation{}
	if err := windows.CreateProcessAsUser(token, nil, cmdline, nil, nil, false, flags, env, dir, si, pi); err != nil {
		return 0, 0, fmt.Errorf("create process: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, pi.ProcessId, nil
}

// killStale ends lock-screen processes left over from a previous agent run.
func killStale(image string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), image) {
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pe.ProcessID); err == nil {
				windows.TerminateProcess(h, 0)
				windows.CloseHandle(h)
			}
		}
	}
}
