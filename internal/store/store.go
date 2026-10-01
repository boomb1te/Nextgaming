// Package store persists NextGaming data in a single SQLite file.
//
// SQLite is embedded (pure Go, no DLL to ship). Every money movement is written
// in the same database transaction as the session change that caused it, so a
// crash can never leave a session and the cash log disagreeing.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"nextgaming/internal/core"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Q is satisfied by both *sql.DB and *sql.Tx, so every query helper works
// inside or outside a transaction.
type Q interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Store is the database handle.
type Store struct {
	DB   *sql.DB
	path string
}

// Open opens (and if needed creates and migrates) the database file.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=synchronous(full)"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite simple and avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Tx runs fn inside a transaction, committing on success.
func (s *Store) Tx(fn func(q Q) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admins (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL,
	disabled      INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS zones (
	id          INTEGER PRIMARY KEY,
	name        TEXT NOT NULL,
	hourly_rate INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS packages (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL,
	zone_id        INTEGER NOT NULL DEFAULT 0,
	minutes        INTEGER NOT NULL DEFAULT 0,
	price          INTEGER NOT NULL,
	ends_at_hour   INTEGER NOT NULL DEFAULT -1,
	available_from INTEGER NOT NULL DEFAULT -1,
	available_to   INTEGER NOT NULL DEFAULT -1,
	active         INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS pcs (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL,
	zone_id        INTEGER NOT NULL DEFAULT 0,
	sort_order     INTEGER NOT NULL DEFAULT 0,
	machine_id     TEXT NOT NULL UNIQUE,
	mac            TEXT NOT NULL DEFAULT '',
	ip             TEXT NOT NULL DEFAULT '',
	hostname       TEXT NOT NULL DEFAULT '',
	board_maker    TEXT NOT NULL DEFAULT '',
	board_product  TEXT NOT NULL DEFAULT '',
	agent_version  TEXT NOT NULL DEFAULT '',
	wol_status     TEXT NOT NULL DEFAULT 'unknown',
	wol_detail     TEXT NOT NULL DEFAULT '',
	wol_checked_at INTEGER NOT NULL DEFAULT 0,
	wol_tested_at  INTEGER NOT NULL DEFAULT 0,
	wol_report     TEXT NOT NULL DEFAULT '',
	created_at     INTEGER NOT NULL,
	deleted        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS shifts (
	id           INTEGER PRIMARY KEY,
	admin_id     INTEGER NOT NULL,
	opened_at    INTEGER NOT NULL,
	closed_at    INTEGER,
	opening_cash INTEGER NOT NULL DEFAULT 0,
	closing_cash INTEGER NOT NULL DEFAULT 0,
	expected     INTEGER NOT NULL DEFAULT 0,
	note         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sessions (
	id             INTEGER PRIMARY KEY,
	pc_id          INTEGER NOT NULL,
	mode           TEXT NOT NULL,
	status         TEXT NOT NULL,
	started_at     INTEGER NOT NULL,
	ended_at       INTEGER,
	paused_at      INTEGER,
	paused_seconds INTEGER NOT NULL DEFAULT 0,
	paid_seconds   INTEGER NOT NULL DEFAULT 0,
	rate_per_hour  INTEGER NOT NULL DEFAULT 0,
	tariff_name    TEXT NOT NULL DEFAULT '',
	paid           INTEGER NOT NULL DEFAULT 0,
	started_by     INTEGER NOT NULL DEFAULT 0,
	ended_by       INTEGER NOT NULL DEFAULT 0,
	shift_id       INTEGER NOT NULL DEFAULT 0,
	customer_id    INTEGER,
	end_reason     TEXT NOT NULL DEFAULT '',
	note           TEXT NOT NULL DEFAULT '',
	checkout_requested INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at);
CREATE TABLE IF NOT EXISTS transactions (
	id         INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL DEFAULT 0,
	pc_id      INTEGER NOT NULL DEFAULT 0,
	shift_id   INTEGER NOT NULL DEFAULT 0,
	admin_id   INTEGER NOT NULL DEFAULT 0,
	kind       TEXT NOT NULL,
	amount     INTEGER NOT NULL,
	reason     TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tx_created ON transactions(created_at);
CREATE INDEX IF NOT EXISTS idx_tx_shift ON transactions(shift_id);
CREATE TABLE IF NOT EXISTS audit (
	id         INTEGER PRIMARY KEY,
	admin_id   INTEGER NOT NULL DEFAULT 0,
	action     TEXT NOT NULL,
	pc_id      INTEGER NOT NULL DEFAULT 0,
	details    TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit(created_at);
-- Money must never be silently edited: block UPDATE/DELETE on the cash log.
CREATE TRIGGER IF NOT EXISTS tx_no_update BEFORE UPDATE ON transactions
BEGIN SELECT RAISE(ABORT, 'transactions are append-only'); END;
CREATE TRIGGER IF NOT EXISTS tx_no_delete BEFORE DELETE ON transactions
BEGIN SELECT RAISE(ABORT, 'transactions are append-only'); END;
`

func (s *Store) migrate() error {
	var v int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v < 1 {
		if _, err := s.DB.Exec(schemaV1); err != nil {
			return err
		}
		if _, err := s.DB.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
		v = 1
	}
	if v < 2 {
		// v2: remember when a PC was removed, so only a reinstalled agent can bring it back.
		if _, err := s.DB.Exec("ALTER TABLE pcs ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
		if _, err := s.DB.Exec("PRAGMA user_version = 2"); err != nil {
			return err
		}
	}
	return nil
}

// ---- time helpers (stored as unix seconds, UTC) ----

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func tsp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func fromTS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

func fromNull(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- key/value ----

// GetKV reads a value; ok=false if missing.
func GetKV(q Q, key string) (string, bool, error) {
	var v string
	err := q.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetKV writes a value.
func SetKV(q Q, key, value string) error {
	_, err := q.Exec("INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// LoadSettings returns stored settings merged over defaults.
func LoadSettings(q Q) (core.Settings, error) {
	st := core.DefaultSettings()
	v, ok, err := GetKV(q, "settings")
	if err != nil || !ok {
		return st, err
	}
	err = json.Unmarshal([]byte(v), &st)
	return st, err
}

// SaveSettings stores settings.
func SaveSettings(q Q, st core.Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return SetKV(q, "settings", string(b))
}

// ---- admins ----

// AdminCount returns the number of admin accounts.
func AdminCount(q Q) (int, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM admins").Scan(&n)
	return n, err
}

// CreateAdmin inserts an admin.
func CreateAdmin(q Q, username, hash, role string) (int64, error) {
	res, err := q.Exec("INSERT INTO admins(username,password_hash,role,created_at) VALUES(?,?,?,?)",
		strings.TrimSpace(username), hash, role, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("username already exists")
		}
		return 0, err
	}
	return res.LastInsertId()
}

// AdminByUsername returns an admin and its password hash.
func AdminByUsername(q Q, username string) (core.Admin, string, error) {
	var a core.Admin
	var hash string
	var created int64
	var disabled int
	err := q.QueryRow("SELECT id,username,password_hash,role,disabled,created_at FROM admins WHERE username=?",
		strings.TrimSpace(username)).Scan(&a.ID, &a.Username, &hash, &a.Role, &disabled, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, "", ErrNotFound
	}
	a.Disabled = disabled == 1
	a.CreatedAt = fromTS(created)
	return a, hash, err
}

// AdminByID returns an admin.
func AdminByID(q Q, id int64) (core.Admin, error) {
	var a core.Admin
	var created int64
	var disabled int
	err := q.QueryRow("SELECT id,username,role,disabled,created_at FROM admins WHERE id=?", id).
		Scan(&a.ID, &a.Username, &a.Role, &disabled, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Disabled = disabled == 1
	a.CreatedAt = fromTS(created)
	return a, err
}

// ListAdmins returns all admins.
func ListAdmins(q Q) ([]core.Admin, error) {
	rows, err := q.Query("SELECT id,username,role,disabled,created_at FROM admins ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Admin
	for rows.Next() {
		var a core.Admin
		var created int64
		var disabled int
		if err := rows.Scan(&a.ID, &a.Username, &a.Role, &disabled, &created); err != nil {
			return nil, err
		}
		a.Disabled = disabled == 1
		a.CreatedAt = fromTS(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAdminPassword replaces a password hash.
func SetAdminPassword(q Q, id int64, hash string) error {
	_, err := q.Exec("UPDATE admins SET password_hash=? WHERE id=?", hash, id)
	return err
}

// SetAdminDisabled enables or disables a login.
func SetAdminDisabled(q Q, id int64, disabled bool) error {
	_, err := q.Exec("UPDATE admins SET disabled=? WHERE id=?", b2i(disabled), id)
	return err
}

// ---- zones ----

// ListZones returns all zones.
func ListZones(q Q) ([]core.Zone, error) {
	rows, err := q.Query("SELECT id,name,hourly_rate FROM zones ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Zone{}
	for rows.Next() {
		var z core.Zone
		if err := rows.Scan(&z.ID, &z.Name, &z.HourlyRate); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// ZoneByID returns one zone.
func ZoneByID(q Q, id int64) (core.Zone, error) {
	var z core.Zone
	err := q.QueryRow("SELECT id,name,hourly_rate FROM zones WHERE id=?", id).Scan(&z.ID, &z.Name, &z.HourlyRate)
	if errors.Is(err, sql.ErrNoRows) {
		return z, ErrNotFound
	}
	return z, err
}

// SaveZone inserts (ID==0) or updates a zone.
func SaveZone(q Q, z core.Zone) (int64, error) {
	if z.ID == 0 {
		res, err := q.Exec("INSERT INTO zones(name,hourly_rate) VALUES(?,?)", z.Name, z.HourlyRate)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := q.Exec("UPDATE zones SET name=?,hourly_rate=? WHERE id=?", z.Name, z.HourlyRate, z.ID)
	return z.ID, err
}

// DeleteZone removes a zone if no PC uses it.
func DeleteZone(q Q, id int64) error {
	var n int
	if err := q.QueryRow("SELECT COUNT(*) FROM pcs WHERE zone_id=? AND deleted=0", id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("zone is used by %d PC(s); move them first", n)
	}
	_, err := q.Exec("DELETE FROM zones WHERE id=?", id)
	return err
}

// ---- packages ----

const pkgCols = "id,name,zone_id,minutes,price,ends_at_hour,available_from,available_to,active"

func scanPkg(sc interface{ Scan(...any) error }) (core.Package, error) {
	var p core.Package
	var active int
	err := sc.Scan(&p.ID, &p.Name, &p.ZoneID, &p.Minutes, &p.Price, &p.EndsAtHour, &p.AvailableFrom, &p.AvailableTo, &active)
	p.Active = active == 1
	return p, err
}

// ListPackages returns all packages.
func ListPackages(q Q) ([]core.Package, error) {
	rows, err := q.Query("SELECT " + pkgCols + " FROM packages ORDER BY price")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Package{}
	for rows.Next() {
		p, err := scanPkg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PackageByID returns one package.
func PackageByID(q Q, id int64) (core.Package, error) {
	p, err := scanPkg(q.QueryRow("SELECT "+pkgCols+" FROM packages WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// SavePackage inserts (ID==0) or updates a package.
func SavePackage(q Q, p core.Package) (int64, error) {
	if p.ID == 0 {
		res, err := q.Exec("INSERT INTO packages(name,zone_id,minutes,price,ends_at_hour,available_from,available_to,active) VALUES(?,?,?,?,?,?,?,?)",
			p.Name, p.ZoneID, p.Minutes, p.Price, p.EndsAtHour, p.AvailableFrom, p.AvailableTo, b2i(p.Active))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := q.Exec("UPDATE packages SET name=?,zone_id=?,minutes=?,price=?,ends_at_hour=?,available_from=?,available_to=?,active=? WHERE id=?",
		p.Name, p.ZoneID, p.Minutes, p.Price, p.EndsAtHour, p.AvailableFrom, p.AvailableTo, b2i(p.Active), p.ID)
	return p.ID, err
}

// DeletePackage removes a package (past sessions keep the tariff name).
func DeletePackage(q Q, id int64) error {
	_, err := q.Exec("DELETE FROM packages WHERE id=?", id)
	return err
}

// ---- PCs ----

const pcCols = "id,name,zone_id,sort_order,machine_id,mac,ip,hostname,board_maker,board_product,agent_version,wol_status,wol_detail,wol_checked_at,wol_tested_at,wol_report,created_at"

func scanPC(sc interface{ Scan(...any) error }) (core.PC, error) {
	var p core.PC
	var checked, tested, created int64
	err := sc.Scan(&p.ID, &p.Name, &p.ZoneID, &p.SortOrder, &p.MachineID, &p.MAC, &p.IP, &p.Hostname,
		&p.BoardMaker, &p.BoardProduct, &p.AgentVersion, &p.WoL.Status, &p.WoL.Detail, &checked, &tested, &p.WoL.Report, &created)
	p.WoL.CheckedAt = fromTS(checked)
	p.WoL.TestedAt = fromTS(tested)
	p.CreatedAt = fromTS(created)
	return p, err
}

// ListPCs returns all (non-deleted) PCs in display order.
func ListPCs(q Q) ([]core.PC, error) {
	rows, err := q.Query("SELECT " + pcCols + " FROM pcs WHERE deleted=0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.PC{}
	for rows.Next() {
		p, err := scanPC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return naturalLess(out[i].Name, out[j].Name)
	})
	return out, nil
}

// naturalLess sorts "PC 2" before "PC 10".
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ad, bd := isDigit(a[0]), isDigit(b[0])
		if ad && bd {
			ai, bi := 0, 0
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			an, bn := strings.TrimLeft(a[:ai], "0"), strings.TrimLeft(b[:bi], "0")
			if len(an) != len(bn) {
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			a, b = a[ai:], b[bi:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// PCByID returns one PC.
func PCByID(q Q, id int64) (core.PC, error) {
	p, err := scanPC(q.QueryRow("SELECT "+pcCols+" FROM pcs WHERE id=? AND deleted=0", id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// FoundPC is a PC looked up by identity, including removed ones.
type FoundPC struct {
	core.PC
	Deleted   bool
	DeletedAt time.Time
}

func findPC(q Q, where string, args ...any) (*FoundPC, error) {
	var deleted int
	var deletedAt int64
	var p core.PC
	var checked, tested, created int64
	err := q.QueryRow("SELECT "+pcCols+",deleted,deleted_at FROM pcs WHERE "+where+" ORDER BY deleted, id DESC LIMIT 1", args...).Scan(
		&p.ID, &p.Name, &p.ZoneID, &p.SortOrder, &p.MachineID, &p.MAC, &p.IP, &p.Hostname,
		&p.BoardMaker, &p.BoardProduct, &p.AgentVersion, &p.WoL.Status, &p.WoL.Detail, &checked, &tested, &p.WoL.Report, &created,
		&deleted, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.WoL.CheckedAt, p.WoL.TestedAt, p.CreatedAt = fromTS(checked), fromTS(tested), fromTS(created)
	return &FoundPC{PC: p, Deleted: deleted == 1, DeletedAt: fromTS(deletedAt)}, nil
}

// PCByMachineID finds a PC by the agent's machine ID (including removed ones).
func PCByMachineID(q Q, machineID string) (*FoundPC, error) {
	return findPC(q, "machine_id=?", machineID)
}

// PCByMAC finds a PC by its network card address (including removed ones).
func PCByMAC(q Q, mac string) (*FoundPC, error) {
	if mac == "" {
		return nil, nil
	}
	return findPC(q, "mac=? COLLATE NOCASE", mac)
}

// UndeletePC brings a removed PC back.
func UndeletePC(q Q, id int64) error {
	_, err := q.Exec("UPDATE pcs SET deleted=0, deleted_at=0 WHERE id=?", id)
	return err
}

// SetPCMachineID re-links a PC to a new agent identity.
func SetPCMachineID(q Q, id int64, machineID string) error {
	if _, err := q.Exec("UPDATE pcs SET machine_id=machine_id||'-old-'||id WHERE machine_id=? AND id!=?", machineID, id); err != nil {
		return err
	}
	_, err := q.Exec("UPDATE pcs SET machine_id=? WHERE id=?", machineID, id)
	return err
}

// CreatePC registers a new PC.
func CreatePC(q Q, p core.PC) (int64, error) {
	if p.WoL.Status == "" {
		p.WoL.Status = core.WoLUnknown
	}
	res, err := q.Exec(`INSERT INTO pcs(name,zone_id,sort_order,machine_id,mac,ip,hostname,board_maker,board_product,agent_version,wol_status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.ZoneID, p.SortOrder, p.MachineID, p.MAC, p.IP, p.Hostname, p.BoardMaker, p.BoardProduct, p.AgentVersion, p.WoL.Status, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePCHardware stores what the agent reported on connect.
func UpdatePCHardware(q Q, id int64, mac, ip, hostname, maker, product, version string) error {
	_, err := q.Exec("UPDATE pcs SET mac=?,ip=?,hostname=?,board_maker=?,board_product=?,agent_version=? WHERE id=?",
		mac, ip, hostname, maker, product, version, id)
	return err
}

// UpdatePCAdmin stores admin-editable PC fields.
func UpdatePCAdmin(q Q, id int64, name string, zoneID int64, sortOrder int) error {
	_, err := q.Exec("UPDATE pcs SET name=?,zone_id=?,sort_order=? WHERE id=?", name, zoneID, sortOrder, id)
	return err
}

// UpdatePCWoL stores Wake-on-LAN status.
func UpdatePCWoL(q Q, id int64, w core.WoLInfo) error {
	_, err := q.Exec("UPDATE pcs SET wol_status=?,wol_detail=?,wol_checked_at=?,wol_tested_at=?,wol_report=? WHERE id=?",
		w.Status, w.Detail, ts(w.CheckedAt), ts(w.TestedAt), w.Report, id)
	return err
}

// DeletePC hides a PC (history stays intact).
func DeletePC(q Q, id int64) error {
	_, err := q.Exec("UPDATE pcs SET deleted=1, deleted_at=? WHERE id=?", time.Now().Unix(), id)
	return err
}

// PCCount returns the number of non-deleted PCs.
func PCCount(q Q) (int, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM pcs WHERE deleted=0").Scan(&n)
	return n, err
}

// ---- sessions ----

const sessCols = "id,pc_id,mode,status,started_at,ended_at,paused_at,paused_seconds,paid_seconds,rate_per_hour,tariff_name,paid,started_by,ended_by,shift_id,customer_id,end_reason,note,checkout_requested"

func scanSession(sc interface{ Scan(...any) error }) (core.Session, error) {
	var s core.Session
	var started int64
	var ended, paused, customer sql.NullInt64
	var mode, status string
	var checkout int
	err := sc.Scan(&s.ID, &s.PCID, &mode, &status, &started, &ended, &paused, &s.PausedSeconds, &s.PaidSeconds,
		&s.RatePerHour, &s.TariffName, &s.Paid, &s.StartedBy, &s.EndedBy, &s.ShiftID, &customer, &s.EndReason, &s.Note, &checkout)
	s.CheckoutRequested = checkout == 1
	s.Mode, s.Status = core.SessionMode(mode), core.SessionStatus(status)
	s.StartedAt = fromTS(started)
	s.EndedAt, s.PausedAt = fromNull(ended), fromNull(paused)
	if customer.Valid {
		c := customer.Int64
		s.CustomerID = &c
	}
	return s, err
}

// OpenSessions returns every session that is active or paused.
func OpenSessions(q Q) ([]core.Session, error) {
	rows, err := q.Query("SELECT " + sessCols + " FROM sessions WHERE status!='ended'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OpenSessionForPC returns the active or paused session on a PC, if any.
func OpenSessionForPC(q Q, pcID int64) (*core.Session, error) {
	s, err := scanSession(q.QueryRow("SELECT "+sessCols+" FROM sessions WHERE pc_id=? AND status!='ended' ORDER BY id DESC LIMIT 1", pcID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SessionByID returns one session.
func SessionByID(q Q, id int64) (core.Session, error) {
	s, err := scanSession(q.QueryRow("SELECT "+sessCols+" FROM sessions WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// InsertSession stores a new session.
func InsertSession(q Q, s *core.Session) error {
	res, err := q.Exec(`INSERT INTO sessions(pc_id,mode,status,started_at,ended_at,paused_at,paused_seconds,paid_seconds,rate_per_hour,tariff_name,paid,started_by,ended_by,shift_id,customer_id,end_reason,note,checkout_requested)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.PCID, string(s.Mode), string(s.Status), ts(s.StartedAt), tsp(s.EndedAt), tsp(s.PausedAt), s.PausedSeconds, s.PaidSeconds,
		s.RatePerHour, s.TariffName, s.Paid, s.StartedBy, s.EndedBy, s.ShiftID, s.CustomerID, s.EndReason, s.Note, b2i(s.CheckoutRequested))
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// UpdateSession writes every mutable field of a session.
func UpdateSession(q Q, s *core.Session) error {
	_, err := q.Exec(`UPDATE sessions SET pc_id=?,mode=?,status=?,ended_at=?,paused_at=?,paused_seconds=?,paid_seconds=?,
		rate_per_hour=?,tariff_name=?,paid=?,ended_by=?,end_reason=?,note=?,checkout_requested=? WHERE id=?`,
		s.PCID, string(s.Mode), string(s.Status), tsp(s.EndedAt), tsp(s.PausedAt), s.PausedSeconds, s.PaidSeconds,
		s.RatePerHour, s.TariffName, s.Paid, s.EndedBy, s.EndReason, s.Note, b2i(s.CheckoutRequested), s.ID)
	return err
}

// SessionsBetween returns sessions started in [from, to).
func SessionsBetween(q Q, from, to time.Time) ([]core.Session, error) {
	rows, err := q.Query("SELECT "+sessCols+" FROM sessions WHERE started_at>=? AND started_at<? ORDER BY started_at", from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- transactions ----

// InsertTransaction appends a cash movement.
func InsertTransaction(q Q, t *core.Transaction) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	res, err := q.Exec("INSERT INTO transactions(session_id,pc_id,shift_id,admin_id,kind,amount,reason,created_at) VALUES(?,?,?,?,?,?,?,?)",
		t.SessionID, t.PCID, t.ShiftID, t.AdminID, t.Kind, t.Amount, t.Reason, t.CreatedAt.Unix())
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// TransactionsBetween returns cash movements in [from, to).
func TransactionsBetween(q Q, from, to time.Time) ([]core.Transaction, error) {
	return queryTx(q, "SELECT id,session_id,pc_id,shift_id,admin_id,kind,amount,reason,created_at FROM transactions WHERE created_at>=? AND created_at<? ORDER BY id", from.Unix(), to.Unix())
}

// TransactionsForShift returns cash movements recorded in one shift.
func TransactionsForShift(q Q, shiftID int64) ([]core.Transaction, error) {
	return queryTx(q, "SELECT id,session_id,pc_id,shift_id,admin_id,kind,amount,reason,created_at FROM transactions WHERE shift_id=? ORDER BY id", shiftID)
}

func queryTx(q Q, query string, args ...any) ([]core.Transaction, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Transaction{}
	for rows.Next() {
		var t core.Transaction
		var created int64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.PCID, &t.ShiftID, &t.AdminID, &t.Kind, &t.Amount, &t.Reason, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = fromTS(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- shifts ----

const shiftCols = "id,admin_id,opened_at,closed_at,opening_cash,closing_cash,expected,note"

func scanShift(sc interface{ Scan(...any) error }) (core.Shift, error) {
	var s core.Shift
	var opened int64
	var closed sql.NullInt64
	err := sc.Scan(&s.ID, &s.AdminID, &opened, &closed, &s.OpeningCash, &s.ClosingCash, &s.Expected, &s.Note)
	s.OpenedAt = fromTS(opened)
	s.ClosedAt = fromNull(closed)
	return s, err
}

// OpenShift returns the currently open shift, if any.
func OpenShift(q Q) (core.Shift, bool, error) {
	s, err := scanShift(q.QueryRow("SELECT " + shiftCols + " FROM shifts WHERE closed_at IS NULL ORDER BY id DESC LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}

// InsertShift opens a shift.
func InsertShift(q Q, s *core.Shift) error {
	res, err := q.Exec("INSERT INTO shifts(admin_id,opened_at,opening_cash,note) VALUES(?,?,?,?)", s.AdminID, ts(s.OpenedAt), s.OpeningCash, s.Note)
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// CloseShift records the closing count.
func CloseShift(q Q, s core.Shift) error {
	_, err := q.Exec("UPDATE shifts SET closed_at=?,closing_cash=?,expected=?,note=? WHERE id=?",
		tsp(s.ClosedAt), s.ClosingCash, s.Expected, s.Note, s.ID)
	return err
}

// ShiftsBetween returns shifts opened in [from, to).
func ShiftsBetween(q Q, from, to time.Time) ([]core.Shift, error) {
	rows, err := q.Query("SELECT "+shiftCols+" FROM shifts WHERE opened_at>=? AND opened_at<? ORDER BY id", from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Shift{}
	for rows.Next() {
		s, err := scanShift(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- audit ----

// Audit appends an audit entry.
func Audit(q Q, adminID int64, action string, pcID int64, details string) error {
	_, err := q.Exec("INSERT INTO audit(admin_id,action,pc_id,details,created_at) VALUES(?,?,?,?,?)",
		adminID, action, pcID, details, time.Now().Unix())
	return err
}

// AuditBetween returns audit entries in [from, to), newest first.
func AuditBetween(q Q, from, to time.Time, limit int) ([]core.AuditEntry, error) {
	rows, err := q.Query(`SELECT a.id,a.admin_id,COALESCE(ad.username,'system'),a.action,a.pc_id,a.details,a.created_at
		FROM audit a LEFT JOIN admins ad ON ad.id=a.admin_id
		WHERE a.created_at>=? AND a.created_at<? ORDER BY a.id DESC LIMIT ?`, from.Unix(), to.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.AuditEntry{}
	for rows.Next() {
		var e core.AuditEntry
		var created int64
		if err := rows.Scan(&e.ID, &e.AdminID, &e.Admin, &e.Action, &e.PCID, &e.Details, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = fromTS(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- backups ----

// Backup writes a consistent copy of the live database to dst.
func (s *Store) Backup(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst) // VACUUM INTO refuses to overwrite
	_, err := s.DB.Exec("VACUUM INTO ?", dst)
	return err
}
