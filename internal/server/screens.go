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

	// One-off HD snapshot: native resolution (the capturer clamps to the real
	// screen size, never upscales) at near-full JPEG quality.
	snapWidth   = 4000
	snapQuality = 90
	snapStale   = 2 * time.Minute // a snapshot older than this is not served

	// Remote control: a ~0.5s refresh is plenty (control is for fixing things,
	// not gaming), at a readable mid resolution/quality.
	controlEvery   = 500 * time.Millisecond
	controlWidth   = 1280
	controlQuality = 55
	controlTTL     = 8 * time.Second // control stops this long after the last ping
)

type screenFrame struct {
	data []byte
	w, h int
	at   time.Time
}

// screens holds the latest frame per PC and who/what is being watched. snaps
// holds the latest one-off HD snapshot per PC, kept separate so the live grid
// never overwrites it.
type screens struct {
	mu         sync.Mutex
	frames     map[int64]screenFrame
	snaps      map[int64]screenFrame
	snapReq    map[int64]time.Time // when an HD snapshot was last requested per PC
	watchUntil time.Time
	focus      int64
	focusUntil time.Time
	control    int64     // the PC currently under remote control (0 = none)
	ctrlUntil  time.Time // control stops after this (refreshed by the panel's pings)
}

func newScreens() *screens {
	return &screens{frames: map[int64]screenFrame{}, snaps: map[int64]screenFrame{}, snapReq: map[int64]time.Time{}}
}

func (sc *screens) store(pcID int64, data []byte, w, h int, at time.Time) {
	if len(data) == 0 || len(data) > maxFrameBytes {
		return
	}
	sc.mu.Lock()
	sc.frames[pcID] = screenFrame{data: data, w: w, h: h, at: at}
	sc.mu.Unlock()
}

// storeSnap keeps the latest HD snapshot for a PC (separate from live frames).
func (sc *screens) storeSnap(pcID int64, data []byte, w, h int, at time.Time) {
	if len(data) == 0 || len(data) > maxFrameBytes {
		return
	}
	sc.mu.Lock()
	sc.snaps[pcID] = screenFrame{data: data, w: w, h: h, at: at}
	sc.mu.Unlock()
}

func (sc *screens) get(pcID int64) (screenFrame, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	f, ok := sc.frames[pcID]
	return f, ok
}

// markSnapRequest records that a fresh HD snapshot was just asked for, and
// clears any previous one so a stale image isn't served while we wait.
func (sc *screens) markSnapRequest(pcID int64, now time.Time) {
	sc.mu.Lock()
	delete(sc.snaps, pcID)
	sc.snapReq[pcID] = now
	sc.mu.Unlock()
}

// readySnap returns the HD snapshot only once one captured *after* the latest
// request has arrived and is still fresh. Freshness is judged entirely on the
// server clock, so the admin's browser clock is irrelevant.
func (sc *screens) readySnap(pcID int64, now time.Time) (screenFrame, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	f, ok := sc.snaps[pcID]
	if !ok || len(f.data) == 0 || now.Sub(f.at) > snapStale {
		return screenFrame{}, false
	}
	if req, has := sc.snapReq[pcID]; has && f.at.Before(req) {
		return screenFrame{}, false
	}
	return f, true
}

func (sc *screens) drop(pcID int64) {
	sc.mu.Lock()
	delete(sc.frames, pcID)
	delete(sc.snaps, pcID)
	delete(sc.snapReq, pcID)
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

// setControl marks a PC as under remote control and refreshes the timeout; the
// panel calls it on start and as a periodic ping. pcID 0 (or active=false) ends it.
func (sc *screens) setControl(pcID int64, now time.Time) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.control = pcID
	if pcID != 0 {
		sc.ctrlUntil = now.Add(controlTTL)
	} else {
		sc.ctrlUntil = time.Time{}
	}
}

// controlledPC returns the PC under remote control right now, or 0.
func (sc *screens) controlledPC(now time.Time) int64 {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.control != 0 && now.Before(sc.ctrlUntil) {
		return sc.control
	}
	return 0
}

// screenLoop broadcasts capture requests while someone is watching.
func (s *Server) screenLoop(ctx context.Context) {
	thumb := time.NewTicker(screenThumbEvery)
	defer thumb.Stop()
	focus := time.NewTicker(screenFocusEvery)
	defer focus.Stop()
	control := time.NewTicker(controlEvery)
	defer control.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-control.C:
			now := s.now()
			cp := s.screens.controlledPC(now)
			if cp != 0 && s.hub.Online(cp) {
				s.hub.Send(cp, protocol.TypeCapture, protocol.CaptureRequest{Width: controlWidth, Quality: controlQuality})
			}
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

// RequestSnapshot asks a PC for a single high-resolution frame (shown once,
// not streamed). It works whether or not the Screens grid is being watched.
func (s *Server) RequestSnapshot(pcID int64) error {
	if !s.hub.Online(pcID) {
		return errConflict("%s is offline.", s.pcName(pcID))
	}
	s.screens.markSnapRequest(pcID, time.Now())
	return s.hub.Send(pcID, protocol.TypeCapture, protocol.CaptureRequest{Width: snapWidth, Quality: snapQuality, Snap: true})
}

// SetControl starts/refreshes or stops remote control of a PC. The panel calls
// it on entering control mode and as a periodic ping (active=true), and on
// leaving (active=false). Control is refused while the PC is locked or offline.
func (s *Server) SetControl(pcID int64, active bool) error {
	if !active {
		if s.screens.controlledPC(s.now()) == pcID {
			s.screens.setControl(0, s.now())
			s.changed()
		}
		return nil
	}
	if !s.hub.Online(pcID) {
		return errConflict("%s is offline.", s.pcName(pcID))
	}
	if s.hub.Runtime(pcID).Locked {
		return errConflict("%s is locked — unlock it to take control.", s.pcName(pcID))
	}
	first := s.screens.controlledPC(s.now()) != pcID
	s.screens.setControl(pcID, s.now())
	if first {
		s.changed() // show the "being controlled" marker
	}
	return nil
}

// SendControlInput relays mouse/keyboard events to the PC currently controlled.
func (s *Server) SendControlInput(pcID int64, events []protocol.InputEvent) error {
	if len(events) == 0 {
		return nil
	}
	if s.screens.controlledPC(s.now()) != pcID {
		return errConflict("not controlling that PC.")
	}
	s.screens.setControl(pcID, s.now()) // input also keeps control alive
	return s.hub.Send(pcID, protocol.TypeInput, protocol.InputBatch{Events: events})
}

// ControlledPC reports which PC is under remote control (0 = none), for the snapshot.
func (s *Server) ControlledPC() int64 { return s.screens.controlledPC(s.now()) }
