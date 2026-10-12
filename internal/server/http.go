package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
)

//go:embed web
var webFS embed.FS

const cookieName = "ng_session"

// errHandled means the handler already wrote the HTTP response itself.
var errHandled = fmt.Errorf("response already written")

type ctxAdmin struct{}

type handler func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	// public
	mux.HandleFunc("GET /api/setup", s.jsonPublic(func(r *http.Request) (any, error) {
		return map[string]any{"needsSetup": s.NeedsSetup(), "version": Version}, nil
	}))
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// authenticated
	h := func(pattern string, fn handler) { mux.HandleFunc(pattern, s.auth(fn, false)) }
	owner := func(pattern string, fn handler) { mux.HandleFunc(pattern, s.auth(fn, true)) }

	h("GET /api/me", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) { return a, nil })
	h("GET /api/state", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) { return s.BuildSnapshot() })
	mux.HandleFunc("GET /api/events", s.handleEvents)

	// Screen monitoring.
	h("POST /api/screens/state", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			Watching bool  `json:"watching"`
			Focus    int64 `json:"focus"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		s.screens.setWatch(req.Watching, req.Focus, s.now())
		return ok(), nil
	})
	mux.HandleFunc("GET /api/pcs/{id}/screen.jpg", s.handleScreenJPEG)
	// One-off HD snapshot: POST asks the PC for a sharp frame; the GET polls for
	// it (?since=<unix ms> returns 204 until a newer snapshot has arrived).
	h("POST /api/pcs/{id}/snapshot", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.RequestSnapshot(pathID(r))
	})
	mux.HandleFunc("GET /api/pcs/{id}/snapshot.jpg", s.handleSnapshotJPEG)

	h("POST /api/pcs/{id}/start", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req StartRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.StartSession(a.ID, pathID(r), req)
	})
	h("POST /api/pcs/{id}/add-time", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req TimeRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.AddTime(a.ID, pathID(r), req)
	})
	h("POST /api/pcs/{id}/pause", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.Pause(a.ID, pathID(r))
	})
	h("POST /api/pcs/{id}/resume", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.Resume(a.ID, pathID(r))
	})
	h("POST /api/pcs/{id}/lock", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.Lock(a.ID, pathID(r))
	})
	h("POST /api/pcs/{id}/unlock-free", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Note string }
		decode(r, &req)
		return ok(), s.FreeUnlock(a.ID, pathID(r), req.Note)
	})
	h("GET /api/pcs/{id}/quote", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.Quote(pathID(r))
	})
	h("POST /api/pcs/{id}/end", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req EndRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.EndSession(a.ID, pathID(r), req)
	})
	h("POST /api/pcs/{id}/move", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			ToPCID int64 `json:"toPcId"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.MoveSession(a.ID, pathID(r), req.ToPCID)
	})
	h("POST /api/pcs/{id}/note", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Note string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.SetPCNote(a.ID, pathID(r), req.Note)
	})
	h("POST /api/pcs/{id}/message", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Text string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.SendMessage(a.ID, pathID(r), req.Text)
	})
	h("POST /api/pcs/{id}/power", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			Action string
			Force  bool
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.Power(a.ID, pathID(r), req.Action, req.Force)
	})
	h("POST /api/pcs/{id}/wake", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.Wake(a.ID, pathID(r))
	})
	h("POST /api/pcs/{id}/wol-check", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Fix bool }
		decode(r, &req)
		return ok(), s.WoLCheck(a.ID, pathID(r), req.Fix)
	})
	h("POST /api/pcs/{id}/wol-test", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Force bool }
		decode(r, &req)
		return ok(), s.StartWoLTest(a.ID, pathID(r), req.Force)
	})
	h("POST /api/bulk", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Action, Text string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.Bulk(a.ID, req.Action, req.Text)
	})
	owner("PUT /api/pcs/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			Name      string `json:"name"`
			ZoneID    int64  `json:"zoneId"`
			SortOrder int    `json:"sortOrder"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.UpdatePC(a.ID, pathID(r), req.Name, req.ZoneID, req.SortOrder)
	})
	owner("DELETE /api/pcs/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeletePC(a.ID, pathID(r))
	})

	owner("POST /api/zones", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var z core.Zone
		if err := decode(r, &z); err != nil {
			return nil, err
		}
		z.ID = 0
		id, err := s.SaveZone(a.ID, z)
		return map[string]int64{"id": id}, err
	})
	owner("PUT /api/zones/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var z core.Zone
		if err := decode(r, &z); err != nil {
			return nil, err
		}
		z.ID = pathID(r)
		_, err := s.SaveZone(a.ID, z)
		return ok(), err
	})
	owner("DELETE /api/zones/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeleteZone(a.ID, pathID(r))
	})
	owner("POST /api/packages", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var p core.Package
		if err := decode(r, &p); err != nil {
			return nil, err
		}
		p.ID = 0
		id, err := s.SavePackage(a.ID, p)
		return map[string]int64{"id": id}, err
	})
	owner("PUT /api/packages/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var p core.Package
		if err := decode(r, &p); err != nil {
			return nil, err
		}
		p.ID = pathID(r)
		_, err := s.SavePackage(a.ID, p)
		return ok(), err
	})
	owner("DELETE /api/packages/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeletePackage(a.ID, pathID(r))
	})
	owner("PUT /api/settings", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var st core.Settings
		if err := decode(r, &st); err != nil {
			return nil, err
		}
		return ok(), s.SaveSettings(a.ID, st)
	})
	owner("GET /api/club-key", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return map[string]any{
			"clubKey":   s.cfg.ClubKey,
			"agentPort": portOf(s.cfg.AgentAddr, protocol.DefaultAgentPort),
			"serverIPs": localIPv4s(),
		}, nil
	})
	owner("POST /api/backup", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		path, err := s.BackupNow()
		return map[string]string{"path": path}, err
	})
	owner("GET /api/export/db", s.handleExportDB)
	owner("GET /api/export/payments.csv", s.handleExportCSV("payments"))
	owner("GET /api/export/sessions.csv", s.handleExportCSV("sessions"))
	owner("POST /api/import/db", s.handleImportDB)

	h("POST /api/shift/open", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			OpeningCash core.Money `json:"openingCash"`
			Note        string     `json:"note"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.OpenShift(a.ID, req.OpeningCash, req.Note)
	})
	h("POST /api/shift/close", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			CountedCash core.Money `json:"countedCash"`
			Note        string     `json:"note"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.CloseShift(a.ID, req.CountedCash, req.Note)
	})
	h("POST /api/corrections", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			Amount core.Money `json:"amount"`
			Reason string     `json:"reason"`
			PCID   int64      `json:"pcId"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.Correction(a.ID, req.Amount, req.Reason, req.PCID)
	})
	h("GET /api/reports/daily", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.Report(r.URL.Query().Get("date"))
	})
	h("GET /api/audit", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.AuditLog(r.URL.Query().Get("date"))
	})

	// Clients and groups (booking system). Any signed-in staff can manage them.
	h("GET /api/clients", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.ListClients(r.URL.Query().Get("q"))
	})
	h("POST /api/clients", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Name, Phone, Note string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		id, err := s.CreateClient(a.ID, req.Name, req.Phone, req.Note)
		return map[string]int64{"id": id}, err
	})
	h("PUT /api/clients/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Name, Phone, Note string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.UpdateClient(a.ID, pathID(r), req.Name, req.Phone, req.Note)
	})
	h("DELETE /api/clients/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeleteClient(a.ID, pathID(r))
	})
	h("GET /api/groups", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.ListGroups()
	})
	h("GET /api/groups/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.GetGroup(pathID(r))
	})
	h("POST /api/groups", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			Name, Note string
			MemberIDs  []int64 `json:"memberIds"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		id, err := s.CreateGroup(a.ID, req.Name, req.Note, req.MemberIDs)
		return map[string]int64{"id": id}, err
	})
	h("PUT /api/groups/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Name, Note string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.UpdateGroup(a.ID, pathID(r), req.Name, req.Note)
	})
	h("DELETE /api/groups/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeleteGroup(a.ID, pathID(r))
	})
	h("POST /api/groups/{id}/members", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			ClientID int64 `json:"clientId"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.AddGroupMember(a.ID, pathID(r), req.ClientID)
	})
	h("DELETE /api/groups/{id}/members/{clientId}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		cid, _ := strconv.ParseInt(r.PathValue("clientId"), 10, 64)
		return ok(), s.RemoveGroupMember(a.ID, pathID(r), cid)
	})

	// Bookings.
	h("GET /api/bookings", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		from := parseUnix(r.URL.Query().Get("from"))
		to := parseUnix(r.URL.Query().Get("to"))
		if from.IsZero() {
			from = s.now().Add(-12 * time.Hour)
		}
		if to.IsZero() {
			to = from.Add(7 * 24 * time.Hour)
		}
		return s.ListBookings(from, to)
	})
	h("POST /api/bookings", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req BookingRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.CreateBooking(a.ID, req)
	})
	h("PUT /api/bookings/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req BookingRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.UpdateBooking(a.ID, pathID(r), req)
	})
	h("POST /api/bookings/{id}/cancel", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.CancelBooking(a.ID, pathID(r))
	})
	h("POST /api/bookings/{id}/noshow", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.ReleaseNoShow(a.ID, pathID(r))
	})

	// File sharing (LAN only). Browse the front-desk PC's own filesystem, list a
	// gaming PC's single shared folder, and copy files either way. Any signed-in
	// staff can use it, like messaging or power actions.
	h("GET /api/fs/local", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.ListLocal(r.URL.Query().Get("path"))
	})
	h("GET /api/fs/pc/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.ListPCShared(pathID(r), r.URL.Query().Get("rel"))
	})
	h("POST /api/fs/send", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			PCID    int64  `json:"pcId"`
			SrcPath string `json:"srcPath"`
			DestRel string `json:"destRel"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.SendToPC(a.ID, req.PCID, req.SrcPath, req.DestRel)
	})
	h("POST /api/fs/get", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct {
			PCID    int64  `json:"pcId"`
			SrcRel  string `json:"srcRel"`
			Name    string `json:"name"`
			DestDir string `json:"destDir"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return s.GetFromPC(a.ID, req.PCID, req.SrcRel, req.Name, req.DestDir)
	})
	h("GET /api/fs/transfers", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return s.transfers.list(), nil
	})
	// Agent-facing byte channel: authenticated by the club key + a one-time
	// token, never the admin cookie, so these are raw handlers (no CSRF, no login).
	mux.HandleFunc("GET /api/fs/serve/{token}", s.handleFSServe)
	mux.HandleFunc("POST /api/fs/recv/{token}", s.handleFSRecv)

	// Incoming calls (LAN only). The operator phone posts an answered call's
	// number here (club key in X-NextGaming-Phone, not the admin cookie).
	mux.HandleFunc("GET /api/phone/call", s.handlePhoneCall)
	mux.HandleFunc("POST /api/phone/call", s.handlePhoneCall)
	// Operator dismisses a pending-call prompt (booked it or declined).
	h("POST /api/calls/{id}/dismiss", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		s.DismissCall(pathID(r))
		return ok(), nil
	})
	// Dev/test: simulate an incoming call from the panel itself (no phone needed).
	h("POST /api/calls/simulate", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Number string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.RecordIncomingCall(req.Number)
	})

	h("GET /api/admins", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return store.ListAdmins(s.st.DB)
	})
	h("POST /api/admins", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Username, Password, Role string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.CreateAdmin(a, req.Username, req.Password, req.Role)
	})
	h("POST /api/admins/{id}/password", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Current, Password string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.SetPassword(a, pathID(r), req.Current, req.Password)
	})
	h("POST /api/admins/{id}/disabled", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Disabled bool }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.SetAdminDisabled(a, pathID(r), req.Disabled)
	})
	h("PUT /api/admins/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		var req struct{ Username, Role string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ok(), s.UpdateAdmin(a, pathID(r), req.Username, req.Role)
	})
	h("DELETE /api/admins/{id}", func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		return ok(), s.DeleteAdminAccount(a, pathID(r))
	})

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// blob: is needed for the Screens tab: captured frames are fetched and
		// shown via URL.createObjectURL (a blob: URL) on the <img>.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:")
		next.ServeHTTP(w, r)
	})
}

func ok() map[string]bool { return map[string]bool{"ok": true} }

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// parseUnix parses a unix-seconds query parameter; zero time if missing/invalid.
func parseUnix(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return errBad("could not read request")
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errBad("invalid request: %v", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		return
	}
	log.Printf("api error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error: " + err.Error()})
}

// csrfOK requires a custom header on state-changing requests. Browsers never
// add custom headers to cross-site form posts, so this blocks CSRF.
func csrfOK(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return r.Header.Get("X-NextGaming") == "1"
}

func (s *Server) currentAdmin(r *http.Request) (core.Admin, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return core.Admin{}, false
	}
	id, ok := s.logins.Lookup(c.Value)
	if !ok {
		return core.Admin{}, false
	}
	a, err := store.AdminByID(s.st.DB, id)
	if err != nil || a.Disabled {
		return core.Admin{}, false
	}
	return a, true
}

func (s *Server) auth(fn handler, ownerOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !csrfOK(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing request header"})
			return
		}
		a, ok := s.currentAdmin(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "please log in"})
			return
		}
		if ownerOnly && a.Role != core.RoleOwner {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the owner can do this"})
			return
		}
		v, err := fn(w, r, a)
		if err == errHandled {
			return // the handler already wrote the response (file download)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) jsonPublic(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(loginTTL.Seconds())})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing request header"})
		return
	}
	var req struct{ Username, Password, ClubName string }
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	id, err := s.Setup(req.Username, req.Password, req.ClubName)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.setCookie(w, s.logins.Create(id))
	writeJSON(w, http.StatusOK, ok())
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing request header"})
		return
	}
	ip := clientIP(r)
	if s.logins.Throttled(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed attempts; wait 5 minutes"})
		return
	}
	var req struct{ Username, Password string }
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	a, err := s.Login(req.Username, req.Password)
	if err != nil {
		s.logins.Failed(ip)
		time.Sleep(400 * time.Millisecond)
		writeErr(w, err)
		return
	}
	s.logins.Succeeded(ip)
	s.setCookie(w, s.logins.Create(a.ID))
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.logins.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, ok())
}

// handleScreenJPEG serves the latest captured frame for one PC. Pulling a
// frame also keeps monitoring alive, so the grid refreshes even if a heartbeat
// is lost. Returns 204 when there is no fresh frame.
func (s *Server) handleScreenJPEG(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentAdmin(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "please log in"})
		return
	}
	s.screens.keepAlive(s.now())
	w.Header().Set("Cache-Control", "no-store")
	f, ok := s.screens.get(pathID(r))
	if !ok || len(f.data) == 0 || s.now().Sub(f.at) > screenStale {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(f.data)
}

// handleSnapshotJPEG serves the latest one-off HD snapshot for a PC, but only
// once one captured after the most recent request has arrived (204 until then),
// so the panel can poll right after asking for one. Freshness uses the server
// clock only.
func (s *Server) handleSnapshotJPEG(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentAdmin(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "please log in"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	f, ok := s.screens.readySnap(pathID(r), s.now())
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(f.data)
}

// handleEvents streams snapshots to the admin panel (Server-Sent Events).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentAdmin(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "please log in"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	ch := s.bus.subscribe()
	defer s.bus.unsubscribe(ch)

	if snap, err := s.BuildSnapshot(); err == nil {
		b, _ := json.Marshal(snap)
		fmt.Fprintf(w, "event: state\ndata: %s\n\n", b)
		fl.Flush()
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func portOf(addr string, def int) int {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		if p, err := strconv.Atoi(addr[i+1:]); err == nil {
			return p
		}
	}
	return def
}

// localIPv4s lists this computer's LAN addresses (what agents should connect to).
func localIPv4s() []string {
	out := []string{}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip := n.IP.To4(); ip != nil && !ip.IsLinkLocalUnicast() {
					out = append(out, ip.String())
				}
			}
		}
	}
	return out
}
