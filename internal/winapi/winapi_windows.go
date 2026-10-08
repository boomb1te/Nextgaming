//go:build windows

// Package winapi is a minimal set of Win32 bindings for the lock screen.
// Only what the client needs is here; everything is called through
// user32/gdi32/kernel32 directly, so the client has no GUI framework.
package winapi

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	pGetWindowThreadProcessId      = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput             = user32.NewProc("AttachThreadInput")
	pBringWindowToTop              = user32.NewProc("BringWindowToTop")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pBeginPaint                    = user32.NewProc("BeginPaint")
	pEndPaint                      = user32.NewProc("EndPaint")
	pFillRect                      = user32.NewProc("FillRect")
	pInvalidateRect                = user32.NewProc("InvalidateRect")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pSetTimer                      = user32.NewProc("SetTimer")
	pKillTimer                     = user32.NewProc("KillTimer")
	pSetWindowsHookExW             = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx                = user32.NewProc("CallNextHookEx")
	pUnhookWindowsHookEx           = user32.NewProc("UnhookWindowsHookEx")
	pGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pSetLayeredWindowAttributes    = user32.NewProc("SetLayeredWindowAttributes")
	pIsWindowVisible               = user32.NewProc("IsWindowVisible")
	pDrawTextW                     = user32.NewProc("DrawTextW")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	pMessageBeep                   = user32.NewProc("MessageBeep")
	pFindWindowW                   = user32.NewProc("FindWindowW")
	pFindWindowExW                 = user32.NewProc("FindWindowExW")
	pMoveWindow                    = user32.NewProc("MoveWindow")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pSetFocus                      = user32.NewProc("SetFocus")
	pGetWindowTextW                = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW          = user32.NewProc("GetWindowTextLengthW")
	pSetWindowTextW                = user32.NewProc("SetWindowTextW")
	pSendMessageW                  = user32.NewProc("SendMessageW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pEnableWindow                  = user32.NewProc("EnableWindow")

	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pDeleteDC               = gdi32.NewProc("DeleteDC")

	pGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pCreateMutexW       = kernel32.NewProc("CreateMutexW")
)

// Window messages and constants.
const (
	WM_DESTROY           = 0x0002
	WM_PAINT             = 0x000F
	WM_CLOSE             = 0x0010
	WM_QUERYENDSESSION   = 0x0011
	WM_ERASEBKGND        = 0x0014
	WM_ENDSESSION        = 0x0016
	WM_SETCURSOR         = 0x0020
	WM_MOUSEACTIVATE     = 0x0021
	WM_NCHITTEST         = 0x0084
	WM_KEYDOWN           = 0x0100
	WM_SYSKEYDOWN        = 0x0104
	WM_SYSCOMMAND        = 0x0112
	WM_TIMER             = 0x0113
	WM_LBUTTONUP         = 0x0202
	WM_DISPLAYCHANGE     = 0x007E
	WM_APP               = 0x8000
	HTCLIENT             = 1
	HTCAPTION            = 2
	MA_NOACTIVATE        = 3
	SC_CLOSE             = 0xF060
	WS_POPUP             = 0x80000000
	WS_EX_TOPMOST        = 0x00000008
	WS_EX_TOOLWINDOW     = 0x00000080
	WS_EX_LAYERED        = 0x00080000
	WS_EX_NOACTIVATE     = 0x08000000
	SW_HIDE              = 0
	SW_SHOW              = 5
	SW_SHOWNOACTIVATE    = 4
	SWP_NOSIZE           = 0x0001
	SWP_NOMOVE           = 0x0002
	SWP_NOACTIVATE       = 0x0010
	SWP_SHOWWINDOW       = 0x0040
	HWND_TOPMOST         = ^uintptr(0) // (HWND)-1
	SM_CXSCREEN          = 0
	SM_CYSCREEN          = 1
	SM_XVIRTUALSCREEN    = 76
	SM_YVIRTUALSCREEN    = 77
	SM_CXVIRTUALSCREEN   = 78
	SM_CYVIRTUALSCREEN   = 79
	WH_KEYBOARD_LL       = 13
	HC_ACTION            = 0
	LLKHF_ALTDOWN        = 0x20
	VK_TAB               = 0x09
	VK_SHIFT             = 0x10
	VK_CONTROL           = 0x11
	VK_ESCAPE            = 0x1B
	VK_LWIN              = 0x5B
	VK_RWIN              = 0x5C
	VK_APPS              = 0x5D
	VK_F4                = 0x73
	IDC_ARROW            = 32512
	MB_YESNO             = 0x04
	MB_ICONQUESTION      = 0x20
	MB_TOPMOST           = 0x40000
	MB_SETFOREGROUND     = 0x10000
	IDYES                = 6
	MB_ICONWARNING       = 0x30
	LWA_ALPHA            = 0x2
	TRANSPARENT          = 1
	DT_CENTER            = 0x01
	DT_VCENTER           = 0x04
	DT_WORDBREAK         = 0x10
	DT_SINGLELINE        = 0x20
	DT_NOPREFIX          = 0x800
	DT_END_ELLIPSIS      = 0x8000
	DT_LEFT              = 0x00
	DT_CALCRECT          = 0x400
	SRCCOPY              = 0x00CC0020
	FW_NORMAL            = 400
	FW_SEMIBOLD          = 600
	FW_BOLD              = 700
	CLEARTYPE_QUALITY    = 5
	ERROR_ALREADY_EXISTS = 183

	WS_CHILD            = 0x40000000
	WS_VISIBLE          = 0x10000000
	WS_BORDER           = 0x00800000
	WS_TABSTOP          = 0x00010000
	ES_AUTOHSCROLL      = 0x0080
	ES_PASSWORD         = 0x0020
	BS_DEFPUSHBUTTON    = 0x0001
	WM_COMMAND          = 0x0111
	WM_SETFONT          = 0x0030
	WM_GETDLGCODE       = 0x0087
	BN_CLICKED          = 0
	WS_EX_CONTROLPARENT = 0x00010000
	VK_RETURN           = 0x0D
)

// POINT, RECT, MSG, PAINTSTRUCT, WNDCLASSEXW and KBDLLHOOKSTRUCT mirror the Win32 structs.
type POINT struct{ X, Y int32 }

type RECT struct{ Left, Top, Right, Bottom int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	Private uint32
}

type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type KBDLLHOOKSTRUCT struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// RGB builds a COLORREF.
func RGB(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

// UTF16 converts a Go string for Win32.
func UTF16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func ModuleHandle() uintptr { r, _, _ := pGetModuleHandleW.Call(0); return r }

func RegisterClass(name string, wndProc uintptr) error {
	wc := WNDCLASSEXW{LpfnWndProc: wndProc, HInstance: ModuleHandle(), LpszClassName: UTF16(name)}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.HCursor, _, _ = pLoadCursorW.Call(0, IDC_ARROW)
	r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 {
		return err
	}
	return nil
}

func CreateWindow(exStyle uint32, class, title string, style uint32, x, y, w, h int32) uintptr {
	r, _, _ := pCreateWindowExW.Call(uintptr(exStyle), uintptr(unsafe.Pointer(UTF16(class))), uintptr(unsafe.Pointer(UTF16(title))),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, ModuleHandle(), 0)
	return r
}

func DefWindowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), w, l)
	return r
}

// GetMessage returns false when WM_QUIT arrives.
func GetMessage(m *MSG) bool {
	r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
	return int32(r) > 0
}

func TranslateMessage(m *MSG)  { pTranslateMessage.Call(uintptr(unsafe.Pointer(m))) }
func DispatchMessage(m *MSG)   { pDispatchMessageW.Call(uintptr(unsafe.Pointer(m))) }
func PostQuitMessage(code int) { pPostQuitMessage.Call(uintptr(code)) }
func PostMessage(hwnd uintptr, msg uint32, w, l uintptr) {
	pPostMessageW.Call(hwnd, uintptr(msg), w, l)
}
func ShowWindow(hwnd uintptr, cmd int) { pShowWindow.Call(hwnd, uintptr(cmd)) }
func IsWindowVisible(hwnd uintptr) bool {
	r, _, _ := pIsWindowVisible.Call(hwnd)
	return r != 0
}
func SetWindowPos(hwnd, after uintptr, x, y, w, h int32, flags uint32) {
	pSetWindowPos.Call(hwnd, after, uintptr(x), uintptr(y), uintptr(w), uintptr(h), uintptr(flags))
}
func MoveWindow(hwnd uintptr, x, y, w, h int32) {
	pMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
}
func GetSystemMetrics(i int) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int32(r)
}
func SetTimer(hwnd, id uintptr, ms uint32) { pSetTimer.Call(hwnd, id, uintptr(ms), 0) }
func KillTimer(hwnd, id uintptr)           { pKillTimer.Call(hwnd, id) }
func InvalidateRect(hwnd uintptr)          { pInvalidateRect.Call(hwnd, 0, 0) }
func GetClientRect(hwnd uintptr) RECT {
	var r RECT
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}
func SetLayeredAlpha(hwnd uintptr, alpha byte) {
	pSetLayeredWindowAttributes.Call(hwnd, 0, uintptr(alpha), LWA_ALPHA)
}
func MessageBeep(t uint32) { pMessageBeep.Call(uintptr(t)) }

// MessageBox shows a modal dialog and returns the button pressed.
func MessageBox(hwnd uintptr, text, caption string, flags uint32) int {
	r, _, _ := pMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(UTF16(text))), uintptr(unsafe.Pointer(UTF16(caption))), uintptr(flags))
	return int(r)
}

// ForceForeground brings hwnd to the front even when another app has focus.
func ForceForeground(hwnd uintptr) {
	fg, _, _ := pGetForegroundWindow.Call()
	if fg == hwnd {
		return
	}
	me, _, _ := pGetCurrentThreadId.Call()
	other, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	if other != 0 && other != me {
		pAttachThreadInput.Call(other, me, 1)
		defer pAttachThreadInput.Call(other, me, 0)
	}
	pSetForegroundWindow.Call(hwnd)
	pBringWindowToTop.Call(hwnd)
}

// SetDPIAware makes the process DPI aware so text is sharp on scaled displays.
func SetDPIAware() {
	if pSetProcessDpiAwarenessContext.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	pSetProcessDPIAware.Call()
}

// SystemDPI returns the system DPI (96 = 100%).
func SystemDPI() int {
	if pGetDpiForSystem.Find() == nil {
		if r, _, _ := pGetDpiForSystem.Call(); r != 0 {
			return int(r)
		}
	}
	return 96
}

// Keyboard hook.
func SetKeyboardHook(proc uintptr) uintptr {
	r, _, _ := pSetWindowsHookExW.Call(WH_KEYBOARD_LL, proc, ModuleHandle(), 0)
	return r
}
func CallNextHook(code int, w, l uintptr) uintptr {
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), w, l)
	return r
}
func Unhook(h uintptr) { pUnhookWindowsHookEx.Call(h) }
func KeyDown(vk int) bool {
	r, _, _ := pGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// SingleInstance returns false if another instance holds the named mutex.
func SingleInstance(name string) bool {
	h, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(UTF16(name))))
	if h == 0 {
		return true
	}
	return err != syscall.Errno(ERROR_ALREADY_EXISTS)
}

// Taskbar hides or shows the Windows taskbar(s).
func Taskbar(show bool) {
	cmd := uintptr(SW_HIDE)
	if show {
		cmd = SW_SHOW
	}
	if h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(UTF16("Shell_TrayWnd"))), 0); h != 0 {
		pShowWindow.Call(h, cmd)
	}
	var prev uintptr
	for i := 0; i < 8; i++ {
		h, _, _ := pFindWindowExW.Call(0, prev, uintptr(unsafe.Pointer(UTF16("Shell_SecondaryTrayWnd"))), 0)
		if h == 0 {
			break
		}
		pShowWindow.Call(h, cmd)
		prev = h
	}
}

// ---- painting ----

// Painter double-buffers a WM_PAINT.
type Painter struct {
	hwnd, hdc, mem, bmp, oldBmp uintptr
	ps                          PAINTSTRUCT
	W, H                        int32
}

func BeginPaint(hwnd uintptr) *Painter {
	p := &Painter{hwnd: hwnd}
	p.hdc, _, _ = pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&p.ps)))
	rc := GetClientRect(hwnd)
	p.W, p.H = rc.Right-rc.Left, rc.Bottom-rc.Top
	p.mem, _, _ = pCreateCompatibleDC.Call(p.hdc)
	p.bmp, _, _ = pCreateCompatibleBitmap.Call(p.hdc, uintptr(p.W), uintptr(p.H))
	p.oldBmp, _, _ = pSelectObject.Call(p.mem, p.bmp)
	pSetBkMode.Call(p.mem, TRANSPARENT)
	return p
}

func (p *Painter) End() {
	pBitBlt.Call(p.hdc, 0, 0, uintptr(p.W), uintptr(p.H), p.mem, 0, 0, SRCCOPY)
	pSelectObject.Call(p.mem, p.oldBmp)
	pDeleteObject.Call(p.bmp)
	pDeleteDC.Call(p.mem)
	pEndPaint.Call(p.hwnd, uintptr(unsafe.Pointer(&p.ps)))
}

func (p *Painter) Fill(r RECT, color uintptr) {
	br, _, _ := pCreateSolidBrush.Call(color)
	pFillRect.Call(p.mem, uintptr(unsafe.Pointer(&r)), br)
	pDeleteObject.Call(br)
}

// Text draws s inside r. Returns the height used when measuring (DT_CALCRECT).
func (p *Painter) Text(s string, r RECT, font, color uintptr, flags uint32) {
	old, _, _ := pSelectObject.Call(p.mem, font)
	pSetTextColor.Call(p.mem, color)
	u, _ := windows.UTF16FromString(s)
	pDrawTextW.Call(p.mem, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), uintptr(flags|DT_NOPREFIX))
	pSelectObject.Call(p.mem, old)
}

// MeasureText returns the height s needs when wrapped to width w.
func MeasureText(s string, w int32, font uintptr) int32 {
	dc, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, dc)
	old, _, _ := pSelectObject.Call(dc, font)
	defer pSelectObject.Call(dc, old)
	r := RECT{0, 0, w, 0}
	u, _ := windows.UTF16FromString(s)
	pDrawTextW.Call(dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), DT_WORDBREAK|DT_CALCRECT|DT_NOPREFIX)
	return r.Bottom - r.Top
}

var fontCache = map[[2]int32]uintptr{}

// Font returns a cached Segoe UI font of the given pixel height and weight.
func Font(height, weight int32) uintptr {
	key := [2]int32{height, weight}
	if f, ok := fontCache[key]; ok {
		return f
	}
	f, _, _ := pCreateFontW.Call(uintptr(-height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1 /*DEFAULT_CHARSET*/, 0, 0,
		CLEARTYPE_QUALITY, 0, uintptr(unsafe.Pointer(UTF16("Segoe UI"))))
	fontCache[key] = f
	return f
}

// CreateChild makes a child control (EDIT, BUTTON...) inside parent, with id as its control id.
func CreateChild(parent uintptr, class, text string, style uint32, x, y, w, h int32, id uintptr) uintptr {
	r, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(UTF16(class))), uintptr(unsafe.Pointer(UTF16(text))),
		uintptr(style|WS_CHILD), uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, id, ModuleHandle(), 0)
	return r
}

// SetControlFont applies a font to a control.
func SetControlFont(hwnd, font uintptr) {
	pSendMessageW.Call(hwnd, WM_SETFONT, font, 1)
}

// SetFocus moves keyboard focus to a window.
func SetFocus(hwnd uintptr) { pSetFocus.Call(hwnd) }

// ShowChild shows or hides a control.
func ShowChild(hwnd uintptr, show bool) {
	cmd := uintptr(SW_HIDE)
	if show {
		cmd = 5 // SW_SHOW
	}
	pShowWindow.Call(hwnd, cmd)
}

// MoveControl repositions a control.
func MoveControl(hwnd uintptr, x, y, w, h int32) {
	pMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
}

// GetText reads a control's text.
func GetText(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

// SetText sets a control's text.
func SetText(hwnd uintptr, text string) {
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(UTF16(text))))
}

// DestroyWindow destroys a window.
func DestroyWindow(hwnd uintptr) { pDestroyWindow.Call(hwnd) }

// Enable enables or greys out a control.
func Enable(hwnd uintptr, enabled bool) {
	v := uintptr(0)
	if enabled {
		v = 1
	}
	pEnableWindow.Call(hwnd, v)
}

// LoWord returns the low 16 bits (e.g. control id from WM_COMMAND wParam).
func LoWord(v uintptr) uint16 { return uint16(v & 0xFFFF) }

// HiWord returns the high 16 bits (e.g. notification code from WM_COMMAND wParam).
func HiWord(v uintptr) uint16 { return uint16((v >> 16) & 0xFFFF) }
