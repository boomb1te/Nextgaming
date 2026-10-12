//go:build windows

package main

import (
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"nextgaming/internal/protocol"
)

// Remote-control input injection. Runs in the logged-in user's session (the
// capture helper), the only place a SendInput call reaches the real desktop.

var (
	inUser32          = windows.NewLazySystemDLL("user32.dll")
	inKernel32        = windows.NewLazySystemDLL("kernel32.dll")
	pSendInput        = inUser32.NewProc("SendInput")
	pSetWindowsHookEx = inUser32.NewProc("SetWindowsHookExW")
	pCallNextHookEx   = inUser32.NewProc("CallNextHookEx")
	pGetMessage       = inUser32.NewProc("GetMessageW")
	pGetModuleHandle  = inKernel32.NewProc("GetModuleHandleW")
)

const (
	whMouseLL       = 14
	whKeyboardLL    = 13
	llMouseInjected = 0x01
	llKeyInjected   = 0x10
)

// blockUntilNanos holds the unix-nano time until which the player's physical
// input is blocked. The server refreshes it (every control ping) a few seconds
// ahead; if refreshes stop (panel closed, network dropped), it lapses and the
// player gets their mouse/keyboard back automatically.
var blockUntilNanos atomic.Int64

func inputBlocked() bool {
	u := blockUntilNanos.Load()
	return u != 0 && time.Now().UnixNano() < u
}

const (
	inINPUTMouse   = 0
	inMoveFlag     = 0x0001
	inLeftDown     = 0x0002
	inLeftUp       = 0x0004
	inRightDown    = 0x0008
	inRightUp      = 0x0010
	inMiddleDown   = 0x0020
	inMiddleUp     = 0x0040
	inWheelFlag    = 0x0800
	inAbsoluteFlag = 0x8000
	inWheelDelta   = 120
)

// mouseInput mirrors the Win32 INPUT struct for a mouse event on amd64
// (type + padding + MOUSEINPUT, with the ULONG_PTR 8-byte aligned).
type mouseInput struct {
	typ       uint32
	_         uint32
	dx        int32
	dy        int32
	mouseData uint32
	dwFlags   uint32
	time      uint32
	_         uint32
	dwExtra   uintptr
}

// injectEvent injects one remote-control event. Stage 1 handles the mouse;
// keyboard ("key") is a no-op until stage 2.
func injectEvent(ev protocol.InputEvent) {
	switch ev.Kind {
	case "move":
		sendMouse(ev.X, ev.Y, inMoveFlag|inAbsoluteFlag, 0)
	case "down", "up":
		flags := uint32(inMoveFlag | inAbsoluteFlag)
		down := ev.Kind == "down"
		switch ev.Button {
		case "right":
			flags |= pick(down, inRightDown, inRightUp)
		case "middle":
			flags |= pick(down, inMiddleDown, inMiddleUp)
		default:
			flags |= pick(down, inLeftDown, inLeftUp)
		}
		sendMouse(ev.X, ev.Y, flags, 0)
	case "wheel":
		sendMouse(ev.X, ev.Y, inWheelFlag|inMoveFlag|inAbsoluteFlag, uint32(int32(ev.Dir*inWheelDelta)))
	case "block":
		// Block the player's physical input for a short window; control pings keep
		// refreshing it, so it auto-releases if control stops.
		blockUntilNanos.Store(time.Now().Add(6 * time.Second).UnixNano())
	case "unblock":
		blockUntilNanos.Store(0)
	}
}

// ---- blocking the player's physical input while an admin controls the PC ----
//
// Low-level hooks swallow real mouse/keyboard events but let our own injected
// events (which carry the INJECTED flag) through. The hooks stay installed for
// the helper's life and only swallow while inputBlocked() is true, so there is
// no install/uninstall churn and nothing to leak.

type llPoint struct{ x, y int32 }
type msllHook struct {
	pt        llPoint
	mouseData uint32
	flags     uint32
	time      uint32
	dwExtra   uintptr
}
type kbdllHook struct {
	vkCode   uint32
	scanCode uint32
	flags    uint32
	time     uint32
	dwExtra  uintptr
}

func mouseHookProc(nCode int32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	if nCode == 0 && inputBlocked() {
		s := (*msllHook)(lParam)
		if s.flags&llMouseInjected == 0 {
			return 1 // swallow the player's real mouse input
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, uintptr(lParam))
	return r
}

func keyHookProc(nCode int32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	if nCode == 0 && inputBlocked() {
		s := (*kbdllHook)(lParam)
		if s.flags&llKeyInjected == 0 {
			return 1 // swallow the player's real keyboard input
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, uintptr(lParam))
	return r
}

// startInputBlocker installs the hooks and pumps messages (required for
// low-level hooks). It runs for the helper's lifetime on its own OS thread.
func startInputBlocker() {
	runtime.LockOSThread()
	hmod, _, _ := pGetModuleHandle.Call(0)
	pSetWindowsHookEx.Call(whMouseLL, windows.NewCallback(mouseHookProc), hmod, 0)
	pSetWindowsHookEx.Call(whKeyboardLL, windows.NewCallback(keyHookProc), hmod, 0)
	var msg [48]byte // MSG; contents unused, GetMessage just keeps the loop pumping
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
	}
}

func pick(down bool, d, u uint32) uint32 {
	if down {
		return d
	}
	return u
}

// sendMouse injects one mouse action. Absolute coordinates are normalised to
// 0..65535 across the primary screen, so they match the captured image exactly
// regardless of the screen's pixel size.
func sendMouse(x, y float64, flags, data uint32) {
	clamp := func(v float64) int32 {
		if v < 0 {
			v = 0
		} else if v > 1 {
			v = 1
		}
		return int32(v * 65535)
	}
	in := mouseInput{
		typ:       inINPUTMouse,
		dx:        clamp(x),
		dy:        clamp(y),
		mouseData: data,
		dwFlags:   flags,
	}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}
