//go:build windows

package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	w "nextgaming/internal/winapi"
)

// applyWallpaper sets this user's desktop wallpaper to the image at path (empty
// path restores the default). It uses the user's current fit style, which is
// "Fill" by default on Windows 10/11, so a photo covers the screen.
func applyWallpaper(path string) {
	w.SetWallpaper(path)
}

// ensureLockBg loads the lock-screen background for the given version (0 = none)
// in the background and repaints when it is ready. bgVer is updated right away
// so a failed or in-flight fetch is not retried every poll.
func (a *app) ensureLockBg(ver int) {
	if ver == a.bgVer {
		return
	}
	a.bgVer = ver
	if ver == 0 {
		a.mu.Lock()
		a.bgPix, a.bgW, a.bgH = nil, 0, 0
		a.mu.Unlock()
		if a.lockWnd != 0 {
			w.InvalidateRect(a.lockWnd)
		}
		return
	}
	go func() {
		pix, pw, ph, err := a.fetchLockBg()
		if err != nil {
			return
		}
		a.mu.Lock()
		a.bgPix, a.bgW, a.bgH = pix, pw, ph
		a.mu.Unlock()
		if a.lockWnd != 0 {
			w.InvalidateRect(a.lockWnd)
		}
	}()
}

// fetchLockBg downloads the current lock-screen image from the agent and returns
// it as a dimmed, top-down BGRA buffer ready for DrawImageBGRA.
func (a *app) fetchLockBg() ([]byte, int32, int32, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/lock-bg", a.port), nil)
	req.Header.Set("X-NextGaming-Client", "1")
	res, err := a.client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, 0, 0, fmt.Errorf("status %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, 0, 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	return toDimBGRA(img), int32(img.Bounds().Dx()), int32(img.Bounds().Dy()), nil
}

// toDimBGRA converts an image to top-down BGRA and darkens it to ~55% so the
// light lock-screen text stays readable over any photo.
func toDimBGRA(img image.Image) []byte {
	b := img.Bounds()
	wd, ht := b.Dx(), b.Dy()
	pix := make([]byte, wd*ht*4)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA() // 16-bit per channel
			pix[i] = byte((uint32(bl>>8) * 55) / 100)   // B
			pix[i+1] = byte((uint32(g>>8) * 55) / 100)  // G
			pix[i+2] = byte((uint32(r>>8) * 55) / 100)  // R
			pix[i+3] = 255
			i += 4
		}
	}
	return pix
}
