package agent

import (
	"log"
	"os"
	"path/filepath"

	"nextgaming/internal/platform"
	"nextgaming/internal/protocol"
)

// assetPath is where an appearance image is cached on this PC.
func (a *Agent) assetPath(kind string) string {
	switch kind {
	case protocol.AssetDesktop:
		return filepath.Join(a.cfg.dataDir, "desktop.img")
	case protocol.AssetLockBg:
		return filepath.Join(a.cfg.dataDir, "lock_bg.img")
	}
	return ""
}

// handleAsset stores (or clears) an appearance image pushed by the server and
// applies it. The desktop wallpaper is set immediately via a user-session
// helper; the lock-screen background is picked up by the lock screen itself,
// which re-fetches /lock-bg whenever LockScreenVer changes.
func (a *Agent) handleAsset(as protocol.Asset) {
	p := a.assetPath(as.Kind)
	if p == "" {
		return
	}
	if len(as.Data) == 0 {
		os.Remove(p)
	} else if err := writeAtomic(p, as.Data); err != nil {
		log.Printf("asset %s: save failed: %v", as.Kind, err)
		return
	}

	a.mu.Lock()
	switch as.Kind {
	case protocol.AssetDesktop:
		a.st.DesktopVer = as.Version
	case protocol.AssetLockBg:
		a.st.LockVer = as.Version
	}
	a.saveStateLocked()
	clientPath := a.clientPath
	a.mu.Unlock()

	if as.Kind == protocol.AssetDesktop && clientPath != "" {
		// Empty path tells the helper to restore the default wallpaper.
		arg := p
		if len(as.Data) == 0 {
			arg = ""
		}
		if err := platform.RunOnceInSession(clientPath, []string{"-set-wallpaper", arg}); err != nil {
			log.Printf("set wallpaper: %v", err)
		}
	}
}
