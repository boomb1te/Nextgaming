package store

import (
	"path/filepath"
	"testing"
	"time"

	"nextgaming/internal/core"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRoundTrip(t *testing.T) {
	s := openTemp(t)
	zid, err := SaveZone(s.DB, core.Zone{Name: "VIP", HourlyRate: 1500})
	if err != nil {
		t.Fatal(err)
	}
	pcid, err := CreatePC(s.DB, core.PC{Name: "PC 1", ZoneID: zid, MachineID: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	sess := &core.Session{PCID: pcid, Mode: core.ModePrepaid, Status: core.StatusActive,
		StartedAt: time.Now().Truncate(time.Second), PaidSeconds: 3600, RatePerHour: 1500, Paid: 1500}
	if err := InsertSession(s.DB, sess); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	sess.PausedAt = &now
	sess.Status = core.StatusPaused
	if err := UpdateSession(s.DB, sess); err != nil {
		t.Fatal(err)
	}
	got, err := SessionByID(s.DB, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.StatusPaused || got.PausedAt == nil || !got.PausedAt.Equal(now) || got.Paid != 1500 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	open, err := OpenSessions(s.DB)
	if err != nil || len(open) != 1 {
		t.Fatalf("open sessions = %d, %v", len(open), err)
	}
}

func TestTransactionsAreAppendOnly(t *testing.T) {
	s := openTemp(t)
	tx := &core.Transaction{Kind: core.TxSessionStart, Amount: 1000}
	if err := InsertTransaction(s.DB, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE transactions SET amount=1 WHERE id=?", tx.ID); err == nil {
		t.Fatal("UPDATE on transactions must fail")
	}
	if _, err := s.DB.Exec("DELETE FROM transactions WHERE id=?", tx.ID); err == nil {
		t.Fatal("DELETE on transactions must fail")
	}
}

func TestPCOrderingIsNatural(t *testing.T) {
	s := openTemp(t)
	for i, n := range []string{"PC 10", "PC 2", "PC 1"} {
		if _, err := CreatePC(s.DB, core.PC{Name: n, MachineID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	pcs, err := ListPCs(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if pcs[0].Name != "PC 1" || pcs[1].Name != "PC 2" || pcs[2].Name != "PC 10" {
		t.Fatalf("order = %s, %s, %s", pcs[0].Name, pcs[1].Name, pcs[2].Name)
	}
}

func TestBackup(t *testing.T) {
	s := openTemp(t)
	if _, err := SaveZone(s.DB, core.Zone{Name: "Std", HourlyRate: 800}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "b", "backup.db")
	if err := s.Backup(dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	zs, err := ListZones(b.DB)
	if err != nil || len(zs) != 1 {
		t.Fatalf("backup zones = %v, %v", zs, err)
	}
}
