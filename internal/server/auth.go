package server

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const pbkdf2Iterations = 120_000

// HashPassword returns "pbkdf2-sha256$iter$salt$hash".
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies a password against a stored hash in constant time.
func CheckPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidatePassword enforces a minimal password policy.
func ValidatePassword(p string) error {
	if len(p) < 6 {
		return errors.New("password must be at least 6 characters")
	}
	return nil
}

// loginSession is one logged-in browser.
type loginSession struct {
	adminID int64
	expires time.Time
}

// Sessions keeps admin logins in memory. A server restart logs everyone out,
// which is acceptable for a front-desk tool.
type Sessions struct {
	mu       sync.Mutex
	byToken  map[string]*loginSession
	failures map[string][]time.Time // per client IP, for login throttling
}

const loginTTL = 12 * time.Hour

func newSessions() *Sessions {
	return &Sessions{byToken: map[string]*loginSession{}, failures: map[string][]time.Time{}}
}

// Create returns a new random token for adminID.
func (s *Sessions) Create(adminID int64) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.byToken[tok] = &loginSession{adminID: adminID, expires: time.Now().Add(loginTTL)}
	s.mu.Unlock()
	return tok
}

// Lookup returns the admin for a token, extending its lifetime (sliding expiry).
func (s *Sessions) Lookup(tok string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls, ok := s.byToken[tok]
	if !ok {
		return 0, false
	}
	if time.Now().After(ls.expires) {
		delete(s.byToken, tok)
		return 0, false
	}
	ls.expires = time.Now().Add(loginTTL)
	return ls.adminID, true
}

// Delete logs a token out.
func (s *Sessions) Delete(tok string) {
	s.mu.Lock()
	delete(s.byToken, tok)
	s.mu.Unlock()
}

// DeleteAdmin logs out every browser of one admin.
func (s *Sessions) DeleteAdmin(adminID int64) {
	s.mu.Lock()
	for t, ls := range s.byToken {
		if ls.adminID == adminID {
			delete(s.byToken, t)
		}
	}
	s.mu.Unlock()
}

// Throttled reports whether ip has failed too often recently (5 in 5 minutes).
func (s *Sessions) Throttled(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-5 * time.Minute)
	recent := s.failures[ip][:0]
	for _, t := range s.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.failures[ip] = recent
	return len(recent) >= 5
}

// Failed records a failed login.
func (s *Sessions) Failed(ip string) {
	s.mu.Lock()
	s.failures[ip] = append(s.failures[ip], time.Now())
	s.mu.Unlock()
}

// Succeeded clears failures.
func (s *Sessions) Succeeded(ip string) {
	s.mu.Lock()
	delete(s.failures, ip)
	s.mu.Unlock()
}
