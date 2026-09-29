package core

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

func prepaid(minutes int, paid Money) *Session {
	return &Session{Mode: ModePrepaid, Status: StatusActive, StartedAt: t0,
		PaidSeconds: int64(minutes) * 60, Paid: paid, RatePerHour: 1000}
}

func TestRemainingCountsDown(t *testing.T) {
	s := prepaid(60, 1000)
	if got := s.RemainingSeconds(at(10 * time.Minute)); got != 50*60 {
		t.Fatalf("remaining = %d, want %d", got, 50*60)
	}
	if s.Expired(at(59 * time.Minute)) {
		t.Fatal("should not be expired at 59 min")
	}
	if !s.Expired(at(60 * time.Minute)) {
		t.Fatal("should be expired at 60 min")
	}
}

func TestPauseStopsTheClock(t *testing.T) {
	s := prepaid(60, 1000)
	if err := s.Pause(at(10 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 30 minutes pass while paused: remaining must not change.
	if got := s.RemainingSeconds(at(40 * time.Minute)); got != 50*60 {
		t.Fatalf("remaining while paused = %d, want %d", got, 50*60)
	}
	if s.Expired(at(2 * time.Hour)) {
		t.Fatal("paused session must never expire")
	}
	if err := s.Pause(at(41 * time.Minute)); err != ErrAlreadyPaused {
		t.Fatalf("double pause err = %v", err)
	}
	if err := s.Resume(at(40 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := s.RemainingSeconds(at(50 * time.Minute)); got != 40*60 {
		t.Fatalf("remaining after resume = %d, want %d", got, 40*60)
	}
	if err := s.Resume(at(51 * time.Minute)); err != ErrNotPaused {
		t.Fatalf("resume active err = %v", err)
	}
}

func TestEndWhilePausedDoesNotBillPause(t *testing.T) {
	s := &Session{Mode: ModeOpen, Status: StatusActive, StartedAt: t0}
	_ = s.Pause(at(20 * time.Minute))
	_ = s.End(at(80*time.Minute), "admin")
	if got := s.ActiveSeconds(at(5 * time.Hour)); got != 20*60 {
		t.Fatalf("active = %d, want %d", got, 20*60)
	}
	if err := s.End(at(90*time.Minute), "again"); err != ErrSessionEnded {
		t.Fatalf("double end err = %v", err)
	}
}

func TestAddPrepaidTime(t *testing.T) {
	s := prepaid(30, 500)
	if err := s.AddPrepaidTime(30, 500); err != nil {
		t.Fatal(err)
	}
	if s.PaidSeconds != 3600 || s.Paid != 1000 {
		t.Fatalf("paidSeconds=%d paid=%d", s.PaidSeconds, s.Paid)
	}
	if err := s.AddPrepaidTime(0, 100); err != ErrBadMinutes {
		t.Fatalf("zero minutes err = %v", err)
	}
	open := &Session{Mode: ModeOpen, Status: StatusActive}
	if err := open.AddPrepaidTime(10, 100); err != ErrNotPrepaid {
		t.Fatalf("open add time err = %v", err)
	}
}

func TestPriceForMinutes(t *testing.T) {
	cases := []struct {
		rate Money
		min  int
		want Money
	}{
		{1000, 60, 1000},
		{1000, 30, 500},
		{1000, 1, 17}, // 16.67 rounds to 17
		{1000, 90, 1500},
		{0, 60, 0},
		{1000, 0, 0},
	}
	for _, c := range cases {
		if got := PriceForMinutes(c.rate, c.min); got != c.want {
			t.Errorf("PriceForMinutes(%d,%d)=%d want %d", c.rate, c.min, got, c.want)
		}
	}
}

func TestOpenCharge(t *testing.T) {
	// 10.00 per hour, 60s grace, no minimum.
	if got := OpenCharge(1000, 45, 60, 0); got != 0 {
		t.Errorf("inside grace = %d, want 0", got)
	}
	if got := OpenCharge(1000, 61, 60, 0); got != 33 { // 2 started minutes
		t.Errorf("61s = %d, want 33", got)
	}
	if got := OpenCharge(1000, 3600, 60, 0); got != 1000 {
		t.Errorf("1h = %d, want 1000", got)
	}
	if got := OpenCharge(1000, 120, 0, 30); got != 500 { // minimum 30 min
		t.Errorf("min charge = %d, want 500", got)
	}
}

func TestSuggestedRefund(t *testing.T) {
	if got := SuggestedRefund(1000, 3600, 1800); got != 500 {
		t.Errorf("half unused = %d, want 500", got)
	}
	if got := SuggestedRefund(1000, 3600, -5); got != 0 {
		t.Errorf("expired = %d, want 0", got)
	}
	if got := SuggestedRefund(1000, 3600, 9999); got != 1000 {
		t.Errorf("clamped = %d, want 1000", got)
	}
	if err := ValidateRefund(1000, 1001); err != ErrRefundTooLarge {
		t.Errorf("too large err = %v", err)
	}
	if err := ValidateRefund(1000, -1); err != ErrBadAmount {
		t.Errorf("negative err = %v", err)
	}
}

func TestPackageMinutesNightPack(t *testing.T) {
	night := Package{EndsAtHour: 8, Minutes: 0}
	buy := time.Date(2026, 9, 29, 23, 10, 0, 0, time.UTC)
	if got := PackageMinutes(night, buy); got != 8*60+50 {
		t.Errorf("night pack from 23:10 = %d, want %d", got, 8*60+50)
	}
	early := time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC)
	if got := PackageMinutes(night, early); got != 6*60 {
		t.Errorf("night pack from 02:00 = %d, want %d", got, 6*60)
	}
	fixed := Package{EndsAtHour: -1, Minutes: 180}
	if got := PackageMinutes(fixed, buy); got != 180 {
		t.Errorf("fixed = %d", got)
	}
}

func TestPackageAvailable(t *testing.T) {
	night := Package{Active: true, AvailableFrom: 22, AvailableTo: 6}
	for h, want := range map[int]bool{21: false, 22: true, 23: true, 0: true, 5: true, 6: false, 12: false} {
		now := time.Date(2026, 9, 29, h, 30, 0, 0, time.UTC)
		if got := PackageAvailable(night, now); got != want {
			t.Errorf("hour %d: got %v want %v", h, got, want)
		}
	}
	always := Package{Active: true, AvailableFrom: -1, AvailableTo: -1}
	if !PackageAvailable(always, t0) {
		t.Error("always-available package reported unavailable")
	}
	inactive := Package{Active: false, AvailableFrom: -1, AvailableTo: -1}
	if PackageAvailable(inactive, t0) {
		t.Error("inactive package reported available")
	}
}

func TestMoneyFormatting(t *testing.T) {
	for in, want := range map[string]Money{"12": 1200, "12.5": 1250, "12,50": 1250, "0.05": 5, "-3.10": -310, ".5": 50} {
		got, err := ParseMoney(in)
		if err != nil || got != want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.234"} {
		if _, err := ParseMoney(bad); err == nil {
			t.Errorf("ParseMoney(%q) should fail", bad)
		}
	}
	if got := FormatMoney(-1250); got != "-12.50" {
		t.Errorf("FormatMoney = %q", got)
	}
	if got := FormatDuration(3725); got != "1:02:05" {
		t.Errorf("FormatDuration = %q", got)
	}
}
