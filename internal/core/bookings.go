package core

import "time"

// BookingStatus is where a booking is in its life.
type BookingStatus string

const (
	BookingScheduled BookingStatus = "scheduled" // created, not yet started
	BookingStarted   BookingStatus = "started"   // auto-start fired, sessions running
	BookingCancelled BookingStatus = "cancelled" // called off before/while scheduled
	BookingNoShow    BookingStatus = "noshow"    // released because nobody came
)

// Booking reserves one or more PCs for a client or a group for a time window.
// Exactly one of ClientID / GroupID is set.
type Booking struct {
	ID        int64         `json:"id"`
	ClientID  int64         `json:"clientId"`
	GroupID   int64         `json:"groupId"`
	StartAt   time.Time     `json:"startAt"`
	EndAt     time.Time     `json:"endAt"`
	Note      string        `json:"note"`
	Status    BookingStatus `json:"status"`
	PCIDs     []int64       `json:"pcIds"`
	CreatedAt time.Time     `json:"createdAt"`
	StartedAt time.Time     `json:"startedAt"`
}
