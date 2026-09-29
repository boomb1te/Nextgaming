package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

// PCStat is one PC's line in a report.
type PCStat struct {
	PCID     int64      `json:"pcId"`
	Name     string     `json:"name"`
	Revenue  core.Money `json:"revenue"`
	Sessions int        `json:"sessions"`
	Hours    float64    `json:"hours"`
}

// AdminStat is one cashier's takings.
type AdminStat struct {
	AdminID int64      `json:"adminId"`
	Name    string     `json:"name"`
	Revenue core.Money `json:"revenue"`
	Count   int        `json:"count"`
}

// TxView is a transaction with names resolved.
type TxView struct {
	core.Transaction
	PCName    string `json:"pcName"`
	AdminName string `json:"adminName"`
}

// DailyReport summarises one calendar day (server local time).
type DailyReport struct {
	Date         string                `json:"date"`
	Revenue      core.Money            `json:"revenue"`
	ByKind       map[string]core.Money `json:"byKind"`
	Sessions     int                   `json:"sessions"`
	PaidSessions int                   `json:"paidSessions"`
	HoursPlayed  float64               `json:"hoursPlayed"`
	ByPC         []PCStat              `json:"byPc"`
	ByAdmin      []AdminStat           `json:"byAdmin"`
	ByHour       [24]core.Money        `json:"byHour"`
	Transactions []TxView              `json:"transactions"`
	Shifts       []core.Shift          `json:"shifts"`
}

// Report builds the report for a date like "2026-09-29" (empty = today).
func (s *Server) Report(date string) (*DailyReport, error) {
	day := s.now()
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, time.Local)
		if err != nil {
			return nil, errBad("date must look like 2026-09-29")
		}
		day = d
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 1)
	now := s.now()

	txs, err := store.TransactionsBetween(s.st.DB, from, to)
	if err != nil {
		return nil, err
	}
	sessions, err := store.SessionsBetween(s.st.DB, from, to)
	if err != nil {
		return nil, err
	}
	shifts, err := store.ShiftsBetween(s.st.DB, from, to)
	if err != nil {
		return nil, err
	}
	pcs, _ := store.ListPCs(s.st.DB)
	admins, _ := store.ListAdmins(s.st.DB)
	pcName := map[int64]string{}
	for _, p := range pcs {
		pcName[p.ID] = p.Name
	}
	adminName := map[int64]string{0: "system"}
	for _, a := range admins {
		adminName[a.ID] = a.Username
	}

	r := &DailyReport{Date: from.Format("2006-01-02"), ByKind: map[string]core.Money{}, Transactions: []TxView{}, Shifts: shifts}
	perPC := map[int64]*PCStat{}
	getPC := func(id int64) *PCStat {
		if perPC[id] == nil {
			name := pcName[id]
			if name == "" {
				name = fmt.Sprintf("PC #%d (removed)", id)
			}
			perPC[id] = &PCStat{PCID: id, Name: name}
		}
		return perPC[id]
	}
	perAdmin := map[int64]*AdminStat{}
	for _, t := range txs {
		r.Revenue += t.Amount
		r.ByKind[t.Kind] += t.Amount
		r.ByHour[t.CreatedAt.In(time.Local).Hour()] += t.Amount
		if t.PCID != 0 {
			getPC(t.PCID).Revenue += t.Amount
		}
		a := perAdmin[t.AdminID]
		if a == nil {
			a = &AdminStat{AdminID: t.AdminID, Name: adminName[t.AdminID]}
			perAdmin[t.AdminID] = a
		}
		a.Revenue += t.Amount
		a.Count++
		r.Transactions = append(r.Transactions, TxView{Transaction: t, PCName: pcName[t.PCID], AdminName: adminName[t.AdminID]})
	}
	for _, sess := range sessions {
		r.Sessions++
		if sess.Mode != core.ModeFree {
			r.PaidSessions++
		}
		h := float64(sess.ActiveSeconds(now)) / 3600
		r.HoursPlayed += h
		ps := getPC(sess.PCID)
		ps.Sessions++
		ps.Hours += h
	}
	for _, p := range perPC {
		p.Hours = round1(p.Hours)
		r.ByPC = append(r.ByPC, *p)
	}
	sort.Slice(r.ByPC, func(i, j int) bool {
		if r.ByPC[i].Revenue != r.ByPC[j].Revenue {
			return r.ByPC[i].Revenue > r.ByPC[j].Revenue
		}
		return r.ByPC[i].Hours > r.ByPC[j].Hours
	})
	for _, a := range perAdmin {
		r.ByAdmin = append(r.ByAdmin, *a)
	}
	sort.Slice(r.ByAdmin, func(i, j int) bool { return r.ByAdmin[i].Revenue > r.ByAdmin[j].Revenue })
	r.HoursPlayed = round1(r.HoursPlayed)
	return r, nil
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

// Audit returns the audit log for a day.
func (s *Server) AuditLog(date string) ([]core.AuditEntry, error) {
	day := s.now()
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, time.Local)
		if err != nil {
			return nil, errBad("date must look like 2026-09-29")
		}
		day = d
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	return store.AuditBetween(s.st.DB, from, from.AddDate(0, 0, 1), 1000)
}

// ---- daily backups ----

func (s *Server) backupDir() string { return filepath.Join(s.cfg.DataDir(), "backups") }

// BackupNow writes today's backup (overwriting an earlier one from today).
func (s *Server) BackupNow() (string, error) {
	name := "nextgaming-" + s.now().Format("2006-01-02") + ".db"
	dst := filepath.Join(s.backupDir(), name)
	if err := s.st.Backup(dst); err != nil {
		return "", err
	}
	s.pruneBackups()
	return dst, nil
}

func (s *Server) pruneBackups() {
	keep := s.cfg.BackupKeep
	if keep <= 0 {
		keep = 30
	}
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "nextgaming-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // dates sort chronologically
	for len(names) > keep {
		os.Remove(filepath.Join(s.backupDir(), names[0]))
		names = names[1:]
	}
}

// backupLoop makes one backup per day.
func (s *Server) backupLoop(ctx context.Context) {
	check := func() {
		name := "nextgaming-" + s.now().Format("2006-01-02") + ".db"
		if _, err := os.Stat(filepath.Join(s.backupDir(), name)); err == nil {
			return
		}
		if path, err := s.BackupNow(); err != nil {
			log.Printf("backup failed: %v", err)
			s.alert("error", 0, "Daily database backup failed: %v", err)
		} else {
			log.Printf("backup written: %s", path)
		}
	}
	check()
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}
