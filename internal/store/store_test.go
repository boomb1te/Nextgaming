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

func TestPhoneKeyBackfill(t *testing.T) {
	s := openTemp(t)
	// Two clients, one in a non-canonical spelling; simulate a pre-v6 DB by
	// blanking the keys the normal insert computed.
	id1, err := CreateClient(s.DB, "Merdan", "065123456", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateClient(s.DB, "Aman", "+993 65 99 88 77", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE clients SET phone_key=''"); err != nil {
		t.Fatal(err)
	}
	if err := backfillPhoneKeys(s.DB); err != nil {
		t.Fatal(err)
	}
	// After backfill the non-canonical number is matchable by its local key.
	c, found, err := ClientByPhoneKey(s.DB, "65123456")
	if err != nil || !found || c.ID != id1 {
		t.Fatalf("backfilled match = (%+v, %v, %v), want client %d", c, found, err, id1)
	}
}

func TestDuplicatePhoneKeys(t *testing.T) {
	s := openTemp(t)
	if _, err := CreateClient(s.DB, "A", "65123456", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateClient(s.DB, "B", "+99365123456", ""); err != nil { // same number
		t.Fatal(err)
	}
	if _, err := CreateClient(s.DB, "C", "65000000", ""); err != nil {
		t.Fatal(err)
	}
	dups, err := DuplicatePhoneKeys(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 1 || dups[0].Key != "65123456" || len(dups[0].Clients) != 2 {
		t.Fatalf("duplicates = %+v", dups)
	}
}
