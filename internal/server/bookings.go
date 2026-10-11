package server

import (
	"fmt"
	"log"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
	"nextgaming/internal/wol"
)

// preWakeLead is how long before a booking starts we wake its offline PCs.
const preWakeLead = 5 * time.Minute

// BookingView is a booking as the admin panel shows it.
type BookingView struct {
	core.Booking
	Kind    string   `json:"kind"` // "client" | "group"
	Who     string   `json:"who"`
	Phone   string   `json:"phone"`
	PCNames []string `json:"pcNames"`
	Members int      `json:"members"` // group size, when a group
}

// BookingRequest creates or edits a booking. Times are unix seconds.
type BookingRequest struct {
	ClientID int64   `json:"clientId"`
	GroupID  int64   `json:"groupId"`
	Start    int64   `json:"start"`
	End      int64   `json:"end"`
	Note     string  `json:"note"`
	PCIDs    []int64 `json:"pcIds"`
	Force    bool    `json:"force"`
}

// BookingResult is returned by create/update: either the saved booking, or a
// list of conflicting bookings when the soft overlap warning was not forced.
type BookingResult struct {
	Booking   *BookingView  `json:"booking,omitempty"`
	Conflicts []BookingView `json:"conflicts,omitempty"`
}

func (s *Server) bookingWho(q store.Q, b core.Booking) (kind, who, phone string, members int) {
	if b.GroupID != 0 {
		if g, err := store.GroupByID(q, b.GroupID); err == nil {
			return "group", g.Name, "", len(g.Members)
		}
		return "group", fmt.Sprintf("group #%d", b.GroupID), "", 0
	}
	if c, err := store.ClientByID(q, b.ClientID); err == nil {
		return "client", c.Name, c.Phone, 0
	}
	return "client", fmt.Sprintf("client #%d", b.ClientID), "", 0
}

func (s *Server) bookingView(q store.Q, b core.Booking) BookingView {
	v := BookingView{Booking: b, PCNames: []string{}}
	v.Kind, v.Who, v.Phone, v.Members = s.bookingWho(q, b)
	for _, pcID := range b.PCIDs {
		if pc, err := store.PCByID(q, pcID); err == nil {
			v.PCNames = append(v.PCNames, pc.Name)
		} else {
			v.PCNames = append(v.PCNames, fmt.Sprintf("#%d", pcID))
		}
	}
	return v
}

// ListBookings returns bookings overlapping [from,to].
func (s *Server) ListBookings(from, to time.Time) ([]BookingView, error) {
	bs, err := store.ListBookings(s.st.DB, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]BookingView, 0, len(bs))
	for _, b := range bs {
		out = append(out, s.bookingView(s.st.DB, b))
	}
	return out, nil
}

func (s *Server) validateBooking(q store.Q, req BookingRequest) (core.Booking, error) {
	var b core.Booking
	if (req.ClientID == 0) == (req.GroupID == 0) {
		return b, errBad("choose exactly one client or one group")
	}
	if req.ClientID != 0 {
		if _, err := store.ClientByID(q, req.ClientID); err != nil {
			return b, errBad("that client does not exist")
		}
	} else {
		if _, err := store.GroupByID(q, req.GroupID); err != nil {
			return b, errBad("that group does not exist")
		}
	}
	start, end := time.Unix(req.Start, 0), time.Unix(req.End, 0)
	if req.Start == 0 || req.End == 0 || !end.After(start) {
		return b, errBad("the end time must be after the start time")
	}
	if !end.After(s.now()) {
		return b, errBad("that booking is entirely in the past")
	}
	pcs := dedupeIDs(req.PCIDs)
	if len(pcs) == 0 {
		return b, errBad("choose at least one PC")
	}
	for _, pcID := range pcs {
		if _, err := store.PCByID(q, pcID); err != nil {
			return b, errBad("one of the chosen PCs does not exist")
		}
	}
	b = core.Booking{ClientID: req.ClientID, GroupID: req.GroupID, StartAt: start, EndAt: end,
		Note: req.Note, Status: core.BookingScheduled, PCIDs: pcs}
	return b, nil
}

// CreateBooking adds a booking. Overlaps are a soft warning: without Force, a
// clash returns the conflicting bookings instead of saving.
func (s *Server) CreateBooking(adminID int64, req BookingRequest) (BookingResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res BookingResult
	err := s.st.Tx(func(q store.Q) error {
		b, err := s.validateBooking(q, req)
		if err != nil {
			return err
		}
		if !req.Force {
			conf, err := store.OverlapBookings(q, b.PCIDs, b.StartAt, b.EndAt, 0)
			if err != nil {
				return err
			}
			if len(conf) > 0 {
				for _, c := range conf {
					res.Conflicts = append(res.Conflicts, s.bookingView(q, c))
				}
				return errHandledConflict
			}
		}
		if err := store.CreateBooking(q, &b); err != nil {
			return err
		}
		v := s.bookingView(q, b)
		res.Booking = &v
		return store.Audit(q, adminID, "booking_create", 0, fmt.Sprintf("%s %s–%s", v.Who, b.StartAt.Format("15:04"), b.EndAt.Format("15:04")))
	})
	if err == errHandledConflict {
		err = nil // conflicts returned, not an error
	}
	if err == nil {
		s.changed()
	}
	return res, err
}

// UpdateBooking edits a scheduled booking (same soft-overlap rule).
func (s *Server) UpdateBooking(adminID, id int64, req BookingRequest) (BookingResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res BookingResult
	err := s.st.Tx(func(q store.Q) error {
		cur, err := store.BookingByID(q, id)
		if err != nil {
			return errNotFound("booking")
		}
		if cur.Status == core.BookingCancelled || cur.Status == core.BookingNoShow {
			return errConflict("this booking is closed and can't be edited")
		}
		b, err := s.validateBooking(q, req)
		if err != nil {
			return err
		}
		b.ID, b.Status, b.StartedAt = id, cur.Status, cur.StartedAt
		if !req.Force {
			conf, err := store.OverlapBookings(q, b.PCIDs, b.StartAt, b.EndAt, id)
			if err != nil {
				return err
			}
			if len(conf) > 0 {
				for _, c := range conf {
					res.Conflicts = append(res.Conflicts, s.bookingView(q, c))
				}
				return errHandledConflict
			}
		}
		if err := store.UpdateBooking(q, &b); err != nil {
			return err
		}
		v := s.bookingView(q, b)
		res.Booking = &v
		return store.Audit(q, adminID, "booking_update", 0, fmt.Sprintf("booking %d", id))
	})
	if err == errHandledConflict {
		err = nil
	}
	if err == nil {
		s.changed()
	}
	return res, err
}

// errHandledConflict signals that overlap conflicts were collected (not a real error).
var errHandledConflict = fmt.Errorf("booking overlap")

// CancelBooking calls a booking off. Any sessions it started are ended free.
func (s *Server) CancelBooking(adminID, id int64) error {
	return s.closeBooking(adminID, id, core.BookingCancelled, "booking_cancelled")
}

// ReleaseNoShow frees a booking's PCs (ends its sessions free) and logs a no-show.
func (s *Server) ReleaseNoShow(adminID, id int64) error {
	return s.closeBooking(adminID, id, core.BookingNoShow, "booking_noshow")
}

func (s *Server) closeBooking(adminID, id int64, status core.BookingStatus, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pcs []int64
	var who string
	err := s.st.Tx(func(q store.Q) error {
		b, err := store.BookingByID(q, id)
		if err != nil {
			return errNotFound("booking")
		}
		if b.Status == core.BookingCancelled || b.Status == core.BookingNoShow {
			return errConflict("this booking is already closed")
		}
		_, who, _, _ = s.bookingWho(q, b)
		now := s.now()
		sessions, err := store.OpenSessionsForBooking(q, id)
		if err != nil {
			return err
		}
		for i := range sessions {
			sessions[i].EndedBy = adminID
			if err := sessions[i].End(now, reason); err != nil {
				return err
			}
			if err := store.UpdateSession(q, &sessions[i]); err != nil {
				return err
			}
			pcs = append(pcs, sessions[i].PCID)
		}
		if err := store.SetBookingStatus(q, id, status, b.StartedAt); err != nil {
			return err
		}
		return store.Audit(q, adminID, string(status), 0, fmt.Sprintf("booking %d (%s)", id, who))
	})
	if err != nil {
		return err
	}
	for _, pc := range pcs {
		go s.pushState(pc)
	}
	if status == core.BookingNoShow {
		s.alert("info", 0, "Booking for %s released as a no-show; its PCs are free.", who)
	}
	s.changed()
	return nil
}

// ---- the scheduler: pre-wake and auto-start ----

// processBookings wakes PCs shortly before their booking and auto-starts
// bookings whose time has come. Called every second by the ticker.
func (s *Server) processBookings(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Pre-wake offline PCs a few minutes before their booking (once each).
	up, err := store.UpcomingBookings(s.st.DB, now, now.Add(preWakeLead))
	if err == nil {
		for _, b := range up {
			if s.wokenBookings[b.ID] {
				continue
			}
			s.wokenBookings[b.ID] = true
			for _, pcID := range b.PCIDs {
				if s.hub.Online(pcID) {
					continue
				}
				if pc, err := store.PCByID(s.st.DB, pcID); err == nil && pc.MAC != "" {
					wol.Send(pc.MAC, pc.IP)
				}
			}
		}
	}

	due, err := store.DueBookings(s.st.DB, now)
	if err != nil {
		return
	}
	for i := range due {
		s.autoStartLocked(due[i], now)
	}
}

func (s *Server) autoStartLocked(b core.Booking, now time.Time) {
	var startedIDs []int64
	var started, failed []string
	err := s.st.Tx(func(q store.Q) error {
		_, who, _, _ := s.bookingWho(q, b)
		for _, pcID := range b.PCIDs {
			pc, err := store.PCByID(q, pcID)
			if err != nil {
				failed = append(failed, fmt.Sprintf("#%d", pcID))
				continue
			}
			if !s.hub.Online(pcID) {
				failed = append(failed, pc.Name+" (offline)")
				continue
			}
			if open, _ := store.OpenSessionForPC(q, pcID); open != nil {
				failed = append(failed, pc.Name+" (still in use)")
				continue
			}
			if err := s.startBookingPC(q, pc, who, b.ID, now); err != nil {
				failed = append(failed, pc.Name+" ("+err.Error()+")")
				continue
			}
			started = append(started, pc.Name)
			startedIDs = append(startedIDs, pc.ID)
		}
		return store.SetBookingStatus(q, b.ID, core.BookingStarted, now)
	})
	if err != nil {
		log.Printf("auto-start booking %d: %v", b.ID, err)
		return
	}
	for _, pcID := range startedIDs {
		go s.pushState(pcID)
	}
	_, who, _, _ := s.bookingWho(s.st.DB, b)
	if len(failed) == 0 {
		s.alert("ok", 0, "Booking for %s started on %d PC(s).", who, len(started))
	} else {
		s.alert("warn", 0, "Booking for %s: started %d PC(s); handle manually: %s", who, len(started), joinNames(failed))
	}
	s.changed()
}

// startBookingPC starts an open session for a booking. Open never auto-locks,
// and no money moves at start, so it does not require an open shift.
func (s *Server) startBookingPC(q store.Q, pc core.PC, who string, bookingID int64, now time.Time) error {
	rate := s.zoneRate(q, pc)
	if rate <= 0 {
		return fmt.Errorf("no hourly rate in its zone")
	}
	shiftID, _ := s.currentShift(q, false)
	note := who
	sess := &core.Session{PCID: pc.ID, Mode: core.ModeOpen, Status: core.StatusActive, StartedAt: now,
		RatePerHour: rate, StartedBy: 0, ShiftID: shiftID, TariffName: "Open (booking)", Note: note}
	if err := store.InsertSession(q, sess); err != nil {
		return err
	}
	if err := store.SetSessionBooking(q, sess.ID, bookingID); err != nil {
		return err
	}
	return store.Audit(q, 0, "booking_autostart", pc.ID, fmt.Sprintf("booking %d, %s", bookingID, who))
}

func joinNames(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// nextBookingMarks returns, per PC, the soonest upcoming booking in the next
// 24h, for the tile markers.
func (s *Server) nextBookingMarks(now time.Time) map[int64]*BookingMark {
	out := map[int64]*BookingMark{}
	up, err := store.UpcomingBookings(s.st.DB, now, now.Add(24*time.Hour))
	if err != nil {
		return out
	}
	for _, b := range up {
		_, who, _, _ := s.bookingWho(s.st.DB, b)
		for _, pcID := range b.PCIDs {
			if cur, ok := out[pcID]; !ok || b.StartAt.Before(cur.At) {
				out[pcID] = &BookingMark{At: b.StartAt, Who: who}
			}
		}
	}
	return out
}

// BookingMark is the "booked soon" note shown on a free PC's tile.
type BookingMark struct {
	At  time.Time `json:"at"`
	Who string    `json:"who"`
}
