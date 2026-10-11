package server

import (
	"crypto/subtle"
	"net/http"
	"sort"
	"sync"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

// Incoming-call prompts (LAN only). The operator phone POSTs the number of an
// answered incoming call to /api/phone/call; the server records a pending call
// and pushes it to the admin panel (via the normal snapshot), where it prompts
// the operator to book for that caller. Pending calls live in memory only —
// they are transient prompts, and losing them on a restart is harmless.

const (
	callKeep = 20        // most pending calls kept at once
	callTTL  = time.Hour // a pending call older than this is dropped
)

type pendingCall struct {
	id     int64
	number string // canonical +993…
	key    string // 8-digit match key
	atMs   int64
}

type calls struct {
	srv   *Server
	mu    sync.Mutex
	seq   int64
	m     map[int64]*pendingCall
	byKey map[string]int64 // match key -> id, so a repeat call from one number coalesces
}

func newCalls(s *Server) *calls {
	return &calls{srv: s, m: map[int64]*pendingCall{}, byKey: map[string]int64{}}
}

// record adds (or, for a repeat call from the same number, refreshes) a pending
// call. It returns false if the number is unusable.
func (c *calls) record(number string) bool {
	canonical, key, ok := core.NormalizeTMPhone(number)
	if !ok {
		return false
	}
	now := time.Now().UnixMilli()
	c.mu.Lock()
	if id, ok := c.byKey[key]; ok && c.m[id] != nil {
		c.m[id].atMs = now // same caller rang again — bump it to the top
	} else {
		c.seq++
		pc := &pendingCall{id: c.seq, number: canonical, key: key, atMs: now}
		c.m[pc.id] = pc
		c.byKey[key] = pc.id
		c.prune()
	}
	c.mu.Unlock()
	c.srv.changed()
	return true
}

// dismiss removes a pending call (the operator booked or declined it).
func (c *calls) dismiss(id int64) {
	c.mu.Lock()
	if pc := c.m[id]; pc != nil {
		delete(c.m, id)
		if c.byKey[pc.key] == id {
			delete(c.byKey, pc.key)
		}
	}
	c.mu.Unlock()
	c.srv.changed()
}

// list returns the current pending calls, newest first, dropping expired ones.
func (c *calls) list() []*pendingCall {
	cutoff := time.Now().Add(-callTTL).UnixMilli()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*pendingCall, 0, len(c.m))
	for id, pc := range c.m {
		if pc.atMs < cutoff {
			delete(c.m, id)
			if c.byKey[pc.key] == id {
				delete(c.byKey, pc.key)
			}
			continue
		}
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].atMs > out[j].atMs })
	return out
}

// prune caps the number of pending calls, dropping the oldest. Caller holds mu.
func (c *calls) prune() {
	if len(c.m) <= callKeep {
		return
	}
	var oldest *pendingCall
	for _, pc := range c.m {
		if oldest == nil || pc.atMs < oldest.atMs {
			oldest = pc
		}
	}
	if oldest != nil {
		delete(c.m, oldest.id)
		if c.byKey[oldest.key] == oldest.id {
			delete(c.byKey, oldest.key)
		}
	}
}

// PendingCallView is a pending call as the admin panel sees it; the client match
// is resolved live, so an "unknown" caller becomes known the moment the operator
// adds them.
type PendingCallView struct {
	ID         int64     `json:"id"`
	Number     string    `json:"number"`
	At         time.Time `json:"at"`
	ClientID   int64     `json:"clientId"`   // 0 when the caller is not a client
	ClientName string    `json:"clientName"` // "" when unknown
}

// pendingCallViews resolves each pending call against the current client list.
func (s *Server) pendingCallViews() []PendingCallView {
	pcs := s.calls.list()
	out := make([]PendingCallView, 0, len(pcs))
	for _, pc := range pcs {
		v := PendingCallView{ID: pc.id, Number: pc.number, At: time.UnixMilli(pc.atMs)}
		if cl, found, err := store.ClientByPhoneKey(s.st.DB, pc.key); err == nil && found {
			v.ClientID, v.ClientName = cl.ID, cl.Name
		}
		out = append(out, v)
	}
	return out
}

// RecordIncomingCall registers an answered incoming call for the operator to act on.
func (s *Server) RecordIncomingCall(number string) error {
	if !s.calls.record(number) {
		return errBad("not a usable phone number")
	}
	return nil
}

// DismissCall clears a pending call once the operator has handled it.
func (s *Server) DismissCall(id int64) { s.calls.dismiss(id) }

// phoneAuthed authorises the operator phone by the club key (LAN only, no cookie).
func (s *Server) phoneAuthed(r *http.Request) bool {
	key := r.Header.Get("X-NextGaming-Phone")
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.ClubKey)) == 1
}

// handlePhoneCall receives an answered-incoming-call number from the operator
// phone. The number may come as a ?number= query param or a JSON body, so the
// phone-side automation (MacroDroid) can send it whichever way is easiest.
func (s *Server) handlePhoneCall(w http.ResponseWriter, r *http.Request) {
	if !s.phoneAuthed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	number := r.URL.Query().Get("number")
	if number == "" {
		var req struct {
			Number string `json:"number"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, err)
			return
		}
		number = req.Number
	}
	if err := s.RecordIncomingCall(number); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ok())
}
