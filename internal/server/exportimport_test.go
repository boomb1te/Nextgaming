package server

import (
	"os"
	"path/filepath"
	"testing"

	"nextgaming/internal/core"
	"nextgaming/internal/store"
)

func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Build a database with a recognizable zone.
	st, err := store.Open(filepath.Join(dir, "nextgaming.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveZone(st.DB, core.Zone{Name: "VIP-marker", HourlyRate: 1234}); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "backup.db")
	if err := st.Backup(export); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// A good upload is accepted.
	if err := verifyDatabase(export); err != nil {
		t.Fatalf("valid db rejected: %v", err)
	}
	// Junk is rejected.
	junk := filepath.Join(t.TempDir(), "junk.db")
	os.WriteFile(junk, []byte("not a database"), 0o644)
	if err := verifyDatabase(junk); err == nil {
		t.Fatal("junk file accepted as a database")
	}

	// Simulate a different live DB, then restore the export over it.
	live := filepath.Join(dir, "nextgaming.db")
	st2, _ := store.Open(live)
	store.SaveZone(st2.DB, core.Zone{Name: "WILL-BE-REPLACED", HourlyRate: 1})
	st2.Close()
	if err := os.Rename(export, filepath.Join(dir, pendingRestoreName)); err != nil {
		t.Fatal(err)
	}
	if err := applyPendingRestore(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingRestoreName)); !os.IsNotExist(err) {
		t.Fatal("pending restore file should be consumed")
	}
	st3, err := store.Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	zones, _ := store.ListZones(st3.DB)
	found := false
	for _, z := range zones {
		if z.Name == "VIP-marker" && z.HourlyRate == 1234 {
			found = true
		}
		if z.Name == "WILL-BE-REPLACED" {
			t.Fatal("old data survived the restore")
		}
	}
	if !found {
		t.Fatal("restored data not found")
	}
	if _, err := os.Stat(live + ".replaced"); err != nil {
		t.Fatal("safety copy of the replaced database not kept")
	}
}

func TestExportEndpoints(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	// DB download
	req := mustReq(t, e, "GET", "/api/export/db")
	resp, err := e.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("export db: %v %v", err, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Disposition"); ct == "" {
		t.Fatal("no attachment header on db export")
	}
	resp.Body.Close()
	// CSV
	for _, path := range []string{"/api/export/payments.csv", "/api/export/sessions.csv"} {
		req := mustReq(t, e, "GET", path)
		resp, err := e.http.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
