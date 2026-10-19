package server

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for validation
	_ "image/png"
	"os"
	"path/filepath"

	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
)

// maxAssetBytes caps an appearance image. Wallpapers are a few hundred KB to a
// couple of MB; the protocol line limit is 8 MB, so stay well under it.
const maxAssetBytes = 6 << 20 // 6 MB

func (s *Server) assetDir() string { return filepath.Join(s.cfg.DataDir(), "assets") }

// assetFile is the on-disk filename for an appearance image kind ("" if unknown).
func assetFile(kind string) string {
	switch kind {
	case protocol.AssetDesktop:
		return "desktop.img"
	case protocol.AssetLockBg:
		return "lock_bg.img"
	}
	return ""
}

func (s *Server) assetPath(kind string) string {
	f := assetFile(kind)
	if f == "" {
		return ""
	}
	return filepath.Join(s.assetDir(), f)
}

// loadAsset returns the stored image bytes for a kind, or nil if none is set.
func (s *Server) loadAsset(kind string) []byte {
	p := s.assetPath(kind)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

func assetVersion(st core.Settings, kind string) int {
	if kind == protocol.AssetDesktop {
		return st.DesktopWallpaperVer
	}
	return st.LockScreenVer
}

// SaveAppearance stores (data non-empty) or clears (data nil/empty) one
// appearance image, bumps its version so agents re-fetch, and pushes it to
// every online PC.
func (s *Server) SaveAppearance(adminID int64, kind string, data []byte) error {
	if assetFile(kind) == "" {
		return errBad("unknown image kind")
	}
	if len(data) > maxAssetBytes {
		return errBad("image is too large (max %d MB)", maxAssetBytes>>20)
	}
	if len(data) > 0 {
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return errBad("that file is not a PNG or JPEG image")
		}
	}
	if err := os.MkdirAll(s.assetDir(), 0o755); err != nil {
		return err
	}
	p := s.assetPath(kind)
	if len(data) == 0 {
		os.Remove(p)
	} else if err := writeFileAtomic(p, data); err != nil {
		return err
	}
	st := s.Settings()
	switch kind {
	case protocol.AssetDesktop:
		st.DesktopWallpaperVer++
	case protocol.AssetLockBg:
		st.LockScreenVer++
	}
	if err := store.SaveSettings(s.st.DB, st); err != nil {
		return err
	}
	s.settingsMu.Lock()
	s.settings = st
	s.settingsMu.Unlock()
	store.Audit(s.st.DB, adminID, "appearance_"+kind, 0, fmt.Sprintf("v%d, %d bytes", assetVersion(st, kind), len(data)))
	go s.broadcastAsset(kind)
	go s.pushAll() // refresh DesiredState (versions + name position) everywhere
	s.changed()
	return nil
}

// broadcastAsset sends the current image for a kind to every online PC.
func (s *Server) broadcastAsset(kind string) {
	ver := assetVersion(s.Settings(), kind)
	data := s.loadAsset(kind)
	for _, id := range s.hub.OnlineIDs() {
		s.hub.Send(id, protocol.TypeAsset, protocol.Asset{Kind: kind, Version: ver, Data: data})
	}
}

// sendAssets gives a freshly connected PC any image whose version differs from
// what it reported already having.
func (s *Server) sendAssets(pcID int64, haveDesktop, haveLock int) {
	st := s.Settings()
	if st.DesktopWallpaperVer != haveDesktop {
		s.hub.Send(pcID, protocol.TypeAsset, protocol.Asset{Kind: protocol.AssetDesktop, Version: st.DesktopWallpaperVer, Data: s.loadAsset(protocol.AssetDesktop)})
	}
	if st.LockScreenVer != haveLock {
		s.hub.Send(pcID, protocol.TypeAsset, protocol.Asset{Kind: protocol.AssetLockBg, Version: st.LockScreenVer, Data: s.loadAsset(protocol.AssetLockBg)})
	}
}
