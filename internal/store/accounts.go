package store

import (
	"database/sql"
	"errors"
	"time"
)

// A client_accounts row makes a client an "authorized" member: it holds the
// (hashed) login password and when/by whom the client was registered. One row
// per client (client_id is the primary key); no row = basic client.

// Account is the membership record for a client (password hash excluded).
type Account struct {
	ClientID     int64     `json:"clientId"`
	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registeredAt"`
	RegisteredBy int64     `json:"registeredBy"`
}

// CreateAccount makes a client a member. It fails if one already exists.
func CreateAccount(q Q, clientID int64, passwordHash string, by int64, now time.Time) error {
	_, err := q.Exec("INSERT INTO client_accounts(client_id,password_hash,status,registered_at,registered_by) VALUES(?,?,?,?,?)",
		clientID, passwordHash, "active", now.Unix(), by)
	return err
}

// SetAccountPassword replaces a member's password hash (password reset).
func SetAccountPassword(q Q, clientID int64, passwordHash string) error {
	_, err := q.Exec("UPDATE client_accounts SET password_hash=? WHERE client_id=?", passwordHash, clientID)
	return err
}

// DeleteAccount removes membership entirely (de-authorize). The client stays.
func DeleteAccount(q Q, clientID int64) error {
	_, err := q.Exec("DELETE FROM client_accounts WHERE client_id=?", clientID)
	return err
}

// AccountByClientID returns a client's membership and its stored password hash.
func AccountByClientID(q Q, clientID int64) (acc Account, hash string, found bool, err error) {
	row := q.QueryRow("SELECT client_id,password_hash,status,registered_at,registered_by FROM client_accounts WHERE client_id=?", clientID)
	var reg int64
	err = row.Scan(&acc.ClientID, &hash, &acc.Status, &reg, &acc.RegisteredBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, "", false, nil
	}
	if err != nil {
		return Account{}, "", false, err
	}
	acc.RegisteredAt = fromTS(reg)
	return acc, hash, true, nil
}

// AllAccounts returns every membership keyed by client id (for list views).
func AllAccounts(q Q) (map[int64]Account, error) {
	rows, err := q.Query("SELECT client_id,status,registered_at,registered_by FROM client_accounts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Account{}
	for rows.Next() {
		var a Account
		var reg int64
		if err := rows.Scan(&a.ClientID, &a.Status, &reg, &a.RegisteredBy); err != nil {
			return nil, err
		}
		a.RegisteredAt = fromTS(reg)
		out[a.ClientID] = a
	}
	return out, rows.Err()
}

// ClientHoursSeconds returns, per client id, total active (non-paused) seconds
// across every session attached to that client — the basis for "hours played".
// Open sessions count up to now. Paused time is excluded via paused_seconds
// (a live pause's in-progress seconds are not subtracted, a minor display-only
// approximation). Only sessions with a real attached client are summed.
func ClientHoursSeconds(q Q, now time.Time) (map[int64]int64, error) {
	rows, err := q.Query(`SELECT customer_id,
		SUM( (CASE WHEN ended_at>0 THEN ended_at ELSE ? END) - started_at - paused_seconds )
		FROM sessions WHERE customer_id IS NOT NULL AND customer_id>0
		GROUP BY customer_id`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, secs int64
		if err := rows.Scan(&id, &secs); err != nil {
			return nil, err
		}
		if secs < 0 {
			secs = 0
		}
		out[id] = secs
	}
	return out, rows.Err()
}
