package server

import (
	"context"
	"sync"
	"time"

	"nextgaming/internal/protocol"
)

// Screen monitoring. While an admin has the Screens tab open, the server
// broadcasts a capture tick to every online PC every screenThumbEvery, so all
// PCs grab a frame at the same moment. The PC the admin has opened full-size
// (the focus) gets a larger, faster frame on its own cadence. Nothing is
// captured when nobody is watching, so idle clubs pay no cost.
const (
	screenThumbEvery = 2500 * time.Millisecond
	screenFocusEvery = 1000 * time.Millisecond
	screenWatchTTL   = 8 * time.Second  // monitoring stops this long after the last sign of a viewer
	screenStale      = 30 * time.Second // a frame older than this is not served

	thumbWidth    = 384
	thumbQuality  = 40
	focusWidth    = 1366
	focusQuality  = 70
	maxFrameBytes = 6 << 20
)

type screenFrame struct {
	data []byte
	w, h int
	at   time.Time
}

// screens holds the latest frame per PC and who/what is being watched.
type screens struct {
	mu         sync.Mutex
	frames     map[int64]screenFrame
	watchUntil time.Time
	focus      int64
	focusUntil time.Time
}

func newScreens() *screens { return &screens{frames: map[int64]screenFrame{}} }

func (sc *screens) store(pcID int64, data []byte, w, h int, at time.Time) {
	if len(data) == 0 || len(data) > maxFrameBytes {
		return
	}
	sc.mu.Lock()
	sc.frames[pcID] = screenFrame{data: data, w: w, h: h, at: at}
	sc.mu.Unlock()
}

func (sc *screens) get(pcID int64) (screenFrame, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	f, ok := sc.frames[pcID]
	return f, ok
}

func (sc *screens) drop(pcID int64) {
	sc.mu.Lock()
	delete(sc.frames, pcID)
	sc.mu.Unlock()
}

// setWatch is the explicit heartbeat from the Screens tab: whether it is open
// and which PC (if any) is expanded full-size.
func (sc *screens) setWatch(watching bool, focus int64, now time.Time) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if watching {
		sc.watchUntil = now.Add(screenWatchTTL)
	} else {
		sc.watchUntil = time.Time{}
		sc.focus, sc.focusUntil = 0, time.Time{}
		return
	}
	sc.focus = focus
	if focus != 0 {
		sc.focusUntil = now.Add(screenWatchTTL)
	} else {
		sc.focusUntil = time.Time{}
	}
}

// keepAlive extends watching whenever a frame image is actually pulled, so the
// grid keeps refreshing even if a heartbeat is missed.
func (sc *screens) keepAlive(now time.Time) {
	sc.mu.Lock()
	sc.watchUntil = now.Add(screenWatchTTL)
	sc.mu.Unlock()
}

func (sc *screens) watching(now time.Time) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return now.Before(sc.watchUntil)
}

func (sc *screens) focusPC(now time.Time) int64 {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if now.Before(sc.focusUntil) {
		return sc.focus
	}
	return 0
}

// screenLoop broadcasts capture requests while someone is watching.
func (s *Server) screenLoop(ctx context.Context) {
	thumb := time.NewTicker(screenThumbEvery)
	defer thumb.Stop()
	focus := time.NewTicker(screenFocusEvery)
	defer focus.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-thumb.C:
			now := s.now()
			if !s.screens.watching(now) {
				continue
			}
			foc := s.screens.focusPC(now)
			for _, id := range s.hub.OnlineIDs() {
				if id == foc {
					continue // the focused PC gets a hi-res frame on its own cadence
				}
				s.hub.Send(id, protocol.TypeCapture, protocol.CaptureRequest{Width: thumbWidth, Quality: thumbQuality})
			}
		case <-focus.C:
			now := s.now()
			if !s.screens.watching(now) {
				continue
			}
			foc := s.screens.focusPC(now)
			if foc == 0 || !s.hub.Online(foc) {
				continue
			}
			s.hub.Send(foc, protocol.TypeCapture, protocol.CaptureRequest{Width: focusWidth, Quality: focusQuality})
		}
	}
}
