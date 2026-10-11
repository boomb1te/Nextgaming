package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// serveLocal starts the loopback-only API used by the lock screen.
func (a *Agent) serveLocal(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", a.cfg.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("local API %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.ClientState())
	})
	mux.HandleFunc("POST /staff-login", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NextGaming-Client") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if err := a.StaffLogin(req.Username, req.Password); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	// Screen monitoring. The lock screen long-polls /capture-wait; when the
	// server asks for a frame the poll returns the size/quality to use. The
	// lock screen grabs the screen and posts the JPEG to /screen.
	mux.HandleFunc("GET /capture-wait", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NextGaming-Client") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		a.capMu.Lock()
		wake := a.capWake
		a.capMu.Unlock()
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		case <-time.After(30 * time.Second):
			w.WriteHeader(http.StatusNoContent) // no request; the client polls again
			return
		}
		a.capMu.Lock()
		resp := map[string]int{"seq": int(a.capSeq), "width": a.capW, "quality": a.capQ}
		a.capMu.Unlock()
		writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("POST /screen", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NextGaming-Client") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 6<<20))
		if err != nil || len(data) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		cw, _ := strconv.Atoi(r.URL.Query().Get("w"))
		ch, _ := strconv.Atoi(r.URL.Query().Get("h"))
		a.sendScreen(data, cw, ch)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		// A custom header cannot be sent by a web page without a CORS
		// preflight (which we never answer), so browsers cannot call this.
		if r.Header.Get("X-NextGaming-Client") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		var req struct {
			SessionID int64 `json:"sessionId"`
		}
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if err := a.PlayerLogout(req.SessionID); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	srv := &http.Server{Handler: loopbackOnly(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("local API: %v", err)
		}
	}()
	return nil
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
