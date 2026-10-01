package server

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

// pendingRestoreName is a database file dropped next to the live one; on the
// next start the server replaces the live database with it. Restoring while
// running would corrupt open connections, so it waits for a restart.
const pendingRestoreName = "restore-pending.db"

// applyPendingRestore replaces the database with an uploaded one, if present.
// Called before the database is opened.
func applyPendingRestore(dataDir string) error {
	pending := filepath.Join(dataDir, pendingRestoreName)
	if _, err := os.Stat(pending); err != nil {
		return nil // nothing to restore
	}
	live := filepath.Join(dataDir, "nextgaming.db")
	// Verify the upload is a usable NextGaming database before trusting it.
	if err := verifyDatabase(pending); err != nil {
		os.Rename(pending, pending+".rejected")
		return fmt.Errorf("uploaded database rejected: %w", err)
	}
	// Keep the current database as a safety copy, then swap in the upload.
	_ = os.Remove(live + ".replaced")
	if _, err := os.Stat(live); err == nil {
		if err := os.Rename(live, live+".replaced"); err != nil {
			return err
		}
	}
	// WAL/SHM side files belong to the old database; drop them.
	os.Remove(live + "-wal")
	os.Remove(live + "-shm")
	if err := os.Rename(pending, live); err != nil {
		// Put the original back if the swap failed.
		os.Rename(live+".replaced", live)
		return err
	}
	return nil
}

// verifyDatabase opens a candidate file and checks it is a NextGaming database.
func verifyDatabase(path string) error {
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()
	// A valid database has the settings row and the pcs table; Open ran migrations.
	if _, err := store.LoadSettings(st.DB); err != nil {
		return err
	}
	if _, err := store.PCCount(st.DB); err != nil {
		return err
	}
	return nil
}

// handleExportDB streams a consistent copy of the database for download.
func (s *Server) handleExportDB(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
	tmp := filepath.Join(s.cfg.DataDir(), "export-tmp.db")
	defer os.Remove(tmp)
	if err := s.st.Backup(tmp); err != nil {
		return nil, err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	name := fmt.Sprintf("nextgaming-%s.db", s.now().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if fi != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	http.ServeContent(w, r, name, time.Now(), f)
	store.Audit(s.st.DB, a.ID, "export_db", 0, name)
	return nil, errHandled
}

// handleImportDB accepts an uploaded database and schedules a restore.
func (s *Server) handleImportDB(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return nil, errBad("could not read the upload")
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, errBad("choose a .db file to import")
	}
	defer file.Close()

	pending := filepath.Join(s.cfg.DataDir(), pendingRestoreName)
	out, err := os.Create(pending)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(out, io.LimitReader(file, 512<<20)); err != nil {
		out.Close()
		os.Remove(pending)
		return nil, err
	}
	out.Close()

	if err := verifyDatabase(pending); err != nil {
		os.Remove(pending)
		return nil, errBad("that file is not a valid NextGaming database")
	}
	store.Audit(s.st.DB, a.ID, "import_db", 0, hdr.Filename)
	return map[string]string{"message": "Database uploaded. Restart the server (sc stop NextGamingServer, then sc start) to apply it. Current data is kept as nextgaming.db.replaced."}, nil
}

// handleExportCSV writes payments or sessions as CSV for a date range.
func (s *Server) handleExportCSV(kind string) handler {
	return func(w http.ResponseWriter, r *http.Request, a core.Admin) (any, error) {
		from, to, err := dateRange(r)
		if err != nil {
			return nil, err
		}
		pcs, _ := store.ListPCs(s.st.DB)
		pcName := map[int64]string{}
		for _, p := range pcs {
			pcName[p.ID] = p.Name
		}
		admins, _ := store.ListAdmins(s.st.DB)
		adminName := map[int64]string{0: "system"}
		for _, ad := range admins {
			adminName[ad.ID] = ad.Username
		}
		cur := s.Settings().Currency

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="nextgaming-%s-%s.csv"`, kind, s.now().Format("2006-01-02")))
		w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM so Excel reads UTF-8
		cw := csv.NewWriter(w)
		defer cw.Flush()

		switch kind {
		case "payments":
			cw.Write([]string{"Time", "PC", "Type", "Cashier", "Amount (" + cur + ")", "Reason", "SessionID"})
			txs, err := store.TransactionsBetween(s.st.DB, from, to)
			if err != nil {
				return nil, err
			}
			for _, t := range txs {
				cw.Write([]string{t.CreatedAt.Local().Format("2006-01-02 15:04"), pcName[t.PCID], t.Kind, adminName[t.AdminID],
					core.FormatMoney(t.Amount), t.Reason, strconv.FormatInt(t.SessionID, 10)})
			}
		case "sessions":
			cw.Write([]string{"ID", "PC", "Mode", "Tariff", "Started", "Ended", "Minutes played", "Paid (" + cur + ")"})
			sessions, err := store.SessionsBetween(s.st.DB, from, to)
			if err != nil {
				return nil, err
			}
			for _, sess := range sessions {
				ended := ""
				if sess.EndedAt != nil {
					ended = sess.EndedAt.Local().Format("2006-01-02 15:04")
				}
				mins := strconv.FormatInt(sess.ActiveSeconds(s.now())/60, 10)
				cw.Write([]string{strconv.FormatInt(sess.ID, 10), pcName[sess.PCID], string(sess.Mode), sess.TariffName,
					sess.StartedAt.Local().Format("2006-01-02 15:04"), ended, mins, core.FormatMoney(sess.Paid)})
			}
		}
		store.Audit(s.st.DB, a.ID, "export_csv", 0, kind)
		return nil, errHandled
	}
}

func dateRange(r *http.Request) (time.Time, time.Time, error) {
	q := r.URL.Query()
	parse := func(s string) (time.Time, error) { return time.ParseInLocation("2006-01-02", s, time.Local) }
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 1)
	if v := q.Get("from"); v != "" {
		t, err := parse(v)
		if err != nil {
			return from, to, errBad("from date must look like 2026-09-29")
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := parse(v)
		if err != nil {
			return from, to, errBad("to date must look like 2026-09-29")
		}
		to = t.AddDate(0, 0, 1) // inclusive of the "to" day
	}
	return from, to, nil
}
