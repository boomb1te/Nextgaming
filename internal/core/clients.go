package core

import "time"

// Client is a customer record used by the booking system. Name and phone are
// both required but NOT unique: the same person (or name, or number) may appear
// more than once, so each client is its own record.
type Client struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

// ClientGroup is a named set of clients (e.g. a team booked together). A group
// must always have at least one member; beyond that, members are added and
// removed freely.
type ClientGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
	Members   []Client  `json:"members"`
}
