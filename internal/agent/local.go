package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
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
