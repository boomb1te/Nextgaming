//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"nextgaming/internal/protocol"
)

// Remote-control input injection. Runs in the logged-in user's session (the
// capture helper), the only place a SendInput call reaches the real desktop.

var (
	inUser32   = windows.NewLazySystemDLL("user32.dll")
	pSendInput = inUser32.NewProc("SendInput")
)

const (
	inINPUTMouse    = 0
	inMoveFlag      = 0x0001
	inLeftDown      = 0x0002
	inLeftUp        = 0x0004
	inRightDown     = 0x0008
	inRightUp       = 0x0010
	inMiddleDown    = 0x0020
	inMiddleUp      = 0x0040
	inWheelFlag     = 0x0800
	inAbsoluteFlag  = 0x8000
	inWheelDelta    = 120
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
