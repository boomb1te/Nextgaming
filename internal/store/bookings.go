package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"nextgaming/internal/core"
)

func bookingPCIDs(q Q, bookingID int64) ([]int64, error) {
	rows, err := q.Query("SELECT pc_id FROM booking_pcs WHERE booking_id=? ORDER BY pc_id", bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func setBookingPCs(q Q, bookingID int64, pcIDs []int64) error {
	if _, err := q.Exec("DELETE FROM booking_pcs WHERE booking_id=?", bookingID); err != nil {
		return err
	}
	for _, pc := range pcIDs {
		if _, err := q.Exec("INSERT OR IGNORE INTO booking_pcs(booking_id,pc_id) VALUES(?,?)", bookingID, pc); err != nil {
			return err
		}
	}
	return nil
}

// CreateBooking inserts a booking and its PCs.
func CreateBooking(q Q, b *core.Booking) error {
	res, err := q.Exec(`INSERT INTO bookings(client_id,group_id,start_at,end_at,note,status,created_by,created_at,started_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		b.ClientID, b.GroupID, ts(b.StartAt), ts(b.EndAt), strings.TrimSpace(b.Note), string(b.Status),
		0, time.Now().Unix(), ts(b.StartedAt))
	if err != nil {
		return err
	}
	if b.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	return setBookingPCs(q, b.ID, b.PCIDs)
}

// UpdateBooking changes a booking's fields and PCs.
func UpdateBooking(q Q, b *core.Booking) error {
	_, err := q.Exec(`UPDATE bookings SET client_id=?,group_id=?,start_at=?,end_at=?,note=? WHERE id=?`,
		b.ClientID, b.GroupID, ts(b.StartAt), ts(b.EndAt), strings.TrimSpace(b.Note), b.ID)
	if err != nil {
		return err
	}
	return setBookingPCs(q, b.ID, b.PCIDs)
}

// SetBookingStatus updates status and (when starting) started_at.
func SetBookingStatus(q Q, id int64, status core.BookingStatus, startedAt time.Time) error {
	_, err := q.Exec("UPDATE bookings SET status=?, started_at=? WHERE id=?", string(status), ts(startedAt), id)
	return err
}

func scanBooking(row interface{ Scan(...any) error }) (core.Booking, error) {
	var b core.Booking
	var start, end, created, started int64
	var status string
	if err := row.Scan(&b.ID, &b.ClientID, &b.GroupID, &start, &end, &b.Note, &status, &created, &started); err != nil {
		return b, err
	}
	b.StartAt, b.EndAt, b.CreatedAt, b.StartedAt = fromTS(start), fromTS(end), fromTS(created), fromTS(started)
	b.Status = core.BookingStatus(status)
	return b, nil
}

const bookingCols = "id,client_id,group_id,start_at,end_at,note,status,created_at,started_at"

// BookingByID returns one booking with its PCs.
func BookingByID(q Q, id int64) (core.Booking, error) {
	b, err := scanBooking(q.QueryRow("SELECT "+bookingCols+" FROM bookings WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	b.PCIDs, err = bookingPCIDs(q, id)
	return b, err
}

func collectBookings(q Q, rows *sql.Rows) ([]core.Booking, error) {
	defer rows.Close()
	out := []core.Booking{}
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		pcs, err := bookingPCIDs(q, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].PCIDs = pcs
	}
	return out, nil
}

// ListBookings returns non-cancelled bookings overlapping [from,to), soonest first.
func ListBookings(q Q, from, to time.Time) ([]core.Booking, error) {
	rows, err := q.Query("SELECT "+bookingCols+` FROM bookings
		WHERE status!='cancelled' AND start_at < ? AND end_at > ? ORDER BY start_at`, ts(to), ts(from))
	if err != nil {
		return nil, err
	}
	return collectBookings(q, rows)
}

// DueBookings returns scheduled bookings whose start time has arrived.
func DueBookings(q Q, now time.Time) ([]core.Booking, error) {
	rows, err := q.Query("SELECT "+bookingCols+` FROM bookings
		WHERE status='scheduled' AND start_at <= ? ORDER BY start_at`, now.Unix())
	if err != nil {
		return nil, err
	}
	return collectBookings(q, rows)
}

// UpcomingBookings returns scheduled bookings starting within [from,to].
func UpcomingBookings(q Q, from, to time.Time) ([]core.Booking, error) {
	rows, err := q.Query("SELECT "+bookingCols+` FROM bookings
		WHERE status='scheduled' AND start_at >= ? AND start_at <= ? ORDER BY start_at`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	return collectBookings(q, rows)
}

// OverlapBookings returns active (scheduled/started) bookings on any of the
// given PCs that overlap [start,end], excluding excludeID. Used for the soft
// conflict warning.
func OverlapBookings(q Q, pcIDs []int64, start, end time.Time, excludeID int64) ([]core.Booking, error) {
	if len(pcIDs) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(pcIDs)), ",")
	args := []any{}
	for _, id := range pcIDs {
		args = append(args, id)
	}
	args = append(args, ts(end), ts(start), excludeID)
	q2 := "SELECT DISTINCT b." + strings.ReplaceAll(bookingCols, ",", ",b.") + ` FROM bookings b
		JOIN booking_pcs p ON p.booking_id=b.id
		WHERE p.pc_id IN (` + ph + `) AND b.status IN ('scheduled','started')
		AND b.start_at < ? AND b.end_at > ? AND b.id != ? ORDER BY b.start_at`
	rows, err := q.Query(q2, args...)
	if err != nil {
		return nil, err
	}
	return collectBookings(q, rows)
}

// SetSessionBooking links a session to the booking that started it.
func SetSessionBooking(q Q, sessionID, bookingID int64) error {
	_, err := q.Exec("UPDATE sessions SET booking_id=? WHERE id=?", bookingID, sessionID)
	return err
}

// OpenSessionsForBooking returns the still-open sessions a booking started.
func OpenSessionsForBooking(q Q, bookingID int64) ([]core.Session, error) {
	rows, err := q.Query("SELECT "+sessCols+" FROM sessions WHERE booking_id=? AND status!='ended'", bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
