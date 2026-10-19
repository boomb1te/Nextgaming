package winapi

import "unsafe"

// Image drawing (for the lock-screen background) and the desktop wallpaper API.

const (
	biRGB            = 0
	dibRGBColors     = 0
	halftone         = 4
	srcCopyROP       = 0x00CC0020
	spiSetDeskWallp  = 0x0014
	spifUpdateINI    = 0x01
	spifSendChange   = 0x02
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

// DrawImageBGRA blits a 32-bit BGRA, top-down pixel buffer into the painter's
// back buffer, stretched to fill dst. pix must be imgW*imgH*4 bytes.
func (p *Painter) DrawImageBGRA(pix []byte, imgW, imgH int32, dst RECT) {
	if len(pix) < int(imgW)*int(imgH)*4 || imgW <= 0 || imgH <= 0 {
		return
	}
	bi := bitmapInfoHeader{
		Size:        40,
		Width:       imgW,
		Height:      -imgH, // negative = top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
	}
	pSetStretchBltMode.Call(p.mem, halftone)
	dw := dst.Right - dst.Left
	dh := dst.Bottom - dst.Top
	pStretchDIBits.Call(p.mem,
		uintptr(dst.Left), uintptr(dst.Top), uintptr(dw), uintptr(dh),
		0, 0, uintptr(imgW), uintptr(imgH),
		uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bi)),
		dibRGBColors, srcCopyROP)
}

// SetWallpaper sets the current user's desktop wallpaper to the image at path
// (JPEG/PNG/BMP are accepted on Windows 7+). An empty path removes it.
func SetWallpaper(path string) error {
	var p uintptr
	if path != "" {
		p = uintptr(unsafe.Pointer(UTF16(path)))
	}
	r, _, err := pSystemParametersInfoW.Call(spiSetDeskWallp, 0, p, spifUpdateINI|spifSendChange)
	if r == 0 {
		return err
	}
	return nil
}
