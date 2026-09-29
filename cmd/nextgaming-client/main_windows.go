//go:build windows

// Command nextgaming-client is the lock screen and session timer shown on
// each gaming PC. The agent starts it and restarts it if it is closed.
// It asks the agent for the state twice a second and fails closed: if the
// agent cannot be reached, the PC stays locked.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"nextgaming/internal/protocol"
	w "nextgaming/internal/winapi"
)

const (
	msgState  = w.WM_APP + 1
	msgResult = w.WM_APP + 2
	timerTick = 1
	timerHide = 2
)

// Colours.
var (
	colBg      = w.RGB(0x10, 0x14, 0x1a)
	colText    = w.RGB(0xf2, 0xf4, 0xf8)
	colMuted   = w.RGB(0x8a, 0x94, 0xa6)
	colAccent  = w.RGB(0x3b, 0x82, 0xf6)
	colWarn    = w.RGB(0xf5, 0x9e, 0x0b)
	colDanger  = w.RGB(0xef, 0x44, 0x44)
	colPanel   = w.RGB(0x1b, 0x20, 0x29)
	colButton  = w.RGB(0x33, 0x3b, 0x4a)
	colToastBg = w.RGB(0x1e, 0x3a, 0x8a)
)

type app struct {
	port   int
	client *http.Client

	// written by the poller goroutine, read on the UI thread
	mu       sync.Mutex
	polled   protocol.ClientState
	agentOK  bool
	lastGood time.Time

	// UI thread only
	st           protocol.ClientState
	locked       bool
	agentDown    bool
	lockWnd      uintptr
	overlayWnd   uintptr
	toastWnd     uintptr
	scale        float64
	toastText    string
	toastWarn    bool
	lastMsgID    int64
	msgSeen      bool
	warned       map[string]bool
	lastForce    time.Time
	lockMessage  string
	lockMsgUntil time.Time
	btn          w.RECT
	logoutBusy   bool
	resultText   string
}

var theApp *app

// hookLocked is read by the keyboard hook (same thread, but keep it simple).
var hookLocked = true

func main() {
	runtime.LockOSThread()
	port := flag.Int("port", protocol.DefaultClientPort, "agent local port")
	flag.Parse()

	if !w.SingleInstance(`Local\NextGamingClient`) {
		os.Exit(0)
	}
	w.SetDPIAware()

	a := &app{port: *port, client: &http.Client{Timeout: 1500 * time.Millisecond}, warned: map[string]bool{}, locked: true, lastMsgID: -1}
	a.scale = float64(w.SystemDPI()) / 96
	theApp = a

	wndProc := syscall.NewCallback(wndProc)
	for _, c := range []string{"NextGamingLock", "NextGamingOverlay", "NextGamingToast"} {
		if err := w.RegisterClass(c, wndProc); err != nil {
			fmt.Fprintln(os.Stderr, "register class:", err)
			os.Exit(1)
		}
	}
	vx, vy := w.GetSystemMetrics(w.SM_XVIRTUALSCREEN), w.GetSystemMetrics(w.SM_YVIRTUALSCREEN)
	vw, vh := w.GetSystemMetrics(w.SM_CXVIRTUALSCREEN), w.GetSystemMetrics(w.SM_CYVIRTUALSCREEN)
	a.lockWnd = w.CreateWindow(w.WS_EX_TOPMOST|w.WS_EX_TOOLWINDOW, "NextGamingLock", "NextGaming", w.WS_POPUP, vx, vy, vw, vh)

	ow, oh := a.px(340), a.px(84)
	sw := w.GetSystemMetrics(w.SM_CXSCREEN)
	a.overlayWnd = w.CreateWindow(w.WS_EX_TOPMOST|w.WS_EX_TOOLWINDOW|w.WS_EX_NOACTIVATE|w.WS_EX_LAYERED,
		"NextGamingOverlay", "NextGaming timer", w.WS_POPUP, sw-ow-a.px(16), a.px(16), ow, oh)
	w.SetLayeredAlpha(a.overlayWnd, 235)
	a.toastWnd = w.CreateWindow(w.WS_EX_TOPMOST|w.WS_EX_TOOLWINDOW|w.WS_EX_NOACTIVATE|w.WS_EX_LAYERED,
		"NextGamingToast", "NextGaming message", w.WS_POPUP, 0, 0, a.px(520), a.px(120))
	w.SetLayeredAlpha(a.toastWnd, 245)
	if a.lockWnd == 0 || a.overlayWnd == 0 || a.toastWnd == 0 {
		fmt.Fprintln(os.Stderr, "could not create windows")
		os.Exit(1)
	}

	hook := w.SetKeyboardHook(syscall.NewCallback(keyboardHook))
	defer w.Unhook(hook)
	defer w.Taskbar(true)

	// Start locked until the agent says otherwise.
	a.applyLock(true)
	w.SetTimer(a.lockWnd, timerTick, 500)
	go a.poll()

	var m w.MSG
	for w.GetMessage(&m) {
		w.TranslateMessage(&m)
		w.DispatchMessage(&m)
	}
}

func (a *app) px(v float64) int32 { return int32(v*a.scale + 0.5) }

// poll asks the agent for the state twice a second.
func (a *app) poll() {
	url := fmt.Sprintf("http://127.0.0.1:%d/state", a.port)
	for {
		var st protocol.ClientState
		ok := false
		if resp, err := a.client.Get(url); err == nil {
			if resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&st) == nil {
				ok = true
			}
			resp.Body.Close()
		}
		a.mu.Lock()
		if ok {
			a.polled, a.agentOK, a.lastGood = st, true, time.Now()
		} else if time.Since(a.lastGood) > 3*time.Second {
			a.agentOK = false // fail closed
		}
		a.mu.Unlock()
		w.PostMessage(a.lockWnd, msgState, 0, 0)
		time.Sleep(500 * time.Millisecond)
	}
}

// update runs on the UI thread whenever new state arrives.
func (a *app) update() {
	a.mu.Lock()
	st, ok := a.polled, a.agentOK
	a.mu.Unlock()
	a.st = st
	a.agentDown = !ok
	locked := !ok || st.Locked
	if locked != a.locked {
		a.applyLock(locked)
	}

	// Admin messages.
	if ok {
		if a.lastMsgID < 0 {
			a.lastMsgID = st.MessageID // do not replay an old message after a restart
		} else if st.MessageID > a.lastMsgID {
			a.lastMsgID = st.MessageID
			a.lockMessage, a.lockMsgUntil = st.Message, time.Now().Add(60*time.Second)
			if !locked {
				a.showToast("Message from the administrator:\n"+st.Message, false, 15*time.Second)
			}
			w.MessageBeep(w.MB_ICONWARNING)
		}
	}

	// Time warnings for prepaid sessions.
	if !locked && st.Mode == "prepaid" {
		for _, m := range st.WarnMinutes {
			key := fmt.Sprintf("%d-%d", st.SessionID, m)
			if !a.warned[key] && st.RemainingSec <= int64(m*60) && st.RemainingSec > int64(m*60-45) {
				a.warned[key] = true
				unit := "minutes"
				if m == 1 {
					unit = "minute"
				}
				a.showToast(fmt.Sprintf("%d %s left in your session.\nSee the administrator to add time.", m, unit), true, 10*time.Second)
				w.MessageBeep(w.MB_ICONWARNING)
			}
		}
	}

	if locked {
		// Stay above everything, and take keyboard focus back from games.
		w.SetWindowPos(a.lockWnd, w.HWND_TOPMOST, 0, 0, 0, 0, w.SWP_NOMOVE|w.SWP_NOSIZE|w.SWP_SHOWWINDOW)
		if time.Since(a.lastForce) > 2*time.Second {
			w.ForceForeground(a.lockWnd)
			w.Taskbar(false)
			a.lastForce = time.Now()
		}
		w.InvalidateRect(a.lockWnd)
	} else {
		w.SetWindowPos(a.overlayWnd, w.HWND_TOPMOST, 0, 0, 0, 0, w.SWP_NOMOVE|w.SWP_NOSIZE|w.SWP_NOACTIVATE)
		w.InvalidateRect(a.overlayWnd)
	}
}

func (a *app) applyLock(locked bool) {
	a.locked = locked
	hookLocked = locked
	if locked {
		w.ShowWindow(a.overlayWnd, w.SW_HIDE)
		w.ShowWindow(a.toastWnd, w.SW_HIDE)
		w.Taskbar(false)
		w.ShowWindow(a.lockWnd, w.SW_SHOW)
		w.SetWindowPos(a.lockWnd, w.HWND_TOPMOST, 0, 0, 0, 0, w.SWP_NOMOVE|w.SWP_NOSIZE|w.SWP_SHOWWINDOW)
		w.ForceForeground(a.lockWnd)
		a.lastForce = time.Now()
	} else {
		w.ShowWindow(a.lockWnd, w.SW_HIDE)
		w.Taskbar(true)
		w.ShowWindow(a.overlayWnd, w.SW_SHOWNOACTIVATE)
		a.warned = map[string]bool{}
		for _, m := range a.st.WarnMinutes { // no warning for thresholds already passed
			if a.st.Mode == "prepaid" && a.st.RemainingSec <= int64(m*60) {
				a.warned[fmt.Sprintf("%d-%d", a.st.SessionID, m)] = true
			}
		}
	}
}

func (a *app) showToast(text string, warn bool, d time.Duration) {
	a.toastText, a.toastWarn = text, warn
	width := a.px(520)
	textH := w.MeasureText(text, width-a.px(40), w.Font(a.px(20), w.FW_SEMIBOLD))
	height := textH + a.px(40)
	sw := w.GetSystemMetrics(w.SM_CXSCREEN)
	w.MoveWindow(a.toastWnd, (sw-width)/2, a.px(40), width, height)
	w.ShowWindow(a.toastWnd, w.SW_SHOWNOACTIVATE)
	w.SetWindowPos(a.toastWnd, w.HWND_TOPMOST, 0, 0, 0, 0, w.SWP_NOMOVE|w.SWP_NOSIZE|w.SWP_NOACTIVATE)
	w.InvalidateRect(a.toastWnd)
	w.KillTimer(a.toastWnd, timerHide)
	w.SetTimer(a.toastWnd, timerHide, uint32(d/time.Millisecond))
}

// logout asks the agent to end the session (the admin settles payment).
func (a *app) logout() {
	if a.logoutBusy || a.locked {
		return
	}
	if w.MessageBox(a.overlayWnd, "End your session now?\n\nThe PC will lock. Please go to the desk to settle payment.",
		"NextGaming", w.MB_YESNO|w.MB_ICONQUESTION|w.MB_TOPMOST|w.MB_SETFOREGROUND) != w.IDYES {
		return
	}
	a.logoutBusy = true
	sid := a.st.SessionID
	go func() {
		body, _ := json.Marshal(map[string]int64{"sessionId": sid})
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/logout", a.port), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-NextGaming-Client", "1")
		text := ""
		if resp, err := a.client.Do(req); err != nil {
			text = "Could not end the session. Please ask the administrator."
		} else {
			var r struct{ Error string }
			json.NewDecoder(resp.Body).Decode(&r)
			resp.Body.Close()
			if r.Error != "" {
				text = r.Error
			}
		}
		a.mu.Lock()
		a.resultText = text
		a.mu.Unlock()
		w.PostMessage(a.lockWnd, msgResult, 0, 0)
	}()
}

// ---- window procedure ----

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	a := theApp
	switch msg {
	case msgState:
		a.update()
		return 0
	case msgResult:
		a.mu.Lock()
		text := a.resultText
		a.mu.Unlock()
		a.logoutBusy = false
		if text != "" {
			a.showToast(text, true, 8*time.Second)
		}
		return 0
	case w.WM_TIMER:
		if hwnd == a.toastWnd && wp == timerHide {
			w.KillTimer(hwnd, timerHide)
			w.ShowWindow(hwnd, w.SW_HIDE)
		}
		if hwnd == a.lockWnd && wp == timerTick {
			if a.locked {
				w.InvalidateRect(a.lockWnd) // clock
			}
		}
		return 0
	case w.WM_ERASEBKGND:
		return 1
	case w.WM_PAINT:
		switch hwnd {
		case a.lockWnd:
			a.paintLock(hwnd)
		case a.overlayWnd:
			a.paintOverlay(hwnd)
		case a.toastWnd:
			a.paintToast(hwnd)
		}
		return 0
	case w.WM_MOUSEACTIVATE:
		if hwnd != a.lockWnd {
			return w.MA_NOACTIVATE
		}
	case w.WM_NCHITTEST:
		if hwnd == a.overlayWnd {
			// Let the player drag the timer anywhere, except over the button.
			x := int32(int16(lp & 0xFFFF))
			y := int32(int16((lp >> 16) & 0xFFFF))
			r := overlayScreenRect(hwnd)
			cx, cy := x-r.Left, y-r.Top
			if cx >= a.btn.Left && cx <= a.btn.Right && cy >= a.btn.Top && cy <= a.btn.Bottom {
				return w.HTCLIENT
			}
			return w.HTCAPTION
		}
	case w.WM_LBUTTONUP:
		if hwnd == a.overlayWnd {
			a.logout()
			return 0
		}
		if hwnd == a.toastWnd {
			w.ShowWindow(hwnd, w.SW_HIDE)
			return 0
		}
	case w.WM_CLOSE:
		return 0 // the lock screen cannot be closed by the user
	case w.WM_SYSCOMMAND:
		if wp&0xFFF0 == w.SC_CLOSE {
			return 0
		}
	case w.WM_QUERYENDSESSION:
		return 1 // allow Windows to shut down
	case w.WM_ENDSESSION:
		if wp != 0 {
			w.Taskbar(true)
			os.Exit(0)
		}
		return 0
	case w.WM_DISPLAYCHANGE:
		if hwnd == a.lockWnd {
			vx, vy := w.GetSystemMetrics(w.SM_XVIRTUALSCREEN), w.GetSystemMetrics(w.SM_YVIRTUALSCREEN)
			vw, vh := w.GetSystemMetrics(w.SM_CXVIRTUALSCREEN), w.GetSystemMetrics(w.SM_CYVIRTUALSCREEN)
			w.MoveWindow(hwnd, vx, vy, vw, vh)
		}
	case w.WM_DESTROY:
		if hwnd == a.lockWnd {
			w.PostQuitMessage(0)
		}
		return 0
	}
	return w.DefWindowProc(hwnd, msg, wp, lp)
}

var pGetWindowRect = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowRect")

func overlayScreenRect(hwnd uintptr) w.RECT {
	var r w.RECT
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

// keyboardHook blocks task-switching keys while the PC is locked.
func keyboardHook(code int, wp, lp uintptr) uintptr {
	if code == w.HC_ACTION && hookLocked {
		// lp points to OS-owned memory; convert without a uintptr->Pointer cast.
		k := *(**w.KBDLLHOOKSTRUCT)(unsafe.Pointer(&lp))
		alt := k.Flags&w.LLKHF_ALTDOWN != 0
		ctrl := w.KeyDown(w.VK_CONTROL)
		switch {
		case k.VkCode == w.VK_LWIN, k.VkCode == w.VK_RWIN, k.VkCode == w.VK_APPS:
			return 1
		case k.VkCode == w.VK_TAB && alt: // Alt+Tab
			return 1
		case k.VkCode == w.VK_ESCAPE && (alt || ctrl): // Alt+Esc, Ctrl+Esc, Ctrl+Shift+Esc
			return 1
		case k.VkCode == w.VK_F4 && alt: // Alt+F4
			return 1
		}
	}
	return w.CallNextHook(code, wp, lp)
}

// ---- painting ----

func (a *app) paintLock(hwnd uintptr) {
	p := w.BeginPaint(hwnd)
	defer p.End()
	p.Fill(w.RECT{Right: p.W, Bottom: p.H}, colBg)

	// Centre on the primary monitor, which starts at (0,0) in screen space.
	ox := -w.GetSystemMetrics(w.SM_XVIRTUALSCREEN)
	oy := -w.GetSystemMetrics(w.SM_YVIRTUALSCREEN)
	sw, sh := w.GetSystemMetrics(w.SM_CXSCREEN), w.GetSystemMetrics(w.SM_CYSCREEN)
	line := func(text string, y, height int32, weight int32, color uintptr, frac float64) {
		r := w.RECT{Left: ox + sw/20, Top: oy + int32(float64(sh)*frac), Right: ox + sw - sw/20, Bottom: oy + int32(float64(sh)*frac) + height*2}
		p.Text(text, r, w.Font(height, weight), color, w.DT_CENTER|w.DT_SINGLELINE|w.DT_END_ELLIPSIS)
		_ = y
	}

	club := a.st.ClubName
	if club == "" {
		club = "NextGaming"
	}
	name := a.st.PCName
	if name == "" {
		name, _ = os.Hostname()
	}
	status, statusCol := "Please see the administrator to start a session", colText
	switch {
	case a.agentDown:
		status, statusCol = "Connecting to the club system…", colWarn
	case a.st.Paused:
		status, statusCol = "Session paused — please see the administrator", colWarn
	}

	line(club, 0, sh/24, w.FW_SEMIBOLD, colMuted, 0.24)
	line(name, 0, sh/7, w.FW_BOLD, colText, 0.33)
	line(status, 0, sh/30, w.FW_NORMAL, statusCol, 0.56)
	line(time.Now().Format("15:04"), 0, sh/22, w.FW_SEMIBOLD, colMuted, 0.64)

	if a.lockMessage != "" && time.Now().Before(a.lockMsgUntil) {
		font := w.Font(sh/34, w.FW_SEMIBOLD)
		width := sw * 6 / 10
		th := w.MeasureText(a.lockMessage, width-sh/20, font)
		box := w.RECT{Left: ox + (sw-width)/2, Top: oy + sh*74/100, Right: ox + (sw+width)/2, Bottom: oy + sh*74/100 + th + sh/25}
		p.Fill(box, colPanel)
		inner := w.RECT{Left: box.Left + sh/40, Top: box.Top + sh/50, Right: box.Right - sh/40, Bottom: box.Bottom}
		p.Text(a.lockMessage, inner, font, colWarn, w.DT_CENTER|w.DT_WORDBREAK)
	}
	p.Text("NextGaming", w.RECT{Left: ox, Top: oy + sh - sh/18, Right: ox + sw, Bottom: oy + sh}, w.Font(sh/60, w.FW_NORMAL), colMuted, w.DT_CENTER|w.DT_SINGLELINE)
}

func (a *app) paintOverlay(hwnd uintptr) {
	p := w.BeginPaint(hwnd)
	defer p.End()
	p.Fill(w.RECT{Right: p.W, Bottom: p.H}, colPanel)
	p.Fill(w.RECT{Right: a.px(4), Bottom: p.H}, colAccent)
	pad := a.px(14)

	label, secs := "used", a.st.ElapsedSec
	timeCol := colText
	if a.st.Mode == "prepaid" {
		label, secs = "left", a.st.RemainingSec
		if secs < 300 {
			timeCol = colDanger
		}
	}
	title := a.st.PCName
	if a.st.Mode == "free" {
		title += " · staff"
	}
	if !a.st.ServerOnline {
		title += " · offline"
	}
	p.Text(title, w.RECT{Left: pad, Top: a.px(8), Right: p.W - a.px(124), Bottom: a.px(30)}, w.Font(a.px(15), w.FW_SEMIBOLD), colMuted, w.DT_SINGLELINE|w.DT_END_ELLIPSIS)
	p.Text(fmt.Sprintf("%s %s", fmtDur(secs), label), w.RECT{Left: pad, Top: a.px(30), Right: p.W - a.px(124), Bottom: p.H - a.px(6)},
		w.Font(a.px(30), w.FW_BOLD), timeCol, w.DT_SINGLELINE|w.DT_VCENTER)

	a.btn = w.RECT{Left: p.W - a.px(118), Top: a.px(24), Right: p.W - a.px(12), Bottom: a.px(62)}
	p.Fill(a.btn, colButton)
	p.Text("End session", a.btn, w.Font(a.px(14), w.FW_SEMIBOLD), colText, w.DT_CENTER|w.DT_VCENTER|w.DT_SINGLELINE)
}

func (a *app) paintToast(hwnd uintptr) {
	p := w.BeginPaint(hwnd)
	defer p.End()
	bg := colToastBg
	if a.toastWarn {
		bg = w.RGB(0x92, 0x40, 0x0e)
	}
	p.Fill(w.RECT{Right: p.W, Bottom: p.H}, bg)
	pad := a.px(20)
	p.Text(a.toastText, w.RECT{Left: pad, Top: pad, Right: p.W - pad, Bottom: p.H - pad/2}, w.Font(a.px(20), w.FW_SEMIBOLD), colText, w.DT_CENTER|w.DT_WORDBREAK)
}

func fmtDur(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
}
