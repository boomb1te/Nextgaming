//go:build windows

package main

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"time"

	d3d "github.com/kirides/go-d3d"
	"github.com/kirides/go-d3d/d3d11"
	"github.com/kirides/go-d3d/outputduplication"
)

// screenCapturer produces JPEG frames of the primary monitor. It prefers the
// DXGI Desktop Duplication API, which reads the real composited GPU output, so
// it captures borderless / windowed-fullscreen games (which GDI shows as black)
// and follows resolution changes. It falls back to GDI when DXGI is
// unavailable (older hardware, Remote Desktop) or while it is being recreated.
//
// Note: true *exclusive* fullscreen cannot be captured by Desktop Duplication
// (or any user-mode method) — it returns the desktop. Games must run in
// borderless / windowed-fullscreen to appear on the monitoring grid.
type screenCapturer struct {
	device *d3d11.ID3D11Device
	ctx    *d3d11.ID3D11DeviceContext
	dup    *outputduplication.OutputDuplicator

	img       *image.RGBA // full-resolution frame buffer, reused
	bw, bh    int
	haveFrame bool

	dxgiCooldownUntil time.Time // after a hard DXGI failure, use GDI for a while
}

func newScreenCapturer() *screenCapturer { return &screenCapturer{} }

// jpeg returns one frame, DXGI if possible, otherwise GDI.
func (c *screenCapturer) jpeg(maxWidth, quality int) ([]byte, int, int, error) {
	if img, err := c.dxgiFrame(); err == nil && img != nil {
		small := downscaleRGBA(img, maxWidth)
		return encodeJPEG(small, quality)
	}
	return captureJPEGGDI(maxWidth, quality)
}

// dxgiFrame grabs a full-resolution frame via Desktop Duplication.
func (c *screenCapturer) dxgiFrame() (*image.RGBA, error) {
	if time.Now().Before(c.dxgiCooldownUntil) {
		return nil, errors.New("dxgi on cooldown")
	}
	if c.device == nil {
		dev, ctx, err := d3d11.NewD3D11Device()
		if err != nil {
			c.cooldown()
			return nil, err
		}
		c.device, c.ctx = dev, ctx
	}
	if c.dup == nil {
		dup, err := outputduplication.NewIDXGIOutputDuplication(c.device, c.ctx, 0)
		if err != nil {
			c.releaseDevice()
			c.cooldown()
			return nil, err
		}
		c.dup = dup
		c.haveFrame = false
	}

	b, err := c.dup.GetBounds()
	if err != nil {
		c.releaseDup()
		return nil, err
	}
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		c.releaseDup()
		return nil, errors.New("empty bounds")
	}
	if c.img == nil || w != c.bw || h != c.bh {
		c.img = image.NewRGBA(image.Rect(0, 0, w, h))
		c.bw, c.bh = w, h
		c.haveFrame = false
	}

	err = c.dup.GetImage(c.img, 250)
	if err != nil {
		if errors.Is(err, outputduplication.ErrNoImageYet) {
			// Screen did not change within the timeout: the buffer still holds
			// the last frame, so reuse it.
			if c.haveFrame {
				return c.img, nil
			}
			return nil, err
		}
		// Access lost (resolution change, fullscreen takeover) or device lost:
		// tear down and rebuild next time; fall back to GDI for this frame.
		c.releaseDup()
		var hr d3d.HRESULT
		if errors.As(err, &hr) && (hr == d3d.DXGI_ERROR_DEVICE_REMOVED || hr == d3d.DXGI_ERROR_DEVICE_RESET || hr == d3d.DXGI_ERROR_DEVICE_HUNG) {
			c.releaseDevice()
		}
		return nil, err
	}
	c.haveFrame = true
	return c.img, nil
}

func (c *screenCapturer) cooldown() { c.dxgiCooldownUntil = time.Now().Add(30 * time.Second) }

func (c *screenCapturer) releaseDup() {
	if c.dup != nil {
		c.dup.Release()
		c.dup = nil
	}
	c.haveFrame = false
}

func (c *screenCapturer) releaseDevice() {
	c.releaseDup()
	if c.ctx != nil {
		c.ctx.Release()
		c.ctx = nil
	}
	if c.device != nil {
		c.device.Release()
		c.device = nil
	}
}

func (c *screenCapturer) close() { c.releaseDevice() }

// downscaleRGBA shrinks src to at most maxW wide (keeping aspect, never
// upscaling) with nearest-neighbour sampling — fast and good enough for a
// monitoring thumbnail.
func downscaleRGBA(src *image.RGBA, maxW int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	dw := maxW
	if dw > sw {
		dw = sw
	}
	dh := sh * dw / sw
	if dh < 1 {
		dh = 1
	}
	if dw == sw && dh == sh {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := y * sh / dh
		for x := 0; x < dw; x++ {
			sx := x * sw / dw
			si := src.PixOffset(src.Rect.Min.X+sx, src.Rect.Min.Y+sy)
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

func encodeJPEG(img *image.RGBA, quality int) ([]byte, int, int, error) {
	if quality < 1 || quality > 100 {
		quality = 40
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), img.Rect.Dx(), img.Rect.Dy(), nil
}
