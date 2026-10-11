// Package server is the NextGaming server: it owns the database, talks to the
// agent on every gaming PC, and serves the admin panel.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
)

// Version of the server.
var Version = "1.0.0"

// Server is the running NextGaming server.
type Server struct {
	cfg       Config
	st        *store.Store
	hub       *Hub
	logins    *Sessions
	bus       *Bus
	screens   *screens
	transfers *transfers
	calls     *calls

	// mu serialises every state-changing operation so two admins clicking at
	// once can never double-start a PC or double-charge a session.
	mu sync.Mutex

	settingsMu sync.RWMutex
	settings   core.Settings

	alertsMu sync.Mutex
	alerts   []Alert
	alertSeq int64

	wolMu    sync.Mutex
	wolTests map[int64]bool // PCs with a wake test in progress

	wokenBookings map[int64]bool // bookings whose PCs we already pre-woke

	now func() time.Time // replaceable in tests
}

// Alert is a notification shown in the admin panel.
type Alert struct {
	ID    int64     `json:"id"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"` // info | warn | error | ok
	PCID  int64     `json:"pcId"`
	Text  string    `json:"text"`
}

// apiError carries an HTTP status to the API layer.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errBad(format string, a ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

func errConflict(format string, a ...any) error {
	return &apiError{http.StatusConflict, fmt.Sprintf(format, a...)}
}

func errNotFound(what string) error {
	return &apiError{http.StatusNotFound, what + " not found"}
}

// New opens the database and prepares the server.
func New(cfg Config) (*Server, error) {
	if err := applyPendingRestore(cfg.DataDir()); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir(), "nextgaming.db"))
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, st: st, logins: newSessions(), wolTests: map[int64]bool{}, wokenBookings: map[int64]bool{}, now: time.Now}
	s.hub = newHub(s)
	s.bus = newBus(s)
	s.screens = newScreens()
	s.transfers = newTransfers(s)
	s.calls = newCalls(s)
	if s.settings, err = store.LoadSettings(st.DB); err != nil {
		return nil, err
	}
	// A server restart interrupts any wake test that was running.
	pcs, err := store.ListPCs(st.DB)
	if err != nil {
		return nil, err
	}
	for _, pc := range pcs {
		if pc.WoL.Status == core.WoLTesting {
			pc.WoL.Status, pc.WoL.Detail = core.WoLUntested, "The last wake test was interrupted by a server restart."
			store.UpdatePCWoL(st.DB, pc.ID, pc.WoL)
		}
	}
	s.warnDuplicatePhones()
	return s, nil
}

// Close releases the database.
func (s *Server) Close() error { return s.st.Close() }

// Run serves agents and the admin panel until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	errc := make(chan error, 2)
	go func() { errc <- s.hub.Listen(ctx, s.cfg.AgentAddr) }()

	httpSrv := &http.Server{Addr: s.cfg.HTTPAddr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("admin panel: http://localhost%s", displayPort(s.cfg.HTTPAddr))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("admin panel %s: %w", s.cfg.HTTPAddr, err)
		}
	}()
	go s.bus.run(ctx)
	go s.ticker(ctx)
	go s.backupLoop(ctx)
	go s.screenLoop(ctx)

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx)
	return nil
}

func displayPort(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return addr
}

// Settings returns the current club settings.
func (s *Server) Settings() core.Settings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	st := s.settings
	st.WarnMinutes = append([]int(nil), s.settings.WarnMinutes...)
	return st
}

// ClubKey returns the key agents need.
func (s *Server) ClubKey() string { return s.cfg.ClubKey }

// changed schedules an admin-panel refresh.
func (s *Server) changed() { s.bus.poke() }

// alert records a notification for the admin panel.
func (s *Server) alert(level string, pcID int64, format string, a ...any) {
	s.alertsMu.Lock()
	s.alertSeq++
	al := Alert{ID: s.alertSeq, Time: s.now(), Level: level, PCID: pcID, Text: fmt.Sprintf(format, a...)}
	s.alerts = append(s.alerts, al)
	if len(s.alerts) > 100 {
		s.alerts = s.alerts[len(s.alerts)-100:]
	}
	s.alertsMu.Unlock()
	log.Printf("alert [%s] %s", level, al.Text)
	s.changed()
}

func (s *Server) recentAlerts(n int) []Alert {
	s.alertsMu.Lock()
	defer s.alertsMu.Unlock()
	start := len(s.alerts) - n
	if start < 0 {
		start = 0
	}
	return append([]Alert(nil), s.alerts[start:]...)
}

// ticker ends expired prepaid sessions and periodically re-syncs agents.
func (s *Server) ticker(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	resync := time.NewTicker(60 * time.Second)
	defer resync.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.expireSessions()
			s.processBookings(s.now())
		case <-resync.C:
			pcs, err := store.ListPCs(s.st.DB)
			if err != nil {
				continue
			}
			for _, pc := range pcs {
				if s.hub.Online(pc.ID) {
					s.pushState(pc.ID)
				}
			}
		}
	}
}

// expireSessions ends prepaid sessions whose time is up.
func (s *Server) expireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	open, err := store.OpenSessions(s.st.DB)
	if err != nil {
		log.Printf("expire: %v", err)
		return
	}
	for i := range open {
		sess := &open[i]
		if !sess.Expired(now) {
			continue
		}
		if err := s.endExpiredLocked(sess, now); err != nil {
			log.Printf("expire session %d: %v", sess.ID, err)
		}
	}
}

func (s *Server) endExpiredLocked(sess *core.Session, now time.Time) error {
	if err := sess.End(now, "time_up"); err != nil {
		return err
	}
	err := s.st.Tx(func(q store.Q) error {
		if err := store.UpdateSession(q, sess); err != nil {
			return err
		}
		return store.Audit(q, 0, "session_time_up", sess.PCID, fmt.Sprintf("session %d", sess.ID))
	})
	if err != nil {
		return err
	}
	go s.pushState(sess.PCID)
	s.changed()
	return nil
}

// ---- agent callbacks ----

// errRemoved is returned for an agent whose PC was removed in the admin panel.
var errRemoved = errors.New("PC was removed")

// registerAgent finds or creates the PC for a connecting agent.
//
// A PC is found by its agent's machine ID, or failing that by its network
// card address (so a changed ID never creates a duplicate tile). A removed PC
// stays removed unless its agent was installed again after the removal.
func (s *Server) registerAgent(h protocol.Hello, ip string) (core.PC, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mac := pickMAC(h.NICs)
	var pc core.PC
	isNew := false
	err := s.st.Tx(func(q store.Q) error {
		found, err := store.PCByMachineID(q, h.MachineID)
		if err != nil {
			return err
		}
		if found == nil {
			// Unknown ID: maybe a PC we know whose ID changed (new agent version,
			// different adapter order). Match it by MAC, but never steal a PC
			// that is online right now.
			for _, n := range h.NICs {
				f, err := store.PCByMAC(q, n.MAC)
				if err != nil {
					return err
				}
				if f != nil && !s.hub.Online(f.ID) {
					if err := store.SetPCMachineID(q, f.ID, h.MachineID); err != nil {
						return err
					}
					found = f
					log.Printf("agents: %s matched by MAC %s; machine ID updated", f.Name, n.MAC)
					break
				}
			}
		}
		if found != nil && found.Deleted {
			// Old agents do not send InstalledAt; they keep the old behaviour (come back).
			// Removal times are stored in whole seconds, so compare seconds.
			reinstalled := h.InstalledAt.IsZero() || h.InstalledAt.Unix() > found.DeletedAt.Unix()
			if !reinstalled {
				return errRemoved
			}
			if err := store.UndeletePC(q, found.ID); err != nil {
				return err
			}
			store.Audit(q, 0, "pc_readded", found.ID, "agent was installed again")
		}
		if found != nil {
			pc = found.PC
			if mac == "" {
				mac = pc.MAC // keep the last known MAC if the cable is unplugged right now
			}
		} else {
			name, err := nextPCName(q)
			if err != nil {
				return err
			}
			zoneID := int64(0)
			if zones, err := store.ListZones(q); err == nil && len(zones) > 0 {
				zoneID = zones[0].ID
			}
			pc = core.PC{Name: name, ZoneID: zoneID, MachineID: h.MachineID}
			if pc.ID, err = store.CreatePC(q, pc); err != nil {
				return err
			}
			isNew = true
			store.Audit(q, 0, "pc_registered", pc.ID, fmt.Sprintf("%s (%s) registered automatically", name, h.Hostname))
		}
		pc.MAC, pc.IP, pc.Hostname = mac, ip, h.Hostname
		pc.BoardMaker, pc.BoardProduct, pc.AgentVersion = h.BoardMaker, h.BoardProduct, h.AgentVersion
		return store.UpdatePCHardware(q, pc.ID, mac, ip, h.Hostname, h.BoardMaker, h.BoardProduct, h.AgentVersion)
	})
	if err == nil && isNew {
		s.alert("info", pc.ID, "New PC connected and registered as %s (%s). Rename it or set its zone in Settings → PCs.", pc.Name, h.Hostname)
	}
	return pc, err
}

func pickMAC(nics []protocol.NIC) string {
	for _, n := range nics {
		if n.Wired && n.IP != "" && n.MAC != "" {
			return n.MAC
		}
	}
	for _, n := range nics {
		if n.Wired && n.MAC != "" {
			return n.MAC
		}
	}
	for _, n := range nics {
		if n.MAC != "" {
			return n.MAC
		}
	}
	return ""
}

func nextPCName(q store.Q) (string, error) {
	pcs, err := store.ListPCs(q)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, p := range pcs {
		used[strings.ToLower(p.Name)] = true
	}
	for i := 1; ; i++ {
		n := fmt.Sprintf("PC %d", i)
		if !used[strings.ToLower(n)] {
			return n, nil
		}
	}
}

func (s *Server) agentConnected(pcID int64) {
	s.pushState(pcID)
	s.changed()
}

func (s *Server) agentDisconnected(pcID int64) {
	s.screens.drop(pcID)
	s.transfers.dropPC(pcID)
	if s.wolTestRunning(pcID) {
		s.changed()
		return
	}
	sess, _ := store.OpenSessionForPC(s.st.DB, pcID)
	if sess != nil && sess.Status == core.StatusActive && sess.Mode != core.ModeFree {
		s.alert("warn", pcID, "%s went offline during a session. Its timer keeps running on the PC.", s.pcName(pcID))
	}
	s.changed()
}

func (s *Server) pcName(pcID int64) string {
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return fmt.Sprintf("PC #%d", pcID)
	}
	return pc.Name
}

func (s *Server) agentEvent(pcID int64, ev protocol.Event) {
	switch ev.Kind {
	case protocol.EventLocalExpired:
		// The PC's own timer ran out. Normally the server's ticker agrees;
		// if clocks drifted, resend the authoritative state.
		s.expireSessions()
		s.pushState(pcID)
	case protocol.EventUserLogout:
		s.playerCheckout(pcID, ev.SessionID)
	case protocol.EventAgentStopped:
		s.alert("warn", pcID, "NextGaming was stopped on %s (Services / Task Manager) and has started again.", s.pcName(pcID))
	case protocol.EventClientDied:
		s.alert("warn", pcID, "The lock screen on %s was closed and has been restarted (%s).", s.pcName(pcID), ev.Detail)
	}
}

// staffLogin checks a staff password entered on the lock screen and, if valid,
// unlocks the PC as a free (staff) session started by that staff member.
func (s *Server) staffLogin(pcID int64, username, password string) error {
	a, err := s.Login(username, password)
	if err != nil {
		time.Sleep(400 * time.Millisecond) // slow down guessing
		return errors.New("wrong username or password")
	}
	sess, _ := store.OpenSessionForPC(s.st.DB, pcID)
	if sess != nil {
		return errors.New("this PC already has a session")
	}
	if err := s.FreeUnlock(a.ID, pcID, "unlocked at the PC by "+a.Username); err != nil {
		return err
	}
	return nil
}

// playerCheckout handles the player pressing "End session" on the PC:
// the clock stops and the PC locks until the admin settles payment.
func (s *Server) playerCheckout(pcID, sessionID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := store.OpenSessionForPC(s.st.DB, pcID)
	if err != nil || sess == nil || sess.ID != sessionID {
		return
	}
	now := s.now()
	if sess.Mode == core.ModeFree {
		sess.End(now, "player_logout")
	} else {
		if sess.Status == core.StatusActive {
			sess.Pause(now)
		}
		sess.CheckoutRequested = true
	}
	err = s.st.Tx(func(q store.Q) error {
		if err := store.UpdateSession(q, sess); err != nil {
			return err
		}
		return store.Audit(q, 0, "player_end_session", pcID, fmt.Sprintf("session %d", sess.ID))
	})
	if err != nil {
		log.Printf("checkout: %v", err)
		return
	}
	go s.pushState(pcID)
	if sess.Mode != core.ModeFree {
		s.alert("info", pcID, "%s: the player ended the session. Settle it at the desk.", s.pcName(pcID))
	}
	s.changed()
}

// ---- pushing state to agents ----

func (s *Server) desiredState(pcID int64) (protocol.DesiredState, error) {
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return protocol.DesiredState{}, err
	}
	settings := s.Settings()
	ds := protocol.DesiredState{Locked: true, PCName: pc.Name, ClubName: settings.ClubName, WarnMinutes: settings.WarnMinutes}
	sess, err := store.OpenSessionForPC(s.st.DB, pcID)
	if err != nil {
		return ds, err
	}
	if sess == nil {
		return ds, nil
	}
	now := s.now()
	ds.SessionID = sess.ID
	ds.Mode = string(sess.Mode)
	ds.TariffName = sess.TariffName
	ds.RatePerHour = int64(sess.RatePerHour)
	ds.Paid = int64(sess.Paid)
	ds.Currency = settings.Currency
	ds.GraceSeconds = settings.GraceSeconds
	ds.MinChargeMinutes = settings.MinChargeMinutes
	if sess.Status == core.StatusPaused {
		ds.Paused = true
		return ds, nil
	}
	switch sess.Mode {
	case core.ModePrepaid:
		rem := sess.RemainingSeconds(now)
		if rem <= 0 {
			return ds, nil
		}
		ds.Locked = false
		ds.RemainingSec = rem
	default:
		ds.Locked = false
		ds.ElapsedSec = sess.ActiveSeconds(now)
	}
	return ds, nil
}

func (s *Server) pushState(pcID int64) {
	ds, err := s.desiredState(pcID)
	if err != nil {
		return
	}
	s.hub.Send(pcID, protocol.TypeState, ds)
}

// ---- JSON helpers ----

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
