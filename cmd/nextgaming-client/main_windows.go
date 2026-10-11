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

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"nextgaming/internal/protocol"
	w "nextgaming/internal/winapi"
)

const (
	msgState       = w.WM_APP + 1
	msgShowLogin   = w.WM_APP + 3
	msgDoLogin     = w.WM_APP + 4
	msgCancelLogin = w.WM_APP + 5
	msgLoginResult = w.WM_APP + 6
	timerTick      = 1
	timerHide      = 2

	idUser   = 101
	idPass   = 102
	idLogin  = 103
	idCancel = 104
	idStaff  = 105
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

	// staff login on the lock screen
	loginMode  bool
	loginBusy  bool
	loginMsg   string
	userEdit   uintptr
	passEdit   uintptr
	loginBtn   uintptr
	cancelBtn  uintptr
	staffBtn   uintptr
	loginLabel string
	loginErr   string
}

var theApp *app

// hookLocked is read by the keyboard hook (same thread, but keep it simple).
var hookLocked = true

// hookLoginMode is true while the staff-login box is open, so the keyboard
// hook lets typing reach the text boxes instead of swallowing it.
var hookLoginMode = false

func main() {
	runtime.LockOSThread()
	port := flag.Int("port", protocol.DefaultClientPort, "agent local port")
	captureMode := flag.Bool("capture", false, "run only as the screen-capture helper (started by the agent as the logged-in user)")
	flag.Parse()

	// Capture-only helper: no window, no keyboard hook, no single-instance lock.
	// It runs as the logged-in user (GDI capture from a SYSTEM process is black),
	// alongside the real lock screen which stays SYSTEM.
	if *captureMode {
		w.SetDPIAware()
		(&app{port: *port}).captureLoop()
		return
	}

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
	a.lockWnd = w.CreateWindow(w.WS_EX_TOPMOST|w.WS_EX_TOOLWINDOW|w.WS_EX_CONTROLPARENT, "NextGamingLock", "NextGaming", w.WS_POPUP, vx, vy, vw, vh)

	ow, oh := a.px(300), a.px(104)
	sw := w.GetSystemMetrics(w.SM_CXSCREEN)
	// The timer is a normal window (not always-on-top), so games and other
	// apps cover it. Only short warnings and admin messages pop up on top.
	a.overlayWnd = w.CreateWindow(w.WS_EX_TOOLWINDOW|w.WS_EX_NOACTIVATE|w.WS_EX_LAYERED,
		"NextGamingOverlay", "NextGaming timer", w.WS_POPUP, sw-ow-a.px(16), a.px(16), ow, oh)
	w.SetLayeredAlpha(a.overlayWnd, 235)
	a.toastWnd = w.CreateWindow(w.WS_EX_TOPMOST|w.WS_EX_TOOLWINDOW|w.WS_EX_NOACTIVATE|w.WS_EX_LAYERED,
		"NextGamingToast", "NextGaming message", w.WS_POPUP, 0, 0, a.px(520), a.px(120))
	w.SetLayeredAlpha(a.toastWnd, 245)
	if a.lockWnd == 0 || a.overlayWnd == 0 || a.toastWnd == 0 {
		fmt.Fprintln(os.Stderr, "could not create windows")
		os.Exit(1)
	}

	a.createLoginControls()

	hook := w.SetKeyboardHook(syscall.NewCallback(keyboardHook))
	defer w.Unhook(hook)
	defer w.Taskbar(true)

	// Start locked until the agent says otherwise.
	a.applyLock(true)
	w.SetTimer(a.lockWnd, timerTick, 500)
	go a.poll()
	// Screen capture runs in a separate helper (started by the agent as the
	// logged-in user), not here: this process may be SYSTEM, whose GDI screen
	// grab comes back black.

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
		down := !ok && time.Since(a.lastGood) > 5*time.Second
		a.mu.Unlock()
		if down {
			restartAgent()
		}
		w.PostMessage(a.lockWnd, msgState, 0, 0)
		time.Sleep(500 * time.Millisecond)
	}
}

// captureLoop long-polls the agent for a screen-capture request (made only
// while an admin is watching the Screens tab), grabs the primary monitor and
// posts the JPEG back. It idles with no cost when nobody is watching.
func (a *app) captureLoop() {
	base := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	waitClient := &http.Client{Timeout: 35 * time.Second}
	postClient := &http.Client{Timeout: 10 * time.Second}
	cap := newScreenCapturer()
	defer cap.close()
	for {
		req, _ := http.NewRequest("GET", base+"/capture-wait", nil)
		req.Header.Set("X-NextGaming-Client", "1")
		resp, err := waitClient.Do(req)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue // 204: no request this round, poll again
		}
		var cmd struct{ Seq, Width, Quality int }
		json.NewDecoder(resp.Body).Decode(&cmd)
		resp.Body.Close()
		if cmd.Width <= 0 {
			continue
		}
		data, cw, ch, err := cap.jpeg(cmd.Width, cmd.Quality)
		if err != nil || len(data) == 0 {
			continue
		}
		pr, _ := http.NewRequest("POST", fmt.Sprintf("%s/screen?w=%d&h=%d", base, cw, ch), bytes.NewReader(data))
		pr.Header.Set("X-NextGaming-Client", "1")
		pr.Header.Set("Content-Type", "image/jpeg")
		if rp, err := postClient.Do(pr); err == nil {
			rp.Body.Close()
		}
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
		if !a.loginMode {
			a.placeStaffButton()
			w.ShowChild(a.staffBtn, true)
		}
	} else {
		a.hideLogin()
		w.ShowChild(a.staffBtn, false)
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

// ---- staff login box ----

func (a *app) createLoginControls() {
	font := w.Font(a.px(18), w.FW_NORMAL)
	a.userEdit = w.CreateChild(a.lockWnd, "EDIT", "", w.WS_BORDER|w.ES_AUTOHSCROLL|w.WS_TABSTOP, 0, 0, 10, 10, idUser)
	a.passEdit = w.CreateChild(a.lockWnd, "EDIT", "", w.WS_BORDER|w.ES_AUTOHSCROLL|w.ES_PASSWORD|w.WS_TABSTOP, 0, 0, 10, 10, idPass)
	a.loginBtn = w.CreateChild(a.lockWnd, "BUTTON", "Log in", w.BS_DEFPUSHBUTTON|w.WS_TABSTOP, 0, 0, 10, 10, idLogin)
	a.cancelBtn = w.CreateChild(a.lockWnd, "BUTTON", "Cancel", w.WS_TABSTOP, 0, 0, 10, 10, idCancel)
	for _, c := range []uintptr{a.userEdit, a.passEdit, a.loginBtn, a.cancelBtn} {
		w.SetControlFont(c, font)
		w.ShowChild(c, false)
	}
	// Always-visible "Staff log in" button on the lock screen (opens the box).
	a.staffBtn = w.CreateChild(a.lockWnd, "BUTTON", "Staff log in", w.WS_TABSTOP, 0, 0, 10, 10, idStaff)
	w.SetControlFont(a.staffBtn, font)
	w.ShowChild(a.staffBtn, false)
}

// placeStaffButton centres the staff button near the bottom of the primary monitor.
func (a *app) placeStaffButton() {
	ox := -w.GetSystemMetrics(w.SM_XVIRTUALSCREEN)
	oy := -w.GetSystemMetrics(w.SM_YVIRTUALSCREEN)
	sw, sh := w.GetSystemMetrics(w.SM_CXSCREEN), w.GetSystemMetrics(w.SM_CYSCREEN)
	bw, bh := a.px(220), a.px(48)
	w.MoveControl(a.staffBtn, int32(ox)+(sw-bw)/2, int32(oy)+sh-sh/6, bw, bh)
}

// placeLoginControls lays the box out centered on the primary monitor.
func (a *app) placeLoginControls() {
	ox := -w.GetSystemMetrics(w.SM_XVIRTUALSCREEN)
	oy := -w.GetSystemMetrics(w.SM_YVIRTUALSCREEN)
	sw, sh := w.GetSystemMetrics(w.SM_CXSCREEN), w.GetSystemMetrics(w.SM_CYSCREEN)
	fw := a.px(300)
	x := int32(ox) + (sw-fw)/2
	y := int32(oy) + sh/2 - a.px(30)
	rowH := a.px(34)
	w.MoveControl(a.userEdit, x, y, fw, rowH)
	w.MoveControl(a.passEdit, x, y+rowH+a.px(10), fw, rowH)
	by := y + 2*(rowH+a.px(10))
	bw := (fw - a.px(10)) / 2
	w.MoveControl(a.loginBtn, x, by, bw, rowH)
	w.MoveControl(a.cancelBtn, x+bw+a.px(10), by, bw, rowH)
}

func (a *app) showLogin() {
	if a.loginMode || !a.locked {
		return
	}
	a.loginMode = true
	hookLoginMode = true
	a.loginMsg = ""
	w.ShowChild(a.staffBtn, false)
	a.placeLoginControls()
	w.SetText(a.userEdit, "")
	w.SetText(a.passEdit, "")
	for _, c := range []uintptr{a.userEdit, a.passEdit, a.loginBtn, a.cancelBtn} {
		w.Enable(c, true)
		w.ShowChild(c, true)
	}
	w.ForceForeground(a.lockWnd)
	w.SetFocus(a.userEdit)
	w.InvalidateRect(a.lockWnd)
}

func (a *app) hideLogin() {
	if !a.loginMode {
		return
	}
	a.loginMode = false
	hookLoginMode = false
	a.loginMsg = ""
	for _, c := range []uintptr{a.userEdit, a.passEdit, a.loginBtn, a.cancelBtn} {
		w.ShowChild(c, false)
	}
	if a.locked {
		a.placeStaffButton()
		w.ShowChild(a.staffBtn, true)
	}
	w.SetFocus(a.lockWnd)
	w.InvalidateRect(a.lockWnd)
}

func (a *app) doLogin() {
	if a.loginBusy {
		return
	}
	user := w.GetText(a.userEdit)
	pass := w.GetText(a.passEdit)
	if user == "" || pass == "" {
		a.loginMsg = "Enter your username and password."
		w.InvalidateRect(a.lockWnd)
		return
	}
	a.loginBusy = true
	a.loginMsg = "Checking…"
	w.Enable(a.loginBtn, false)
	w.Enable(a.cancelBtn, false)
	w.InvalidateRect(a.lockWnd)
	go func() {
		body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/staff-login", a.port), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-NextGaming-Client", "1")
		errText := ""
		c := &http.Client{Timeout: 10 * time.Second}
		if resp, err := c.Do(req); err != nil {
			errText = "Cannot reach the club server."
		} else {
			var r struct{ Error string }
			json.NewDecoder(resp.Body).Decode(&r)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				errText = r.Error
				if errText == "" {
					errText = "Login failed."
				}
			}
		}
		a.mu.Lock()
		a.loginErr = errText
		a.mu.Unlock()
		w.PostMessage(a.lockWnd, msgLoginResult, 0, 0)
	}()
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

// ---- window procedure ----

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	a := theApp
	switch msg {
	case msgState:
		a.update()
		return 0
	case msgShowLogin:
		a.showLogin()
		return 0
	case msgCancelLogin:
		a.hideLogin()
		return 0
	case msgDoLogin:
		a.doLogin()
		return 0
	case msgLoginResult:
		a.loginBusy = false
		w.Enable(a.loginBtn, true)
		w.Enable(a.cancelBtn, true)
		a.mu.Lock()
		loginErr := a.loginErr
		a.mu.Unlock()
		a.loginErr = loginErr
		if loginErr == "" {
			a.hideLogin() // the server will unlock the PC via a state push
		} else {
			a.loginMsg = a.loginErr
			w.SetText(a.passEdit, "")
			w.SetFocus(a.passEdit)
			w.InvalidateRect(a.lockWnd)
		}
		return 0
	case w.WM_COMMAND:
		switch w.LoWord(wp) {
		case idStaff:
			if !a.loginMode {
				a.showLogin()
			}
		case idLogin:
			if a.loginMode {
				a.doLogin()
			}
		case idCancel:
			if a.loginMode {
				a.hideLogin()
			}
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
			return w.HTCAPTION // the player can drag the timer anywhere
		}
	case w.WM_LBUTTONUP:
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

// keyboardHook blocks task-switching keys while the PC is locked, and opens /
// drives the staff-login box. In login mode normal keys pass through to the
// text boxes; only escape keys and the hotkey are intercepted.
func keyboardHook(code int, wp, lp uintptr) uintptr {
	if code == w.HC_ACTION && hookLocked {
		// lp points to OS-owned memory; convert without a uintptr->Pointer cast.
		k := *(**w.KBDLLHOOKSTRUCT)(unsafe.Pointer(&lp))
		alt := k.Flags&w.LLKHF_ALTDOWN != 0
		ctrl := w.KeyDown(w.VK_CONTROL)
		down := wp == w.WM_KEYDOWN || wp == w.WM_SYSKEYDOWN

		// The hotkey Ctrl+Alt+L opens the staff-login box.
		if down && ctrl && alt && k.VkCode == 'L' && !hookLoginMode {
			w.PostMessage(theApp.lockWnd, msgShowLogin, 0, 0)
			return 1
		}

		// Always block the OS task-switching keys.
		switch {
		case k.VkCode == w.VK_LWIN, k.VkCode == w.VK_RWIN, k.VkCode == w.VK_APPS:
			return 1
		case k.VkCode == w.VK_TAB && alt:
			return 1
		case k.VkCode == w.VK_ESCAPE && (alt || ctrl):
			return 1
		case k.VkCode == w.VK_F4 && alt:
			return 1
		}

		if hookLoginMode {
			// Enter submits, Escape cancels; everything else goes to the text boxes.
			if down && k.VkCode == w.VK_RETURN {
				w.PostMessage(theApp.lockWnd, msgDoLogin, 0, 0)
				return 1
			}
			if down && k.VkCode == w.VK_ESCAPE {
				w.PostMessage(theApp.lockWnd, msgCancelLogin, 0, 0)
				return 1
			}
			return w.CallNextHook(code, wp, lp) // let typing reach the boxes
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
	if a.loginMode {
		fw := a.px(300)
		cx := int32(ox) + (sw-fw)/2
		top := int32(oy) + sh/2 - a.px(84)
		// Backdrop behind the native text boxes/buttons.
		panel := w.RECT{Left: cx - a.px(24), Top: top, Right: cx + fw + a.px(24), Bottom: int32(oy) + sh/2 + a.px(96)}
		p.Fill(panel, colPanel)
		p.Text("Staff log in", w.RECT{Left: panel.Left, Top: top + a.px(10), Right: panel.Right, Bottom: top + a.px(44)},
			w.Font(a.px(22), w.FW_SEMIBOLD), colText, w.DT_CENTER|w.DT_SINGLELINE)
		p.Text("Username", w.RECT{Left: cx, Top: int32(oy) + sh/2 - a.px(48), Right: cx + fw, Bottom: int32(oy) + sh/2 - a.px(30)},
			w.Font(a.px(13), w.FW_NORMAL), colMuted, w.DT_SINGLELINE)
		if a.loginMsg != "" {
			col := colMuted
			if a.loginErr != "" && !a.loginBusy {
				col = colDanger
			}
			p.Text(a.loginMsg, w.RECT{Left: panel.Left, Top: int32(oy) + sh/2 + a.px(60), Right: panel.Right, Bottom: int32(oy) + sh/2 + a.px(92)},
				w.Font(a.px(14), w.FW_NORMAL), col, w.DT_CENTER|w.DT_SINGLELINE|w.DT_END_ELLIPSIS)
		}
	}
	// (The "Staff log in" button is a child control, drawn by Windows.)
	p.Text("NextGaming", w.RECT{Left: ox, Top: oy + sh - sh/18, Right: ox + sw, Bottom: oy + sh}, w.Font(sh/60, w.FW_NORMAL), colMuted, w.DT_CENTER|w.DT_SINGLELINE)
}

func (a *app) paintOverlay(hwnd uintptr) {
	p := w.BeginPaint(hwnd)
	defer p.End()
	p.Fill(w.RECT{Right: p.W, Bottom: p.H}, colPanel)
	p.Fill(w.RECT{Right: a.px(4), Bottom: p.H}, colAccent)
	pad := a.px(14)
	st := a.st

	// Line 1: PC and tariff.
	title := st.PCName
	switch {
	case st.Mode == "free":
		title += " · Staff"
	case st.TariffName != "":
		title += " · " + st.TariffName
	}
	if !st.ServerOnline {
		title += " · offline"
	}
	p.Text(title, w.RECT{Left: pad, Top: a.px(8), Right: p.W - pad, Bottom: a.px(28)},
		w.Font(a.px(14), w.FW_SEMIBOLD), colMuted, w.DT_SINGLELINE|w.DT_END_ELLIPSIS)

	// Line 2: the timer.
	label, secs, timeCol := "used", st.ElapsedSec, colText
	if st.Mode == "prepaid" {
		label, secs = "left", st.RemainingSec
		if secs < 300 {
			timeCol = colDanger
		}
	}
	p.Text(fmt.Sprintf("%s %s", fmtDur(secs), label), w.RECT{Left: pad, Top: a.px(28), Right: p.W - pad, Bottom: a.px(66)},
		w.Font(a.px(28), w.FW_BOLD), timeCol, w.DT_SINGLELINE|w.DT_VCENTER)

	// Line 3: rate and cost.
	info := ""
	switch st.Mode {
	case "prepaid":
		info = fmt.Sprintf("%s/h  ·  Paid %s", fmtMoney(st.RatePerHour, st.Currency), fmtMoney(st.Cost, st.Currency))
	case "open":
		info = fmt.Sprintf("%s/h  ·  Cost now %s", fmtMoney(st.RatePerHour, st.Currency), fmtMoney(st.Cost, st.Currency))
	case "free":
		info = "Not billed"
	}
	p.Text(info, w.RECT{Left: pad, Top: a.px(68), Right: p.W - pad, Bottom: p.H - a.px(8)},
		w.Font(a.px(15), w.FW_NORMAL), colText, w.DT_SINGLELINE|w.DT_END_ELLIPSIS)
}

// fmtMoney renders minor units as "12.50 TMT".
func fmtMoney(v int64, currency string) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	s := fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
	if currency != "" {
		s += " " + currency
	}
	return s
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

var lastAgentStart time.Time

// restartAgent starts the agent service if someone stopped it. The lock
// screen runs as SYSTEM, so it may do this; the agent in turn restarts the
// lock screen. Each one watches the other.
func restartAgent() {
	if time.Since(lastAgentStart) < 5*time.Second {
		return
	}
	lastAgentStart = time.Now()
	m, err := mgr.Connect()
	if err != nil {
		return // not allowed (lock screen running as a normal user)
	}
	defer m.Disconnect()
	s, err := m.OpenService(agentService)
	if err != nil {
		return
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State == svc.Stopped {
		s.Start()
	}
}

const agentService = "NextGamingAgent"
