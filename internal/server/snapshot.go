package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"nextgaming/internal/bios"
	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
)

// SessionView is a session as the admin panel shows it.
type SessionView struct {
	ID                int64              `json:"id"`
	Mode              core.SessionMode   `json:"mode"`
	Status            core.SessionStatus `json:"status"`
	StartedAt         time.Time          `json:"startedAt"`
	RemainingSec      int64              `json:"remainingSec"`
	ElapsedSec        int64              `json:"elapsedSec"`
	Paid              core.Money         `json:"paid"`
	Due               core.Money         `json:"due"` // open: what it would cost if ended now
	RatePerHour       core.Money         `json:"ratePerHour"`
	TariffName        string             `json:"tariffName"`
	CheckoutRequested bool               `json:"checkoutRequested"`
	Note              string             `json:"note,omitempty"`
}

// WoLView is Wake-on-LAN state plus guidance.
type WoLView struct {
	core.WoLInfo
	Report *protocol.WoLReport `json:"report,omitempty"`
	Guide  *bios.Guide         `json:"guide,omitempty"`
}

// PCView is one tile in the admin panel grid.
type PCView struct {
	core.PC
	WoL      WoLView      `json:"wol"`
	ZoneName string       `json:"zoneName"`
	Runtime  Runtime      `json:"runtime"`
	Session  *SessionView `json:"session"`
	// State summarises the tile colour: offline | free | busy | paused | checkout | staff
	State string `json:"state"`
	// NextBooking marks an upcoming reservation on a free PC ("Booked 19:00 — Merdan").
	NextBooking *BookingMark `json:"nextBooking,omitempty"`
}

// Snapshot is everything the admin panel needs, pushed on every change.
type Snapshot struct {
	ServerTime time.Time      `json:"serverTime"`
	Version    string         `json:"version"`
	Settings   core.Settings  `json:"settings"`
	Zones      []core.Zone    `json:"zones"`
	Packages   []core.Package `json:"packages"`
	PCs        []PCView       `json:"pcs"`
	Shift      *ShiftView     `json:"shift"`
	Alerts     []Alert        `json:"alerts"`
	// PendingCalls are answered incoming calls awaiting the operator's action
	// (book for this caller / dismiss). See internal/server/calls.go.
	PendingCalls []PendingCallView `json:"pendingCalls"`
}

// BuildSnapshot assembles the current state.
func (s *Server) BuildSnapshot() (*Snapshot, error) {
	now := s.now()
	settings := s.Settings()
	zones, err := store.ListZones(s.st.DB)
	if err != nil {
		return nil, err
	}
	pkgs, err := store.ListPackages(s.st.DB)
	if err != nil {
		return nil, err
	}
	pcs, err := store.ListPCs(s.st.DB)
	if err != nil {
		return nil, err
	}
	open, err := store.OpenSessions(s.st.DB)
	if err != nil {
		return nil, err
	}
	shift, err := s.CurrentShift()
	if err != nil {
		return nil, err
	}
	zoneName := map[int64]string{}
	for _, z := range zones {
		zoneName[z.ID] = z.Name
	}
	byPC := map[int64]*core.Session{}
	for i := range open {
		byPC[open[i].PCID] = &open[i]
	}
	marks := s.nextBookingMarks(now)
	snap := &Snapshot{ServerTime: now, Version: Version, Settings: settings, Zones: zones, Packages: pkgs,
		Shift: shift, Alerts: s.recentAlerts(30), PCs: make([]PCView, 0, len(pcs))}
	for _, pc := range pcs {
		v := PCView{PC: pc, ZoneName: zoneName[pc.ZoneID], Runtime: s.hub.Runtime(pc.ID)}
		v.WoL.WoLInfo = pc.WoL
		if pc.WoL.Report != "" {
			var rep protocol.WoLReport
			if json.Unmarshal([]byte(pc.WoL.Report), &rep) == nil {
				v.WoL.Report = &rep
			}
		}
		if pc.WoL.Status != core.WoLOK && pc.WoL.Status != core.WoLWiFi {
			g := bios.For(pc.BoardMaker, pc.BoardProduct)
			v.WoL.Guide = &g
		}
		if sess := byPC[pc.ID]; sess != nil {
			sv := &SessionView{ID: sess.ID, Mode: sess.Mode, Status: sess.Status, StartedAt: sess.StartedAt,
				ElapsedSec: sess.ActiveSeconds(now), Paid: sess.Paid, RatePerHour: sess.RatePerHour,
				TariffName: sess.TariffName, CheckoutRequested: sess.CheckoutRequested, Note: sess.Note}
			switch sess.Mode {
			case core.ModePrepaid:
				sv.RemainingSec = max(sess.RemainingSeconds(now), 0)
			case core.ModeOpen:
				sv.Due = core.OpenCharge(sess.RatePerHour, sv.ElapsedSec, settings.GraceSeconds, settings.MinChargeMinutes)
			}
			v.Session = sv
		}
		v.State = tileState(v)
		if v.Session == nil {
			v.NextBooking = marks[pc.ID] // only show a reservation marker on a free PC
		}
		snap.PCs = append(snap.PCs, v)
	}
	snap.PendingCalls = s.pendingCallViews()
	return snap, nil
}

func tileState(v PCView) string {
	switch {
	case v.Session != nil && v.Session.CheckoutRequested:
		return "checkout"
	case v.Session != nil && v.Session.Status == core.StatusPaused:
		return "paused"
	case v.Session != nil && v.Session.Mode == core.ModeFree:
		return "staff"
	case v.Session != nil:
		return "busy"
	case !v.Runtime.Online:
		return "offline"
	default:
		return "free"
	}
}

// ---- server-sent events to admin browsers ----

// Bus fans snapshots out to every connected admin panel.
type Bus struct {
	srv  *Server
	kick chan struct{}

	mu   sync.Mutex
	subs map[chan []byte]struct{}
	last []byte
}

func newBus(s *Server) *Bus {
	return &Bus{srv: s, kick: make(chan struct{}, 1), subs: map[chan []byte]struct{}{}}
}

// poke requests a refresh; many pokes in a burst produce one snapshot.
func (b *Bus) poke() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

func (b *Bus) run(ctx context.Context) {
	periodic := time.NewTicker(5 * time.Second)
	defer periodic.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.kick:
			time.Sleep(150 * time.Millisecond) // coalesce bursts
		case <-periodic.C:
		}
		b.mu.Lock()
		n := len(b.subs)
		b.mu.Unlock()
		if n == 0 {
			continue
		}
		snap, err := b.srv.BuildSnapshot()
		if err != nil {
			continue
		}
		data, _ := json.Marshal(snap)
		b.mu.Lock()
		b.last = data
		for ch := range b.subs {
			select {
			case ch <- data:
			default: // slow browser: it will get the next one
			}
		}
		b.mu.Unlock()
	}
}

func (b *Bus) subscribe() chan []byte {
	ch := make(chan []byte, 4)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Bus) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}
