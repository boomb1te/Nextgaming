package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nextgaming/internal/agent"
	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type env struct {
	t      *testing.T
	s      *Server
	base   string
	http   *http.Client
	offset *atomic.Int64
	agents []*agent.Agent
	ports  []int
}

func (e *env) advance(d time.Duration) { e.offset.Add(int64(d)) }

func (e *env) call(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.base+path, rd)
	req.Header.Set("X-NextGaming", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (e *env) must(method, path string, body any, out any) {
	e.t.Helper()
	if err := e.call(method, path, body, out); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) snapshot() *Snapshot {
	e.t.Helper()
	var s Snapshot
	e.must("GET", "/api/state", nil, &s)
	return &s
}

func (e *env) pc(name string) PCView {
	e.t.Helper()
	for _, p := range e.snapshot().PCs {
		if p.Name == name {
			return p
		}
	}
	e.t.Fatalf("no PC %q", name)
	return PCView{}
}

// clientState reads what the lock screen on agent i would show.
func (e *env) clientState(i int) protocol.ClientState {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/state", e.ports[i]))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var cs protocol.ClientState
	json.NewDecoder(resp.Body).Decode(&cs)
	return cs
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func setup(t *testing.T, nAgents int) *env {
	t.Helper()
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	httpPort, agentPort := freePort(t), freePort(t)
	cfg.HTTPAddr = fmt.Sprintf("127.0.0.1:%d", httpPort)
	cfg.AgentAddr = fmt.Sprintf("127.0.0.1:%d", agentPort)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	s.now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); time.Sleep(100 * time.Millisecond); s.Close() })
	go s.Run(ctx)

	jar, _ := cookiejar.New(nil)
	e := &env{t: t, s: s, base: fmt.Sprintf("http://127.0.0.1:%d", httpPort), http: &http.Client{Jar: jar, Timeout: 10 * time.Second}, offset: offset}
	eventually(t, "http server", func() bool { return e.call("GET", "/api/setup", nil, nil) == nil })

	for i := 0; i < nAgents; i++ {
		ac, _ := agent.LoadConfig(t.TempDir())
		ac.Server = cfg.AgentAddr
		ac.ClubKey = cfg.ClubKey
		ac.MachineID = fmt.Sprintf("test-pc-%d", i+1)
		ac.LocalPort = freePort(t)
		ac.ClientPath = "/nonexistent"
		ac.InstalledAt = time.Now()
		a := agent.New(ac)
		e.agents = append(e.agents, a)
		e.ports = append(e.ports, ac.LocalPort)
		go a.Run(ctx)
		// Register in order so they are named PC 1, PC 2, ...
		want := i + 1
		eventually(t, fmt.Sprintf("agent %d registered", want), func() bool {
			s.hub.mu.RLock()
			defer s.hub.mu.RUnlock()
			return len(s.hub.conns) >= want
		})
	}
	return e
}

func TestEndToEnd(t *testing.T) {
	e := setup(t, 3)

	// --- setup & auth ---
	if err := e.call("GET", "/api/state", nil, nil); err == nil {
		t.Fatal("state must require login")
	}
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1", "clubName": "Test Club"}, nil)
	var me core.Admin
	e.must("GET", "/api/me", nil, &me)
	if me.Role != core.RoleOwner {
		t.Fatalf("role = %s", me.Role)
	}
	// CSRF guard: a POST without the custom header is refused.
	req, _ := http.NewRequest("POST", e.base+"/api/shift/open", strings.NewReader(`{"openingCash":0}`))
	resp, err := e.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("CSRF guard: status %v err %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	snap := e.snapshot()
	if len(snap.PCs) != 3 || snap.PCs[0].Name != "PC 1" || snap.PCs[2].Name != "PC 3" {
		t.Fatalf("PCs = %+v", snap.PCs)
	}
	if len(snap.Zones) != 1 || snap.Zones[0].HourlyRate != 1000 {
		t.Fatalf("default zone = %+v", snap.Zones)
	}
	pc1, pc2, pc3 := e.pc("PC 1"), e.pc("PC 2"), e.pc("PC 3")
	if !pc1.Runtime.Online || pc1.State != "free" {
		t.Fatalf("PC 1 = %+v", pc1)
	}
	eventually(t, "club name reaches the PC", func() bool {
		cs := e.clientState(0)
		return cs.Locked && cs.PCName == "PC 1" && cs.ClubName == "Test Club"
	})

	// --- money needs a shift ---
	err = e.call("POST", fmt.Sprintf("/api/pcs/%d/start", pc1.ID), StartRequest{Mode: core.ModePrepaid, Minutes: 60}, nil)
	if err == nil || !strings.Contains(err.Error(), "shift") {
		t.Fatalf("start without shift: %v", err)
	}
	e.must("POST", "/api/shift/open", map[string]any{"openingCash": 5000}, nil)

	// --- prepaid session: 60 min at 10.00/h ---
	e.must("POST", fmt.Sprintf("/api/pcs/%d/start", pc1.ID), StartRequest{Mode: core.ModePrepaid, Minutes: 60}, nil)
	eventually(t, "PC 1 unlocks", func() bool { cs := e.clientState(0); return !cs.Locked && cs.Mode == "prepaid" })
	cs := e.clientState(0)
	if cs.RemainingSec < 3590 || cs.RemainingSec > 3600 {
		t.Fatalf("remaining = %d", cs.RemainingSec)
	}
	if err := e.call("POST", fmt.Sprintf("/api/pcs/%d/start", pc1.ID), StartRequest{Mode: core.ModeOpen}, nil); err == nil {
		t.Fatal("double start must fail")
	}

	// add 30 minutes (5.00)
	e.must("POST", fmt.Sprintf("/api/pcs/%d/add-time", pc1.ID), TimeRequest{Minutes: 30}, nil)
	eventually(t, "PC 1 gets extra time", func() bool { return e.clientState(0).RemainingSec > 3600 })

	// pause locks the PC, resume unlocks
	e.must("POST", fmt.Sprintf("/api/pcs/%d/pause", pc1.ID), nil, nil)
	eventually(t, "PC 1 locks on pause", func() bool { cs := e.clientState(0); return cs.Locked && cs.Paused })
	e.must("POST", fmt.Sprintf("/api/pcs/%d/resume", pc1.ID), nil, nil)
	eventually(t, "PC 1 unlocks on resume", func() bool { return !e.clientState(0).Locked })

	// --- time runs out: server ends the session and locks the PC ---
	e.advance(91 * time.Minute)
	eventually(t, "PC 1 locks when time is up", func() bool { return e.clientState(0).Locked })
	eventually(t, "PC 1 free again", func() bool { return e.pc("PC 1").State == "free" })

	// --- open session on PC 2: 30 minutes costs 5.00 ---
	e.must("POST", fmt.Sprintf("/api/pcs/%d/start", pc2.ID), StartRequest{Mode: core.ModeOpen}, nil)
	eventually(t, "PC 2 unlocks", func() bool { cs := e.clientState(1); return !cs.Locked && cs.Mode == "open" })
	e.advance(30 * time.Minute)
	var q EndQuote
	e.must("GET", fmt.Sprintf("/api/pcs/%d/quote", pc2.ID), nil, &q)
	if q.Charge < 500 || q.Charge > 502 { // 30 min (+ up to one started minute)
		t.Fatalf("open charge = %d", q.Charge)
	}
	// move it to PC 3 first
	e.must("POST", fmt.Sprintf("/api/pcs/%d/move", pc2.ID), map[string]int64{"toPcId": pc3.ID}, nil)
	eventually(t, "PC 2 locks after move", func() bool { return e.clientState(1).Locked })
	eventually(t, "PC 3 unlocks after move", func() bool { return !e.clientState(2).Locked })
	var endRes EndResult
	e.must("POST", fmt.Sprintf("/api/pcs/%d/end", pc3.ID), EndRequest{}, &endRes)
	endRes0 := endRes.Collected
	if endRes.Collected != q.Charge && endRes.Collected != q.Charge+17 {
		t.Fatalf("collected %d, quoted %d", endRes.Collected, q.Charge)
	}
	eventually(t, "PC 3 locks after end", func() bool { return e.clientState(2).Locked })

	// --- the player presses "End session" on the PC ---
	e.must("POST", fmt.Sprintf("/api/pcs/%d/start", pc1.ID), StartRequest{Mode: core.ModePrepaid, Minutes: 120}, nil)
	eventually(t, "PC 1 unlocks again", func() bool { return !e.clientState(0).Locked })
	sid := e.clientState(0).SessionID
	b, _ := json.Marshal(map[string]int64{"sessionId": sid})
	lreq, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/logout", e.ports[0]), bytes.NewReader(b))
	lreq.Header.Set("X-NextGaming-Client", "1")
	lresp, err := http.DefaultClient.Do(lreq)
	if err != nil || lresp.StatusCode != 200 {
		t.Fatalf("player logout: %v %v", err, lresp.StatusCode)
	}
	lresp.Body.Close()
	eventually(t, "PC 1 shows checkout", func() bool { return e.pc("PC 1").State == "checkout" })
	eventually(t, "PC 1 locks on checkout", func() bool { return e.clientState(0).Locked })
	e.advance(10 * time.Minute) // paused: no time is used while waiting at the desk
	e.must("GET", fmt.Sprintf("/api/pcs/%d/quote", pc1.ID), nil, &q)
	if q.RemainingSec < 7190 {
		t.Fatalf("checkout should stop the clock; remaining = %d", q.RemainingSec)
	}
	if q.SuggestedRefund < 1990 || q.SuggestedRefund > 2000 {
		t.Fatalf("suggested refund = %d", q.SuggestedRefund)
	}
	if err := e.call("POST", fmt.Sprintf("/api/pcs/%d/end", pc1.ID), EndRequest{Refund: 99999}, nil); err == nil {
		t.Fatal("refund larger than paid must fail")
	}
	e.must("POST", fmt.Sprintf("/api/pcs/%d/end", pc1.ID), EndRequest{Refund: 2000, Reason: "left early"}, &endRes)
	if endRes.Refunded != 2000 || endRes.TotalPaid != 0 {
		t.Fatalf("end result = %+v", endRes)
	}

	// --- staff unlock is free and locking ends it ---
	e.must("POST", fmt.Sprintf("/api/pcs/%d/unlock-free", pc2.ID), map[string]string{}, nil)
	eventually(t, "PC 2 staff unlock", func() bool { cs := e.clientState(1); return !cs.Locked && cs.Mode == "free" })
	e.must("POST", fmt.Sprintf("/api/pcs/%d/lock", pc2.ID), nil, nil)
	eventually(t, "PC 2 locked", func() bool { return e.clientState(1).Locked })

	// --- admin message reaches the PC ---
	e.must("POST", fmt.Sprintf("/api/pcs/%d/message", pc2.ID), map[string]string{"text": "Closing in 10 minutes"}, nil)
	eventually(t, "message delivered", func() bool { cs := e.clientState(1); return cs.Message == "Closing in 10 minutes" && cs.MessageID == 1 })

	// --- money adds up: shift takings == report revenue ---
	// prepaid 10.00 + add 5.00 + open ~5.00 + prepaid 20.00 - refund 20.00
	snap = e.snapshot()
	shift := *snap.Shift
	if want := core.Money(1000+500+2000-2000) + endRes0; shift.Takings != want {
		t.Fatalf("shift takings %d, want %d", shift.Takings, want)
	}
	var rep DailyReport
	e.must("GET", "/api/reports/daily", nil, &rep)
	if rep.Revenue != shift.Takings {
		t.Fatalf("report revenue %d != shift takings %d", rep.Revenue, shift.Takings)
	}
	if rep.ByKind[core.TxRefund] != -2000 || rep.ByKind[core.TxAddTime] != 500 {
		t.Fatalf("by kind = %+v", rep.ByKind)
	}
	var closed ShiftView
	e.must("POST", "/api/shift/close", map[string]any{"countedCash": 5000 + rep.Revenue}, &closed)
	if closed.Expected != closed.ClosingCash {
		t.Fatalf("shift close expected %d counted %d", closed.Expected, closed.ClosingCash)
	}

	// --- cash log cannot be edited ---
	if _, err := e.s.st.DB.Exec("UPDATE transactions SET amount = 0"); err == nil {
		t.Fatal("transactions must be append-only")
	}

	// --- audit log recorded actions ---
	var audit []core.AuditEntry
	e.must("GET", "/api/audit", nil, &audit)
	if len(audit) < 10 {
		t.Fatalf("audit entries = %d", len(audit))
	}
}

func TestWrongClubKeyIsRejected(t *testing.T) {
	e := setup(t, 0)
	c, err := net.Dial("tcp", e.s.cfg.AgentAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	conn := protocol.NewConn(c)
	conn.Send(protocol.TypeHello, "", protocol.Hello{ProtocolVersion: protocol.Version, ClubKey: "WRONG", MachineID: "x"})
	env, err := conn.Recv()
	if err != nil || env.Type != protocol.TypeReject {
		t.Fatalf("got %v %v, want reject", env.Type, err)
	}
}

// startAgent runs one in-process agent with its own lifetime.
func startAgent(t *testing.T, e *env, machineID string, installedAt time.Time) (context.CancelFunc, int) {
	t.Helper()
	ac, _ := agent.LoadConfig(t.TempDir())
	ac.Server = e.s.cfg.AgentAddr
	ac.ClubKey = e.s.cfg.ClubKey
	ac.MachineID = machineID
	ac.LocalPort = freePort(t)
	ac.ClientPath = "/nonexistent"
	ac.InstalledAt = installedAt
	ctx, cancel := context.WithCancel(context.Background())
	go agent.New(ac).Run(ctx)
	t.Cleanup(cancel)
	return cancel, ac.LocalPort
}

func onlineCount(e *env) int {
	n := 0
	for _, p := range e.snapshot().PCs {
		if p.Runtime.Online {
			n++
		}
	}
	return n
}

// Removing a PC uninstalls its agent and keeps it removed; reinstalling brings it back.
func TestRemovePCStaysRemoved(t *testing.T) {
	var uninstalls atomic.Int32
	agent.SelfUninstall = func() { uninstalls.Add(1) }

	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	stop, _ := startAgent(t, e, "remove-me", time.Now().Add(-time.Hour))
	eventually(t, "PC online", func() bool { return onlineCount(e) == 1 })
	pc := e.pc("PC 1")

	e.must("DELETE", fmt.Sprintf("/api/pcs/%d", pc.ID), nil, nil)
	eventually(t, "agent told to uninstall", func() bool { return uninstalls.Load() >= 1 })
	// The (simulated) agent keeps trying to reconnect; it must be refused.
	time.Sleep(2500 * time.Millisecond)
	if n := len(e.snapshot().PCs); n != 0 {
		t.Fatalf("removed PC came back: %d PCs listed", n)
	}
	stop()

	// Installing the agent again after the removal brings the same PC back.
	time.Sleep(1100 * time.Millisecond) // removal times have 1 s resolution
	startAgent(t, e, "remove-me", time.Now())
	eventually(t, "reinstalled PC back", func() bool { return len(e.snapshot().PCs) == 1 && onlineCount(e) == 1 })
	if got := e.snapshot().PCs[0]; got.ID != pc.ID || got.Name != "PC 1" {
		t.Fatalf("reinstalled PC = %d %q, want %d PC 1", got.ID, got.Name, pc.ID)
	}
}

// A PC whose agent ID changed (e.g. after an update) is matched by its MAC,
// so no duplicate tile appears.
func TestChangedIDMatchedByMAC(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	stop, _ := startAgent(t, e, "old-id", time.Now())
	eventually(t, "PC online", func() bool { return onlineCount(e) == 1 })
	if e.snapshot().PCs[0].MAC == "" {
		t.Skip("no network card with a MAC address in this test environment")
	}
	stop()
	eventually(t, "PC offline", func() bool { return onlineCount(e) == 0 })
	startAgent(t, e, "new-id", time.Now())
	eventually(t, "PC online again", func() bool { return onlineCount(e) == 1 })
	if n := len(e.snapshot().PCs); n != 1 {
		t.Fatalf("got %d PCs, want 1 (duplicate created)", n)
	}
}

// mustReq builds an authenticated GET request for tests.
func mustReq(t *testing.T, e *env, method, path string) *http.Request {
	t.Helper()
	req, _ := http.NewRequest(method, e.base+path, nil)
	req.Header.Set("X-NextGaming", "1")
	return req
}
