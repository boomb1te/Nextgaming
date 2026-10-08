package server

import (
	"fmt"
	"strings"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
	"nextgaming/internal/wol"
)

// StartRequest starts a session on a PC.
type StartRequest struct {
	Mode      core.SessionMode `json:"mode"`      // prepaid | open
	PackageID int64            `json:"packageId"` // prepaid: a package...
	Minutes   int              `json:"minutes"`   // ...or a number of minutes at the hourly rate
	Note      string           `json:"note"`
}

// TimeRequest adds time to a prepaid session.
type TimeRequest struct {
	PackageID int64 `json:"packageId"`
	Minutes   int   `json:"minutes"`
}

// EndRequest ends a session.
type EndRequest struct {
	// Refund (prepaid only) is money handed back for unused time, in minor units.
	Refund core.Money `json:"refund"`
	Reason string     `json:"reason"`
}

// EndQuote previews what ending a session means for money.
type EndQuote struct {
	SessionID       int64            `json:"sessionId"`
	Mode            core.SessionMode `json:"mode"`
	ActiveSec       int64            `json:"activeSec"`
	RemainingSec    int64            `json:"remainingSec"`
	Paid            core.Money       `json:"paid"`
	Charge          core.Money       `json:"charge"`          // open: amount to collect now
	SuggestedRefund core.Money       `json:"suggestedRefund"` // prepaid: proportional unused amount
}

// EndResult reports what happened when a session ended.
type EndResult struct {
	Collected core.Money `json:"collected"`
	Refunded  core.Money `json:"refunded"`
	TotalPaid core.Money `json:"totalPaid"`
}

// pricing resolves a package or a number of minutes into minutes, price and a label.
func (s *Server) pricing(q store.Q, pc core.PC, rate core.Money, packageID int64, minutes int, now time.Time) (int, core.Money, string, error) {
	if packageID != 0 {
		p, err := store.PackageByID(q, packageID)
		if err != nil {
			return 0, 0, "", errBad("unknown package")
		}
		if !core.PackageFitsZone(p, pc.ZoneID) {
			return 0, 0, "", errBad("package %q is not valid in this PC's zone", p.Name)
		}
		if !core.PackageAvailable(p, now) {
			return 0, 0, "", errBad("package %q cannot be sold at this time of day", p.Name)
		}
		m := core.PackageMinutes(p, now)
		if m <= 0 {
			return 0, 0, "", errBad("package %q has no time", p.Name)
		}
		return m, p.Price, p.Name, nil
	}
	if minutes <= 0 {
		return 0, 0, "", errBad("choose a package or enter the number of minutes")
	}
	if minutes > 24*60 {
		return 0, 0, "", errBad("at most 24 hours at once")
	}
	if rate <= 0 {
		return 0, 0, "", errBad("this PC's zone has no hourly rate; set one in Settings → Zones")
	}
	return minutes, core.PriceForMinutes(rate, minutes), "Hourly", nil
}

func (s *Server) zoneRate(q store.Q, pc core.PC) core.Money {
	z, err := store.ZoneByID(q, pc.ZoneID)
	if err != nil {
		return 0
	}
	return z.HourlyRate
}

// currentShift returns the open shift, or an error if one is required and missing.
func (s *Server) currentShift(q store.Q, needed bool) (int64, error) {
	sh, ok, err := store.OpenShift(q)
	if err != nil {
		return 0, err
	}
	if !ok {
		if needed && s.Settings().RequireShift {
			return 0, errConflict("open a shift first (Shift tab) before taking money")
		}
		return 0, nil
	}
	return sh.ID, nil
}

// StartSession unlocks a PC for a customer.
func (s *Server) StartSession(adminID, pcID int64, req StartRequest) (*core.Session, error) {
	if req.Mode != core.ModePrepaid && req.Mode != core.ModeOpen {
		return nil, errBad("mode must be prepaid or open")
	}
	if !s.hub.Online(pcID) {
		return nil, errConflict("PC is offline; turn it on first")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var sess *core.Session
	err := s.st.Tx(func(q store.Q) error {
		pc, err := store.PCByID(q, pcID)
		if err != nil {
			return errNotFound("PC")
		}
		if open, err := store.OpenSessionForPC(q, pcID); err != nil {
			return err
		} else if open != nil {
			return errConflict("%s already has a session", pc.Name)
		}
		shiftID, err := s.currentShift(q, true)
		if err != nil {
			return err
		}
		rate := s.zoneRate(q, pc)
		sess = &core.Session{PCID: pcID, Mode: req.Mode, Status: core.StatusActive, StartedAt: now,
			RatePerHour: rate, StartedBy: adminID, ShiftID: shiftID, Note: strings.TrimSpace(req.Note)}
		var price core.Money
		if req.Mode == core.ModePrepaid {
			minutes, p, label, err := s.pricing(q, pc, rate, req.PackageID, req.Minutes, now)
			if err != nil {
				return err
			}
			price = p
			sess.PaidSeconds = int64(minutes) * 60
			sess.Paid = price
			sess.TariffName = label
		} else {
			if rate <= 0 {
				return errBad("this PC's zone has no hourly rate; set one in Settings → Zones")
			}
			sess.TariffName = "Open (pay at end)"
		}
		if err := store.InsertSession(q, sess); err != nil {
			return err
		}
		if req.Mode == core.ModePrepaid {
			if err := store.InsertTransaction(q, &core.Transaction{SessionID: sess.ID, PCID: pcID, ShiftID: shiftID,
				AdminID: adminID, Kind: core.TxSessionStart, Amount: price, CreatedAt: now}); err != nil {
				return err
			}
		}
		return store.Audit(q, adminID, "session_start", pcID, fmt.Sprintf("session %d, %s, %s, %s",
			sess.ID, sess.Mode, sess.TariffName, core.FormatMoney(price)))
	})
	if err != nil {
		return nil, err
	}
	s.pushState(pcID)
	s.changed()
	return sess, nil
}

// FreeUnlock unlocks a PC without billing (staff use, maintenance).
func (s *Server) FreeUnlock(adminID, pcID int64, note string) error {
	if !s.hub.Online(pcID) {
		return errConflict("PC is offline")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.st.Tx(func(q store.Q) error {
		if _, err := store.PCByID(q, pcID); err != nil {
			return errNotFound("PC")
		}
		open, err := store.OpenSessionForPC(q, pcID)
		if err != nil {
			return err
		}
		if open != nil {
			if open.Status == core.StatusPaused && !open.CheckoutRequested {
				return errConflict("this PC has a paused session; use Resume")
			}
			return errConflict("this PC already has a session")
		}
		sess := &core.Session{PCID: pcID, Mode: core.ModeFree, Status: core.StatusActive, StartedAt: s.now(),
			StartedBy: adminID, TariffName: "Free (staff)", Note: strings.TrimSpace(note)}
		if err := store.InsertSession(q, sess); err != nil {
			return err
		}
		return store.Audit(q, adminID, "free_unlock", pcID, fmt.Sprintf("session %d %s", sess.ID, note))
	})
	if err != nil {
		return err
	}
	s.pushState(pcID)
	s.changed()
	return nil
}

// withOpenSession loads the open session of a PC, lets fn modify it, and saves it.
func (s *Server) withOpenSession(pcID int64, fn func(q store.Q, sess *core.Session) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.st.Tx(func(q store.Q) error {
		sess, err := store.OpenSessionForPC(q, pcID)
		if err != nil {
			return err
		}
		if sess == nil {
			return errConflict("this PC has no session")
		}
		if err := fn(q, sess); err != nil {
			return err
		}
		return store.UpdateSession(q, sess)
	})
	if err != nil {
		return err
	}
	s.pushState(pcID)
	s.changed()
	return nil
}

// AddTime extends a prepaid session.
func (s *Server) AddTime(adminID, pcID int64, req TimeRequest) error {
	return s.withOpenSession(pcID, func(q store.Q, sess *core.Session) error {
		if sess.Mode != core.ModePrepaid {
			return errBad("only prepaid sessions have time to add; open sessions are billed at the end")
		}
		pc, err := store.PCByID(q, pcID)
		if err != nil {
			return errNotFound("PC")
		}
		shiftID, err := s.currentShift(q, true)
		if err != nil {
			return err
		}
		now := s.now()
		minutes, price, label, err := s.pricing(q, pc, sess.RatePerHour, req.PackageID, req.Minutes, now)
		if err != nil {
			return err
		}
		if err := sess.AddPrepaidTime(minutes, price); err != nil {
			return errBad("%v", err)
		}
		if sess.CheckoutRequested {
			// The player changed their mind and keeps playing.
			sess.CheckoutRequested = false
			if sess.Status == core.StatusPaused {
				sess.Resume(now)
			}
		}
		if err := store.InsertTransaction(q, &core.Transaction{SessionID: sess.ID, PCID: pcID, ShiftID: shiftID,
			AdminID: adminID, Kind: core.TxAddTime, Amount: price, CreatedAt: now}); err != nil {
			return err
		}
		return store.Audit(q, adminID, "add_time", pcID, fmt.Sprintf("session %d +%d min (%s) %s", sess.ID, minutes, label, core.FormatMoney(price)))
	})
}

// Pause stops a session's clock and locks the PC.
func (s *Server) Pause(adminID, pcID int64) error {
	return s.withOpenSession(pcID, func(q store.Q, sess *core.Session) error {
		if err := sess.Pause(s.now()); err != nil {
			return errConflict("%v", err)
		}
		return store.Audit(q, adminID, "pause", pcID, fmt.Sprintf("session %d", sess.ID))
	})
}

// Resume restarts a paused session and unlocks the PC.
func (s *Server) Resume(adminID, pcID int64) error {
	if !s.hub.Online(pcID) {
		return errConflict("PC is offline")
	}
	return s.withOpenSession(pcID, func(q store.Q, sess *core.Session) error {
		if err := sess.Resume(s.now()); err != nil {
			return errConflict("%v", err)
		}
		sess.CheckoutRequested = false
		if sess.Expired(s.now()) {
			return errConflict("no time left; add time instead")
		}
		return store.Audit(q, adminID, "resume", pcID, fmt.Sprintf("session %d", sess.ID))
	})
}

// Lock locks a PC: a free (staff) session ends, a paid session pauses.
func (s *Server) Lock(adminID, pcID int64) error {
	sess, err := store.OpenSessionForPC(s.st.DB, pcID)
	if err != nil {
		return err
	}
	if sess == nil {
		s.pushState(pcID)
		return nil
	}
	if sess.Mode == core.ModeFree {
		_, err := s.EndSession(adminID, pcID, EndRequest{Reason: "locked by admin"})
		return err
	}
	if sess.Status == core.StatusPaused {
		return nil
	}
	return s.Pause(adminID, pcID)
}

// Quote previews ending the session on a PC.
func (s *Server) Quote(pcID int64) (EndQuote, error) {
	sess, err := store.OpenSessionForPC(s.st.DB, pcID)
	if err != nil {
		return EndQuote{}, err
	}
	if sess == nil {
		return EndQuote{}, errConflict("this PC has no session")
	}
	return s.quote(sess, s.now()), nil
}

func (s *Server) quote(sess *core.Session, now time.Time) EndQuote {
	st := s.Settings()
	q := EndQuote{SessionID: sess.ID, Mode: sess.Mode, ActiveSec: sess.ActiveSeconds(now), Paid: sess.Paid}
	switch sess.Mode {
	case core.ModeOpen:
		q.Charge = core.OpenCharge(sess.RatePerHour, q.ActiveSec, st.GraceSeconds, st.MinChargeMinutes)
	case core.ModePrepaid:
		q.RemainingSec = sess.RemainingSeconds(now)
		if q.RemainingSec < 0 {
			q.RemainingSec = 0
		}
		q.SuggestedRefund = core.SuggestedRefund(sess.Paid, sess.PaidSeconds, q.RemainingSec)
	}
	return q
}

// EndSession closes the session on a PC, collecting (open) or refunding (prepaid) money.
func (s *Server) EndSession(adminID, pcID int64, req EndRequest) (EndResult, error) {
	var res EndResult
	err := s.withOpenSession(pcID, func(q store.Q, sess *core.Session) error {
		now := s.now()
		quote := s.quote(sess, now)
		reason := strings.TrimSpace(req.Reason)
		var amount core.Money
		kind := ""
		switch sess.Mode {
		case core.ModeOpen:
			if req.Refund != 0 {
				return errBad("open sessions have nothing to refund")
			}
			amount, kind = quote.Charge, core.TxOpenClose
			res.Collected = amount
		case core.ModePrepaid:
			if err := core.ValidateRefund(sess.Paid, req.Refund); err != nil {
				return errBad("%v", err)
			}
			if req.Refund > 0 {
				amount, kind = -req.Refund, core.TxRefund
				if reason == "" {
					reason = "unused time"
				}
				res.Refunded = req.Refund
			}
		}
		if amount != 0 {
			shiftID, err := s.currentShift(q, true)
			if err != nil {
				return err
			}
			if err := store.InsertTransaction(q, &core.Transaction{SessionID: sess.ID, PCID: pcID, ShiftID: shiftID,
				AdminID: adminID, Kind: kind, Amount: amount, Reason: reason, CreatedAt: now}); err != nil {
				return err
			}
			sess.Paid += amount
		}
		endReason := "admin"
		if sess.CheckoutRequested {
			endReason = "player_checkout"
		}
		if reason != "" && sess.Mode == core.ModeFree {
			endReason = reason
		}
		sess.CheckoutRequested = false
		sess.EndedBy = adminID
		if err := sess.End(now, endReason); err != nil {
			return errConflict("%v", err)
		}
		res.TotalPaid = sess.Paid
		return store.Audit(q, adminID, "session_end", pcID, fmt.Sprintf("session %d, %s, active %s, collected %s, refunded %s",
			sess.ID, sess.Mode, core.FormatDuration(sess.ActiveSeconds(now)), core.FormatMoney(res.Collected), core.FormatMoney(res.Refunded)))
	})
	return res, err
}

// MoveSession moves a running session to another (free, online) PC.
func (s *Server) MoveSession(adminID, fromPC, toPC int64) error {
	if fromPC == toPC {
		return errBad("choose a different PC")
	}
	if !s.hub.Online(toPC) {
		return errConflict("the target PC is offline")
	}
	s.mu.Lock()
	err := s.st.Tx(func(q store.Q) error {
		target, err := store.PCByID(q, toPC)
		if err != nil {
			return errNotFound("target PC")
		}
		if open, err := store.OpenSessionForPC(q, toPC); err != nil {
			return err
		} else if open != nil {
			return errConflict("%s is busy", target.Name)
		}
		sess, err := store.OpenSessionForPC(q, fromPC)
		if err != nil {
			return err
		}
		if sess == nil {
			return errConflict("this PC has no session")
		}
		sess.PCID = toPC
		if err := store.UpdateSession(q, sess); err != nil {
			return err
		}
		return store.Audit(q, adminID, "session_move", fromPC, fmt.Sprintf("session %d moved to %s (rate kept)", sess.ID, target.Name))
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.pushState(fromPC)
	s.pushState(toPC)
	s.changed()
	return nil
}

// SendMessage shows text on a PC's screen.
func (s *Server) SendMessage(adminID, pcID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errBad("message is empty")
	}
	if len(text) > 500 {
		return errBad("message is too long (500 characters max)")
	}
	if err := s.hub.Send(pcID, protocol.TypeMessage, protocol.Message{Text: text}); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, adminID, "message", pcID, text)
	return nil
}

// Power sends shutdown or restart. Busy PCs need force=true.
func (s *Server) Power(adminID, pcID int64, action string, force bool) error {
	typ := protocol.TypeShutdown
	if action == "restart" {
		typ = protocol.TypeRestart
	} else if action != "shutdown" {
		return errBad("unknown power action")
	}
	if !force {
		if sess, _ := store.OpenSessionForPC(s.st.DB, pcID); sess != nil && sess.Mode != core.ModeFree {
			return errConflict("this PC has a paid session; end it first or confirm to %s anyway", action)
		}
	}
	if err := s.hub.Command(pcID, typ, nil, 10*time.Second); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, adminID, action, pcID, "")
	return nil
}

// Wake sends a Wake-on-LAN packet to a PC.
func (s *Server) Wake(adminID, pcID int64) error {
	pc, err := store.PCByID(s.st.DB, pcID)
	if err != nil {
		return errNotFound("PC")
	}
	if s.hub.Online(pcID) {
		return errConflict("%s is already on", pc.Name)
	}
	if pc.MAC == "" {
		return errConflict("%s has no known network address yet; it must connect once first", pc.Name)
	}
	if err := wol.Send(pc.MAC, pc.IP); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, adminID, "wake", pcID, pc.MAC)
	return nil
}

// BulkResult reports a bulk action.
type BulkResult struct {
	Done    []string `json:"done"`
	Skipped []string `json:"skipped"`
}

// Bulk runs wake / shutdown / message on many PCs.
func (s *Server) Bulk(adminID int64, action, text string) (BulkResult, error) {
	var r BulkResult
	pcs, err := store.ListPCs(s.st.DB)
	if err != nil {
		return r, err
	}
	for _, pc := range pcs {
		online := s.hub.Online(pc.ID)
		var err error
		switch action {
		case "wake":
			if online {
				continue
			}
			err = s.Wake(adminID, pc.ID)
		case "shutdown_free":
			if !online {
				continue
			}
			if sess, _ := store.OpenSessionForPC(s.st.DB, pc.ID); sess != nil {
				r.Skipped = append(r.Skipped, pc.Name+" (busy)")
				continue
			}
			err = s.Power(adminID, pc.ID, "shutdown", false)
		case "message":
			if !online {
				continue
			}
			err = s.SendMessage(adminID, pc.ID, text)
		default:
			return r, errBad("unknown bulk action")
		}
		if err != nil {
			r.Skipped = append(r.Skipped, fmt.Sprintf("%s (%v)", pc.Name, err))
		} else {
			r.Done = append(r.Done, pc.Name)
		}
	}
	return r, nil
}

// ---- PCs, zones, packages, settings ----

// UpdatePC changes admin-editable PC fields.
func (s *Server) UpdatePC(adminID, pcID int64, name string, zoneID int64, sortOrder int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errBad("name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.st.Tx(func(q store.Q) error {
		if _, err := store.PCByID(q, pcID); err != nil {
			return errNotFound("PC")
		}
		pcs, err := store.ListPCs(q)
		if err != nil {
			return err
		}
		for _, p := range pcs {
			if p.ID != pcID && strings.EqualFold(p.Name, name) {
				return errConflict("another PC is already called %q", name)
			}
		}
		if zoneID != 0 {
			if _, err := store.ZoneByID(q, zoneID); err != nil {
				return errBad("unknown zone")
			}
		}
		if err := store.UpdatePCAdmin(q, pcID, name, zoneID, sortOrder); err != nil {
			return err
		}
		return store.Audit(q, adminID, "pc_update", pcID, fmt.Sprintf("name=%s zone=%d order=%d", name, zoneID, sortOrder))
	})
	if err == nil {
		go s.pushState(pcID) // the lock screen shows the new name
		s.changed()
	}
	return err
}

// SetPCNote saves the free staff note on a PC (any staff).
func (s *Server) SetPCNote(adminID, pcID int64, note string) error {
	note = strings.TrimSpace(note)
	if len(note) > 500 {
		return errBad("note is too long (500 characters max)")
	}
	if _, err := store.PCByID(s.st.DB, pcID); err != nil {
		return errNotFound("PC")
	}
	if err := store.SetPCNote(s.st.DB, pcID, note); err != nil {
		return err
	}
	store.Audit(s.st.DB, adminID, "pc_note", pcID, note)
	s.changed()
	return nil
}

// DeletePC removes a PC from the list (history is kept). If the PC is online,
// its agent is told to uninstall itself, so the PC is free to use and does
// not register again. Reinstalling the agent later brings the PC back.
func (s *Server) DeletePC(adminID, pcID int64) error {
	s.mu.Lock()
	err := s.st.Tx(func(q store.Q) error {
		if open, err := store.OpenSessionForPC(q, pcID); err != nil {
			return err
		} else if open != nil {
			return errConflict("end this PC's session first")
		}
		if err := store.DeletePC(q, pcID); err != nil {
			return err
		}
		return store.Audit(q, adminID, "pc_delete", pcID, "")
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if s.hub.Online(pcID) {
		s.hub.Command(pcID, protocol.TypeUninstall, nil, 5*time.Second)
	}
	s.hub.Disconnect(pcID)
	s.changed()
	return nil
}

// SaveZone creates or updates a zone.
func (s *Server) SaveZone(adminID int64, z core.Zone) (int64, error) {
	z.Name = strings.TrimSpace(z.Name)
	if z.Name == "" {
		return 0, errBad("zone name is required")
	}
	if z.HourlyRate < 0 {
		return 0, errBad("rate must not be negative")
	}
	id, err := store.SaveZone(s.st.DB, z)
	if err == nil {
		store.Audit(s.st.DB, adminID, "zone_save", 0, fmt.Sprintf("%s %s/h", z.Name, core.FormatMoney(z.HourlyRate)))
		s.changed()
	}
	return id, err
}

// DeleteZone removes an unused zone.
func (s *Server) DeleteZone(adminID, id int64) error {
	if err := store.DeleteZone(s.st.DB, id); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, adminID, "zone_delete", 0, fmt.Sprint(id))
	s.changed()
	return nil
}

// SavePackage creates or updates a package.
func (s *Server) SavePackage(adminID int64, p core.Package) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	switch {
	case p.Name == "":
		return 0, errBad("package name is required")
	case p.Price < 0:
		return 0, errBad("price must not be negative")
	case p.EndsAtHour < -1 || p.EndsAtHour > 23:
		return 0, errBad("'ends at' hour must be 0-23")
	case p.EndsAtHour == -1 && p.Minutes <= 0:
		return 0, errBad("minutes must be greater than zero")
	case p.AvailableFrom < -1 || p.AvailableFrom > 23 || p.AvailableTo < -1 || p.AvailableTo > 23:
		return 0, errBad("availability hours must be 0-23")
	}
	id, err := store.SavePackage(s.st.DB, p)
	if err == nil {
		store.Audit(s.st.DB, adminID, "package_save", 0, fmt.Sprintf("%s %s", p.Name, core.FormatMoney(p.Price)))
		s.changed()
	}
	return id, err
}

// DeletePackage removes a package.
func (s *Server) DeletePackage(adminID, id int64) error {
	if err := store.DeletePackage(s.st.DB, id); err != nil {
		return err
	}
	store.Audit(s.st.DB, adminID, "package_delete", 0, fmt.Sprint(id))
	s.changed()
	return nil
}

// SaveSettings updates club settings.
func (s *Server) SaveSettings(adminID int64, st core.Settings) error {
	st.ClubName = strings.TrimSpace(st.ClubName)
	st.Currency = strings.TrimSpace(st.Currency)
	if st.ClubName == "" {
		return errBad("club name is required")
	}
	if st.GraceSeconds < 0 || st.MinChargeMinutes < 0 {
		return errBad("values must not be negative")
	}
	var warn []int
	for _, m := range st.WarnMinutes {
		if m > 0 && m <= 60 {
			warn = append(warn, m)
		}
	}
	st.WarnMinutes = warn
	if err := store.SaveSettings(s.st.DB, st); err != nil {
		return err
	}
	s.settingsMu.Lock()
	s.settings = st
	s.settingsMu.Unlock()
	store.Audit(s.st.DB, adminID, "settings_save", 0, mustJSON(st))
	go s.pushAll()
	s.changed()
	return nil
}

// pushAll resends the desired state to every connected PC (club name, warnings...).
func (s *Server) pushAll() {
	pcs, _ := store.ListPCs(s.st.DB)
	for _, pc := range pcs {
		if s.hub.Online(pc.ID) {
			s.pushState(pc.ID)
		}
	}
}

// ---- shifts and cash ----

// ShiftView is the current shift with live totals.
type ShiftView struct {
	core.Shift
	AdminName string     `json:"adminName"`
	Takings   core.Money `json:"takings"` // net money taken during the shift
	CashNow   core.Money `json:"cashNow"` // opening cash + takings
	TxCount   int        `json:"txCount"`
}

// CurrentShift returns the open shift with totals, or nil.
func (s *Server) CurrentShift() (*ShiftView, error) {
	sh, ok, err := store.OpenShift(s.st.DB)
	if err != nil || !ok {
		return nil, err
	}
	return s.shiftView(s.st.DB, sh)
}

func (s *Server) shiftView(q store.Q, sh core.Shift) (*ShiftView, error) {
	txs, err := store.TransactionsForShift(q, sh.ID)
	if err != nil {
		return nil, err
	}
	v := &ShiftView{Shift: sh, TxCount: len(txs)}
	for _, t := range txs {
		v.Takings += t.Amount
	}
	v.CashNow = sh.OpeningCash + v.Takings
	if a, err := store.AdminByID(q, sh.AdminID); err == nil {
		v.AdminName = a.Username
	}
	return v, nil
}

// OpenShift starts a shift.
func (s *Server) OpenShift(adminID int64, openingCash core.Money, note string) error {
	if openingCash < 0 {
		return errBad("opening cash must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.st.Tx(func(q store.Q) error {
		if _, ok, err := store.OpenShift(q); err != nil {
			return err
		} else if ok {
			return errConflict("a shift is already open; close it first")
		}
		sh := &core.Shift{AdminID: adminID, OpenedAt: s.now(), OpeningCash: openingCash, Note: note}
		if err := store.InsertShift(q, sh); err != nil {
			return err
		}
		return store.Audit(q, adminID, "shift_open", 0, core.FormatMoney(openingCash))
	})
	if err == nil {
		s.changed()
	}
	return err
}

// CloseShift ends the shift with the cash the cashier counted.
func (s *Server) CloseShift(adminID int64, counted core.Money, note string) (*ShiftView, error) {
	if counted < 0 {
		return nil, errBad("counted cash must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var view *ShiftView
	err := s.st.Tx(func(q store.Q) error {
		sh, ok, err := store.OpenShift(q)
		if err != nil {
			return err
		}
		if !ok {
			return errConflict("no shift is open")
		}
		v, err := s.shiftView(q, sh)
		if err != nil {
			return err
		}
		now := s.now()
		sh.ClosedAt = &now
		sh.ClosingCash = counted
		sh.Expected = v.CashNow
		sh.Note = strings.TrimSpace(note)
		if err := store.CloseShift(q, sh); err != nil {
			return err
		}
		v.Shift = sh
		view = v
		return store.Audit(q, adminID, "shift_close", 0, fmt.Sprintf("expected %s counted %s difference %s",
			core.FormatMoney(sh.Expected), core.FormatMoney(counted), core.FormatMoney(counted-sh.Expected)))
	})
	if err == nil {
		s.changed()
	}
	return view, err
}

// Correction records a manual cash correction (positive or negative).
func (s *Server) Correction(adminID int64, amount core.Money, reason string, pcID int64) error {
	reason = strings.TrimSpace(reason)
	if amount == 0 {
		return errBad("amount must not be zero")
	}
	if reason == "" {
		return errBad("a reason is required for corrections")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.st.Tx(func(q store.Q) error {
		shiftID, err := s.currentShift(q, true)
		if err != nil {
			return err
		}
		if err := store.InsertTransaction(q, &core.Transaction{PCID: pcID, ShiftID: shiftID, AdminID: adminID,
			Kind: core.TxCorrection, Amount: amount, Reason: reason, CreatedAt: s.now()}); err != nil {
			return err
		}
		return store.Audit(q, adminID, "correction", pcID, fmt.Sprintf("%s: %s", core.FormatMoney(amount), reason))
	})
	if err == nil {
		s.changed()
	}
	return err
}

// ---- admins ----

// Setup creates the first owner account and seeds default tariffs.
func (s *Server) Setup(username, password, clubName string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errBad("username is required")
	}
	if err := ValidatePassword(password); err != nil {
		return 0, errBad("%v", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err = s.st.Tx(func(q store.Q) error {
		n, err := store.AdminCount(q)
		if err != nil {
			return err
		}
		if n > 0 {
			return errConflict("setup is already done")
		}
		if id, err = store.CreateAdmin(q, username, hash, core.RoleOwner); err != nil {
			return err
		}
		st := core.DefaultSettings()
		if c := strings.TrimSpace(clubName); c != "" {
			st.ClubName = c
		}
		if err := store.SaveSettings(q, st); err != nil {
			return err
		}
		s.settingsMu.Lock()
		s.settings = st
		s.settingsMu.Unlock()
		// Starter tariffs; prices are placeholders to edit in Settings.
		if zones, _ := store.ListZones(q); len(zones) == 0 {
			zid, err := store.SaveZone(q, core.Zone{Name: "Standard", HourlyRate: 1000})
			if err != nil {
				return err
			}
			if _, err := q.Exec("UPDATE pcs SET zone_id=? WHERE zone_id=0", zid); err != nil {
				return err
			}
			for _, p := range []core.Package{
				{Name: "1 hour", Minutes: 60, Price: 1000, EndsAtHour: -1, AvailableFrom: -1, AvailableTo: -1, Active: true},
				{Name: "3 hours", Minutes: 180, Price: 2500, EndsAtHour: -1, AvailableFrom: -1, AvailableTo: -1, Active: true},
				{Name: "Night pack (until 08:00)", Price: 4000, EndsAtHour: 8, AvailableFrom: 22, AvailableTo: 3, Active: true},
			} {
				if _, err := store.SavePackage(q, p); err != nil {
					return err
				}
			}
		}
		return store.Audit(q, id, "setup", 0, "owner account created")
	})
	if err == nil {
		go s.pushAll()
		s.changed()
	}
	return id, err
}

// NeedsSetup reports whether no admin account exists yet.
func (s *Server) NeedsSetup() bool {
	n, err := store.AdminCount(s.st.DB)
	return err == nil && n == 0
}

// Login checks credentials.
func (s *Server) Login(username, password string) (core.Admin, error) {
	a, hash, err := store.AdminByUsername(s.st.DB, username)
	if err != nil || a.Disabled || !CheckPassword(password, hash) {
		return core.Admin{}, &apiError{401, "wrong username or password"}
	}
	store.Audit(s.st.DB, a.ID, "login", 0, "")
	return a, nil
}

// CreateAdmin adds a staff account (owner only).
func (s *Server) CreateAdmin(by core.Admin, username, password, role string) error {
	if by.Role != core.RoleOwner {
		return &apiError{403, "only the owner can manage staff accounts"}
	}
	if role != core.RoleOwner && role != core.RoleCashier {
		return errBad("role must be owner or cashier")
	}
	if strings.TrimSpace(username) == "" {
		return errBad("username is required")
	}
	if err := ValidatePassword(password); err != nil {
		return errBad("%v", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := store.CreateAdmin(s.st.DB, username, hash, role); err != nil {
		return errConflict("%v", err)
	}
	store.Audit(s.st.DB, by.ID, "admin_create", 0, username+" ("+role+")")
	return nil
}

// SetPassword changes a password. Owners can change anyone's; others only their own.
func (s *Server) SetPassword(by core.Admin, targetID int64, current, password string) error {
	if by.ID != targetID && by.Role != core.RoleOwner {
		return &apiError{403, "you can only change your own password"}
	}
	if by.ID == targetID {
		_, hash, err := store.AdminByUsername(s.st.DB, by.Username)
		if err != nil || !CheckPassword(current, hash) {
			return errBad("current password is wrong")
		}
	}
	if err := ValidatePassword(password); err != nil {
		return errBad("%v", err)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := store.SetAdminPassword(s.st.DB, targetID, hash); err != nil {
		return err
	}
	if by.ID != targetID {
		s.logins.DeleteAdmin(targetID)
	}
	store.Audit(s.st.DB, by.ID, "password_change", 0, fmt.Sprintf("admin %d", targetID))
	return nil
}

// SetAdminDisabled disables or enables a staff login (owner only).
func (s *Server) SetAdminDisabled(by core.Admin, targetID int64, disabled bool) error {
	if by.Role != core.RoleOwner {
		return &apiError{403, "only the owner can manage staff accounts"}
	}
	if by.ID == targetID {
		return errBad("you cannot disable your own account")
	}
	if err := store.SetAdminDisabled(s.st.DB, targetID, disabled); err != nil {
		return err
	}
	if disabled {
		s.logins.DeleteAdmin(targetID)
	}
	store.Audit(s.st.DB, by.ID, "admin_disable", 0, fmt.Sprintf("admin %d disabled=%v", targetID, disabled))
	return nil
}

// UpdateAdmin renames a staff account and/or changes its role (owner only).
func (s *Server) UpdateAdmin(by core.Admin, targetID int64, username, role string) error {
	if by.Role != core.RoleOwner {
		return &apiError{403, "only the owner can manage staff accounts"}
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errBad("username is required")
	}
	if role != core.RoleOwner && role != core.RoleCashier {
		return errBad("role must be owner or cashier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		target, err := store.AdminByID(q, targetID)
		if err != nil {
			return errNotFound("account")
		}
		// Don't allow removing the last owner by demoting them.
		if target.Role == core.RoleOwner && role != core.RoleOwner {
			owners, err := store.CountOwners(q)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return errConflict("this is the only owner account; make another owner first")
			}
		}
		if err := store.UpdateAdmin(q, targetID, username, role); err != nil {
			return errConflict("%v", err)
		}
		if by.ID == targetID && role != core.RoleOwner {
			s.logins.DeleteAdmin(targetID) // you demoted yourself: re-login needed
		}
		return store.Audit(q, by.ID, "admin_update", 0, fmt.Sprintf("admin %d -> %s (%s)", targetID, username, role))
	})
}

// DeleteAdminAccount removes a staff account (owner only).
func (s *Server) DeleteAdminAccount(by core.Admin, targetID int64) error {
	if by.Role != core.RoleOwner {
		return &apiError{403, "only the owner can manage staff accounts"}
	}
	if by.ID == targetID {
		return errBad("you cannot delete your own account")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Tx(func(q store.Q) error {
		target, err := store.AdminByID(q, targetID)
		if err != nil {
			return errNotFound("account")
		}
		if target.Role == core.RoleOwner {
			owners, err := store.CountOwners(q)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return errConflict("this is the only owner account; make another owner first")
			}
		}
		if err := store.DeleteAdmin(q, targetID); err != nil {
			return err
		}
		s.logins.DeleteAdmin(targetID)
		return store.Audit(q, by.ID, "admin_delete", 0, fmt.Sprintf("admin %d", targetID))
	})
}
