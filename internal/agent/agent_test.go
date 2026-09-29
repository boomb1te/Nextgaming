package agent

import (
	"context"
	"testing"
	"time"

	"nextgaming/internal/protocol"
)

func newTestAgent(t *testing.T, dir string) *Agent {
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

// The PC must lock on time even with no server connection.
func TestLocalTimerLocksWithoutServer(t *testing.T) {
	dir := t.TempDir()
	a := newTestAgent(t, dir)
	if !a.ClientState().Locked {
		t.Fatal("a fresh agent must start locked")
	}
	a.apply(protocol.DesiredState{Locked: false, SessionID: 7, Mode: "prepaid", RemainingSec: 1, PCName: "PC 1"})
	if a.ClientState().Locked {
		t.Fatal("should be unlocked after a session starts")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.tick(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !a.ClientState().Locked {
		if time.Now().After(deadline) {
			t.Fatal("agent did not lock when its local timer ran out")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The lock survives an agent restart (state is on disk).
	b := newTestAgent(t, dir)
	if !b.ClientState().Locked {
		t.Fatal("restarted agent forgot the lock")
	}
}

// A prepaid deadline survives an agent restart mid-session.
func TestSessionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a := newTestAgent(t, dir)
	a.apply(protocol.DesiredState{Locked: false, SessionID: 3, Mode: "prepaid", RemainingSec: 600})
	b := newTestAgent(t, dir)
	cs := b.ClientState()
	if cs.Locked || cs.RemainingSec < 598 || cs.RemainingSec > 600 {
		t.Fatalf("after restart: %+v", cs)
	}
}

// Open sessions keep counting up while offline.
func TestOpenSessionCountsUp(t *testing.T) {
	a := newTestAgent(t, t.TempDir())
	a.apply(protocol.DesiredState{Locked: false, SessionID: 4, Mode: "open", ElapsedSec: 120})
	cs := a.ClientState()
	if cs.Locked || cs.ElapsedSec < 120 || cs.ElapsedSec > 121 {
		t.Fatalf("open state: %+v", cs)
	}
	// Player logout needs the server.
	if err := a.PlayerLogout(4); err != errNotConnected {
		t.Fatalf("offline logout err = %v", err)
	}
	if err := a.PlayerLogout(99); err == nil {
		t.Fatal("logout for a different session must fail")
	}
}
