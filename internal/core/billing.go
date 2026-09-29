package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Errors returned by session operations.
var (
	ErrSessionEnded   = errors.New("session has already ended")
	ErrNotPrepaid     = errors.New("only prepaid sessions have a time allotment")
	ErrAlreadyPaused  = errors.New("session is already paused")
	ErrNotPaused      = errors.New("session is not paused")
	ErrBadMinutes     = errors.New("minutes must be greater than zero")
	ErrBadAmount      = errors.New("amount must not be negative")
	ErrRefundTooLarge = errors.New("refund is larger than what was paid")
)

// ActiveSeconds is how long the session has actually been running,
// excluding paused time.
func (s *Session) ActiveSeconds(now time.Time) int64 {
	end := now
	if s.EndedAt != nil {
		end = *s.EndedAt
	}
	total := int64(end.Sub(s.StartedAt) / time.Second)
	paused := s.PausedSeconds
	if s.PausedAt != nil && s.EndedAt == nil {
		paused += int64(now.Sub(*s.PausedAt) / time.Second)
	}
	active := total - paused
	if active < 0 {
		return 0
	}
	return active
}

// RemainingSeconds is the prepaid time left. It can be negative for a
// session that expired while the server was not looking; callers clamp it.
// Only meaningful for prepaid sessions.
func (s *Session) RemainingSeconds(now time.Time) int64 {
	if s.Mode != ModePrepaid {
		return 0
	}
	return s.PaidSeconds - s.ActiveSeconds(now)
}

// Expired reports whether a running prepaid session has used up its time.
func (s *Session) Expired(now time.Time) bool {
	return s.Mode == ModePrepaid && s.Status == StatusActive && s.RemainingSeconds(now) <= 0
}

// Pause stops the clock. The PC locks while paused.
func (s *Session) Pause(now time.Time) error {
	switch s.Status {
	case StatusEnded:
		return ErrSessionEnded
	case StatusPaused:
		return ErrAlreadyPaused
	}
	t := now
	s.PausedAt = &t
	s.Status = StatusPaused
	return nil
}

// Resume restarts the clock after a pause.
func (s *Session) Resume(now time.Time) error {
	switch s.Status {
	case StatusEnded:
		return ErrSessionEnded
	case StatusActive:
		return ErrNotPaused
	}
	if s.PausedAt != nil {
		s.PausedSeconds += int64(now.Sub(*s.PausedAt) / time.Second)
	}
	s.PausedAt = nil
	s.Status = StatusActive
	return nil
}

// AddPrepaidTime extends a prepaid session.
func (s *Session) AddPrepaidTime(minutes int, price Money) error {
	if s.Status == StatusEnded {
		return ErrSessionEnded
	}
	if s.Mode != ModePrepaid {
		return ErrNotPrepaid
	}
	if minutes <= 0 {
		return ErrBadMinutes
	}
	if price < 0 {
		return ErrBadAmount
	}
	s.PaidSeconds += int64(minutes) * 60
	s.Paid += price
	return nil
}

// End closes the session. If it was paused, the pause is folded in first
// so paused time is never billed.
func (s *Session) End(now time.Time, reason string) error {
	if s.Status == StatusEnded {
		return ErrSessionEnded
	}
	if s.Status == StatusPaused && s.PausedAt != nil {
		s.PausedSeconds += int64(now.Sub(*s.PausedAt) / time.Second)
		s.PausedAt = nil
	}
	t := now
	s.EndedAt = &t
	s.Status = StatusEnded
	s.EndReason = reason
	return nil
}

// PriceForMinutes converts playing time into money at an hourly rate,
// rounded to the nearest minor unit.
func PriceForMinutes(ratePerHour Money, minutes int) Money {
	if minutes <= 0 || ratePerHour <= 0 {
		return 0
	}
	// (rate * minutes + 30) / 60 rounds half up.
	return Money((int64(ratePerHour)*int64(minutes) + 30) / 60)
}

// OpenCharge is what an open (pay-at-the-end) session costs.
// Every started minute is billed. Sessions shorter than the grace period are free;
// otherwise at least minChargeMinutes is billed.
func OpenCharge(ratePerHour Money, activeSeconds int64, graceSeconds, minChargeMinutes int) Money {
	if activeSeconds <= int64(graceSeconds) {
		return 0
	}
	minutes := int((activeSeconds + 59) / 60) // round up to the started minute
	if minutes < minChargeMinutes {
		minutes = minChargeMinutes
	}
	return PriceForMinutes(ratePerHour, minutes)
}

// SuggestedRefund is the share of what was paid that corresponds to unused
// prepaid time. The admin can accept or change it.
func SuggestedRefund(paid Money, paidSeconds, remainingSeconds int64) Money {
	if paid <= 0 || paidSeconds <= 0 || remainingSeconds <= 0 {
		return 0
	}
	if remainingSeconds > paidSeconds {
		remainingSeconds = paidSeconds
	}
	// Round down: never suggest giving back more than the proportional amount.
	return Money(int64(paid) * remainingSeconds / paidSeconds)
}

// ValidateRefund checks a refund amount against what the session has taken.
func ValidateRefund(paid, refund Money) error {
	if refund < 0 {
		return ErrBadAmount
	}
	if refund > paid {
		return ErrRefundTooLarge
	}
	return nil
}

// PackageMinutes is how long a package lasts if bought at `now`.
// Packages with EndsAtHour run until the next occurrence of that clock hour.
func PackageMinutes(p Package, now time.Time) int {
	if p.EndsAtHour < 0 || p.EndsAtHour > 23 {
		return p.Minutes
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), p.EndsAtHour, 0, 0, 0, now.Location())
	if !end.After(now) {
		end = end.Add(24 * time.Hour)
	}
	return int(end.Sub(now) / time.Minute)
}

// PackageAvailable reports whether the package may be sold at `now`.
func PackageAvailable(p Package, now time.Time) bool {
	if !p.Active {
		return false
	}
	if p.AvailableFrom < 0 || p.AvailableTo < 0 || p.AvailableFrom == p.AvailableTo {
		return true
	}
	h := now.Hour()
	if p.AvailableFrom < p.AvailableTo {
		return h >= p.AvailableFrom && h < p.AvailableTo
	}
	// Window wraps midnight, e.g. 22 -> 6.
	return h >= p.AvailableFrom || h < p.AvailableTo
}

// PackageFitsZone reports whether a package can be used on a PC in zoneID.
func PackageFitsZone(p Package, zoneID int64) bool {
	return p.ZoneID == 0 || p.ZoneID == zoneID
}

// FormatMoney renders minor units as "12.50".
func FormatMoney(m Money) string {
	sign := ""
	if m < 0 {
		sign = "-"
		m = -m
	}
	return fmt.Sprintf("%s%d.%02d", sign, m/100, m%100)
}

// ParseMoney parses "12.5", "12.50" or "12" into minor units.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, errors.New("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("too many decimal places in %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	v := Money(w*100 + f)
	if neg {
		v = -v
	}
	return v, nil
}

// FormatDuration renders seconds as H:MM:SS.
func FormatDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, (sec%3600)/60, sec%60)
}
