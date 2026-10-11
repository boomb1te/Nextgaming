//go:build windows

package main

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Screen capture for the monitoring grid. The lock-screen client runs in the
// interactive desktop session, so it (unlike the agent service in session 0)
// can actually see the user's screen. We grab the primary monitor with GDI,
// downscale it with StretchBlt (HALFTONE), and JPEG-encode it in pure Go.
//
// Note: GDI cannot capture a game running in exclusive full-screen (it returns
// black). Borderless/windowed games capture fine.

var (
	capUser32 = windows.NewLazySystemDLL("user32.dll")
	capGdi32  = windows.NewLazySystemDLL("gdi32.dll")

	pCapGetDC             = capUser32.NewProc("GetDC")
	pCapReleaseDC         = capUser32.NewProc("ReleaseDC")
	pCapGetSystemMetrics  = capUser32.NewProc("GetSystemMetrics")
	pCapCreateDC          = capGdi32.NewProc("CreateCompatibleDC")
	pCapDeleteDC          = capGdi32.NewProc("DeleteDC")
	pCapCreateDIBSection  = capGdi32.NewProc("CreateDIBSection")
	pCapSelectObject      = capGdi32.NewProc("SelectObject")
	pCapDeleteObject      = capGdi32.NewProc("DeleteObject")
	pCapSetStretchBltMode = capGdi32.NewProc("SetStretchBltMode")
	pCapStretchBlt        = capGdi32.NewProc("StretchBlt")
)

const (
	capSMCXSCREEN   = 0
	capSMCYSCREEN   = 1
	capCOLORONCOLOR = 3          // delete scanlines when shrinking; never dims the image
	capSRCCOPY      = 0x00CC0020 // raster op
	capCAPTUREBLT   = 0x40000000 // also include layered windows (otherwise they come back black)
	capBIRGB        = 0
	capDIBRGB       = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// captureJPEGGDI grabs the primary monitor with GDI, scales it to at most
// maxWidth pixels wide (keeping aspect, never upscaling) and returns a JPEG.
// GDI is the fallback: it cannot see GPU-rendered game frames (returns the
// desktop or black), so the DXGI capturer is tried first.
func captureJPEGGDI(maxWidth, quality int) ([]byte, int, int, error) {
	if maxWidth < 16 {
		maxWidth = 16
	}
	if quality < 1 || quality > 100 {
		quality = 40
	}
	sw, _, _ := pCapGetSystemMetrics.Call(capSMCXSCREEN)
	sh, _, _ := pCapGetSystemMetrics.Call(capSMCYSCREEN)
	srcW, srcH := int(int32(sw)), int(int32(sh))
	if srcW <= 0 || srcH <= 0 {
		return nil, 0, 0, errors.New("no screen")
	}
	dstW := maxWidth
	if dstW > srcW {
		dstW = srcW
	}
	dstH := srcH * dstW / srcW
	if dstH < 1 {
		dstH = 1
	}

	screenDC, _, _ := pCapGetDC.Call(0)
	if screenDC == 0 {
		return nil, 0, 0, errors.New("GetDC failed")
	}
	defer pCapReleaseDC.Call(0, screenDC)
	memDC, _, _ := pCapCreateDC.Call(screenDC)
	if memDC == 0 {
		return nil, 0, 0, errors.New("CreateCompatibleDC failed")
	}
	defer pCapDeleteDC.Call(memDC)

	var bi bitmapInfo
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(dstW)
	bi.Header.Height = int32(-dstH) // negative = top-down rows
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = capBIRGB

	var bits unsafe.Pointer
	hbmp, _, _ := pCapCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bi)), capDIBRGB,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 || bits == nil {
		return nil, 0, 0, errors.New("CreateDIBSection failed")
	}
	defer pCapDeleteObject.Call(hbmp)
	old, _, _ := pCapSelectObject.Call(memDC, hbmp)
	defer pCapSelectObject.Call(memDC, old)

	pCapSetStretchBltMode.Call(memDC, capCOLORONCOLOR)
	r, _, _ := pCapStretchBlt.Call(memDC, 0, 0, uintptr(dstW), uintptr(dstH),
		screenDC, 0, 0, uintptr(srcW), uintptr(srcH), capSRCCOPY|capCAPTUREBLT)
	if r == 0 {
		return nil, 0, 0, errors.New("StretchBlt failed")
	}

	// The DIB is 32-bit BGRA, top-down, 4-byte aligned per row (DWORD-aligned
	// already at 32bpp), so there is no row padding to skip.
	n := dstW * dstH * 4
	src := unsafe.Slice((*byte)(bits), n)
	img := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for i := 0; i < n; i += 4 {
		img.Pix[i+0] = src[i+2] // R <- B
		img.Pix[i+1] = src[i+1] // G
		img.Pix[i+2] = src[i+0] // B <- R
		img.Pix[i+3] = 255
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), dstW, dstH, nil
}
