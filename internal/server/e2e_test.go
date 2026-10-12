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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nextgaming/internal/agent"
	"nextgaming/internal/core"
	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
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
	t          *testing.T
	s          *Server
	base       string
	http       *http.Client
	offset     *atomic.Int64
	agents     []*agent.Agent
	ports      []int
	sharedDirs []string
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
		ac.SharedDir = filepath.Join(t.TempDir(), "shared") // isolate each PC's shared folder
		ac.InstalledAt = time.Now()
		a := agent.New(ac)
		e.agents = append(e.agents, a)
		e.ports = append(e.ports, ac.LocalPort)
		e.sharedDirs = append(e.sharedDirs, ac.SharedDir)
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

func TestPCNote(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	e.must("POST", fmt.Sprintf("/api/pcs/%d/note", pc.ID), map[string]string{"note": "VIP seat"}, nil)
	eventually(t, "note shows", func() bool { return e.pc("PC 1").Note == "VIP seat" })
	// clearing works
	e.must("POST", fmt.Sprintf("/api/pcs/%d/note", pc.ID), map[string]string{"note": ""}, nil)
	eventually(t, "note cleared", func() bool { return e.pc("PC 1").Note == "" })
}

func TestStaffAccountsEditable(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	e.must("POST", "/api/admins", map[string]string{"username": "cash1", "password": "secret1", "role": "cashier"}, nil)
	var admins []core.Admin
	e.must("GET", "/api/admins", nil, &admins)
	var cashID int64
	for _, a := range admins {
		if a.Username == "cash1" {
			cashID = a.ID
		}
	}
	if cashID == 0 {
		t.Fatal("cashier not created")
	}
	// rename + promote
	e.must("PUT", fmt.Sprintf("/api/admins/%d", cashID), map[string]string{"username": "boss", "role": "owner"}, nil)
	e.must("GET", "/api/admins", nil, &admins)
	found := false
	for _, a := range admins {
		if a.ID == cashID && a.Username == "boss" && a.Role == "owner" {
			found = true
		}
	}
	if !found {
		t.Fatal("rename/role change did not apply")
	}
	// delete
	e.must("DELETE", fmt.Sprintf("/api/admins/%d", cashID), nil, nil)
	e.must("GET", "/api/admins", nil, &admins)
	for _, a := range admins {
		if a.ID == cashID {
			t.Fatal("account not deleted")
		}
	}
	// cannot delete self (the only owner)
	var me core.Admin
	e.must("GET", "/api/me", nil, &me)
	if err := e.call("DELETE", fmt.Sprintf("/api/admins/%d", me.ID), nil, nil); err == nil {
		t.Fatal("deleting own/only-owner account must fail")
	}
}

func TestStaffLoginAtPC(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	// Wrong password: no unlock.
	if err := e.s.staffLogin(pc.ID, "owner", "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	// Right password: PC unlocks as a free (staff) session.
	if err := e.s.staffLogin(pc.ID, "owner", "secret1"); err != nil {
		t.Fatalf("staff login: %v", err)
	}
	eventually(t, "PC unlocked by staff", func() bool {
		p := e.pc("PC 1")
		return p.State == "staff" && !e.clientState(0).Locked
	})
	// Already has a session -> refused.
	if err := e.s.staffLogin(pc.ID, "owner", "secret1"); err == nil {
		t.Fatal("staff login on a busy PC should fail")
	}
}

// Full wire path: POST to the agent's local staff-login API unlocks the PC.
func TestStaffLoginWire(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	post := func(user, pass string) int {
		b, _ := json.Marshal(map[string]string{"username": user, "password": pass})
		req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/staff-login", e.ports[0]), bytes.NewReader(b))
		req.Header.Set("X-NextGaming-Client", "1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("staff-login request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post("owner", "nope") == 200 {
		t.Fatal("wrong password accepted over the wire")
	}
	if e.pc("PC 1").State != "free" {
		t.Fatal("PC should still be free after a failed login")
	}
	if code := post("owner", "secret1"); code != 200 {
		t.Fatalf("staff login failed over the wire: %d", code)
	}
	eventually(t, "PC unlocked by staff over the wire", func() bool {
		return e.pc("PC 1").State == "staff" && !e.clientState(0).Locked
	})
}

// Full screen-monitoring path: when the panel starts watching, the server asks
// the agent for a frame; a stand-in for the lock screen (which only runs on
// Windows) answers the agent's /capture-wait and posts a JPEG, which the panel
// can then fetch from /api/pcs/{id}/screen.jpg.
func TestScreenMonitoringWire(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	local := fmt.Sprintf("http://127.0.0.1:%d", e.ports[0])
	frame := []byte("\xff\xd8\xff\xe0JPEGBYTES\xff\xd9") // stand-in payload

	// Stand in for the lock screen: long-poll the agent, then post a frame.
	go func() {
		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest("GET", local+"/capture-wait", nil)
			req.Header.Set("X-NextGaming-Client", "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			code := resp.StatusCode
			resp.Body.Close()
			if code != 200 {
				continue // 204 timeout: poll again
			}
			pr, _ := http.NewRequest("POST", local+"/screen?w=4&h=2", bytes.NewReader(frame))
			pr.Header.Set("X-NextGaming-Client", "1")
			pr.Header.Set("Content-Type", "image/jpeg")
			if rp, err := http.DefaultClient.Do(pr); err == nil {
				rp.Body.Close()
			}
		}
	}()

	// Before watching, there is no frame.
	if code, _ := e.getRaw(fmt.Sprintf("/api/pcs/%d/screen.jpg", pc.ID)); code != http.StatusNoContent {
		t.Fatalf("expected 204 before watching, got %d", code)
	}

	// Start watching; the server should now drive captures.
	e.must("POST", "/api/screens/state", map[string]any{"watching": true}, nil)
	eventually(t, "a frame arrives", func() bool {
		code, body := e.getRaw(fmt.Sprintf("/api/pcs/%d/screen.jpg", pc.ID))
		return code == 200 && bytes.Equal(body, frame)
	})

	// Stop watching; the stored frame goes stale and is no longer served.
	e.must("POST", "/api/screens/state", map[string]any{"watching": false}, nil)
}

// Frames must keep updating tick after tick, not freeze on the first one.
// This mirrors the real helper loop: long-poll /capture-wait, post a NEW frame,
// repeat — and checks the server serves each new frame in turn.
func TestScreenMonitoringKeepsUpdating(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	local := fmt.Sprintf("http://127.0.0.1:%d", e.ports[0])

	e.must("POST", "/api/screens/state", map[string]any{"watching": true}, nil)

	for round := 1; round <= 4; round++ {
		// Long-poll until the server asks for a capture (keeping watching alive).
		got := false
		for try := 0; try < 60 && !got; try++ {
			e.getRaw(fmt.Sprintf("/api/pcs/%d/screen.jpg", pc.ID)) // keepAlive
			req, _ := http.NewRequest("GET", local+"/capture-wait", nil)
			req.Header.Set("X-NextGaming-Client", "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			code := resp.StatusCode
			resp.Body.Close()
			if code != 200 {
				continue
			}
			got = true
		}
		if !got {
			t.Fatalf("round %d: never got a capture request from the server", round)
		}
		frame := []byte(fmt.Sprintf("\xff\xd8\xff\xe0FRAME-%d\xff\xd9", round))
		pr, _ := http.NewRequest("POST", local+"/screen?w=4&h=2", bytes.NewReader(frame))
		pr.Header.Set("X-NextGaming-Client", "1")
		pr.Header.Set("Content-Type", "image/jpeg")
		if rp, err := http.DefaultClient.Do(pr); err == nil {
			rp.Body.Close()
		}
		eventually(t, fmt.Sprintf("round %d frame served", round), func() bool {
			code, body := e.getRaw(fmt.Sprintf("/api/pcs/%d/screen.jpg", pc.ID))
			return code == 200 && bytes.Equal(body, frame)
		})
	}
}

func TestClientsAndGroups(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)

	// name required, phone must be a valid Turkmen number
	if err := e.call("POST", "/api/clients", map[string]string{"name": "", "phone": "65000001"}, nil); err == nil {
		t.Fatal("client without a name should be rejected")
	}
	if err := e.call("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": ""}, nil); err == nil {
		t.Fatal("client without a phone should be rejected")
	}
	if err := e.call("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "123"}, nil); err == nil {
		t.Fatal("client with an invalid (too short) phone should be rejected")
	}

	// create two clients; names may repeat but phone numbers are unique
	var c1, c2 struct{ ID int64 }
	e.must("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "+993 65 00 00 01", "note": "VIP"}, &c1)
	e.must("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "65000002"}, &c2) // same name OK
	if c1.ID == 0 || c2.ID == 0 || c1.ID == c2.ID {
		t.Fatalf("expected two distinct clients, got %d and %d", c1.ID, c2.ID)
	}
	// a duplicate number — in any format — is rejected
	if err := e.call("POST", "/api/clients", map[string]string{"name": "Someone", "phone": "065000001"}, nil); err == nil {
		t.Fatal("duplicate phone number should be rejected")
	}

	// search by phone (stored canonical form contains the local digits)
	var found []core.Client
	e.must("GET", "/api/clients?q=65000001", nil, &found)
	if len(found) != 1 || found[0].ID != c1.ID {
		t.Fatalf("search by phone returned %d, want 1 (c1)", len(found))
	}
	if found[0].Phone != "+99365000001" {
		t.Fatalf("phone not canonicalised: %q", found[0].Phone)
	}

	// group needs at least one member
	if err := e.call("POST", "/api/groups", map[string]any{"name": "Team A", "memberIds": []int64{}}, nil); err == nil {
		t.Fatal("empty group should be rejected")
	}
	var g struct{ ID int64 }
	e.must("POST", "/api/groups", map[string]any{"name": "Team A", "memberIds": []int64{c1.ID}}, &g)

	// add the second member, then removing down to one is fine, but not to zero
	e.must("POST", fmt.Sprintf("/api/groups/%d/members", g.ID), map[string]int64{"clientId": c2.ID}, nil)
	var grp core.ClientGroup
	e.must("GET", fmt.Sprintf("/api/groups/%d", g.ID), nil, &grp)
	if len(grp.Members) != 2 {
		t.Fatalf("group has %d members, want 2", len(grp.Members))
	}
	e.must("DELETE", fmt.Sprintf("/api/groups/%d/members/%d", g.ID, c2.ID), nil, nil)
	if err := e.call("DELETE", fmt.Sprintf("/api/groups/%d/members/%d", g.ID, c1.ID), nil, nil); err == nil {
		t.Fatal("removing the last member should be rejected")
	}

	// deleting a client drops it from groups and from the list
	e.must("DELETE", fmt.Sprintf("/api/clients/%d", c2.ID), nil, nil)
	e.must("GET", "/api/clients", nil, &found)
	for _, c := range found {
		if c.ID == c2.ID {
			t.Fatal("deleted client still listed")
		}
	}
}

func TestBookings(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	var c struct{ ID int64 }
	e.must("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "65000009"}, &c)
	pc := e.pc("PC 1")
	now := e.s.now()

	// validation: need a client or group, and PCs
	if err := e.call("POST", "/api/bookings", map[string]any{"start": now.Unix(), "end": now.Add(time.Hour).Unix(), "pcIds": []int64{pc.ID}}, nil); err == nil {
		t.Fatal("booking with no client/group should be rejected")
	}

	// a FUTURE booking shows a tile marker on the free PC
	fStart := now.Add(2 * time.Hour)
	var r1 BookingResult
	e.must("POST", "/api/bookings", map[string]any{"clientId": c.ID, "start": fStart.Unix(), "end": fStart.Add(time.Hour).Unix(), "pcIds": []int64{pc.ID}}, &r1)
	if r1.Booking == nil {
		t.Fatalf("future booking not created (conflicts=%v)", r1.Conflicts)
	}
	eventually(t, "tile shows the booking marker", func() bool {
		p := e.pc("PC 1")
		return p.NextBooking != nil && p.NextBooking.Who == "Merdan"
	})

	// overlapping that window on the same PC warns (no force) and does not save
	var r2 BookingResult
	e.must("POST", "/api/bookings", map[string]any{"clientId": c.ID, "start": fStart.Add(30 * time.Minute).Unix(), "end": fStart.Add(90 * time.Minute).Unix(), "pcIds": []int64{pc.ID}}, &r2)
	if r2.Booking != nil || len(r2.Conflicts) == 0 {
		t.Fatalf("overlap should warn, got booking=%v conflicts=%d", r2.Booking, len(r2.Conflicts))
	}
	// forcing it through saves anyway
	var r3 BookingResult
	e.must("POST", "/api/bookings", map[string]any{"clientId": c.ID, "force": true, "start": fStart.Add(30 * time.Minute).Unix(), "end": fStart.Add(90 * time.Minute).Unix(), "pcIds": []int64{pc.ID}}, &r3)
	if r3.Booking == nil {
		t.Fatal("forced overlapping booking should save")
	}
	e.must("POST", fmt.Sprintf("/api/bookings/%d/cancel", r3.Booking.ID), nil, nil)

	// a DUE booking auto-starts an open session on the PC
	var r4 BookingResult
	e.must("POST", "/api/bookings", map[string]any{"clientId": c.ID, "force": true, "start": now.Unix(), "end": now.Add(time.Hour).Unix(), "pcIds": []int64{pc.ID}}, &r4)
	bid := r4.Booking.ID
	eventually(t, "booking auto-starts an open session", func() bool {
		p := e.pc("PC 1")
		return p.State == "busy" && p.Session != nil && p.Session.mode() == "open" && !e.clientState(0).Locked
	})

	// release as no-show frees the PC
	e.must("POST", fmt.Sprintf("/api/bookings/%d/noshow", bid), nil, nil)
	eventually(t, "no-show frees the PC", func() bool { return e.pc("PC 1").State == "free" })
}

// mode returns the session mode as a string (small test helper).
func (v *SessionView) mode() string { return string(v.Mode) }

// getRaw fetches a path and returns the status code and raw body (for images).
func (e *env) getRaw(path string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.base+path, nil)
	req.Header.Set("X-NextGaming", "1")
	resp, err := e.http.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestFileSharing(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	shared := e.sharedDirs[0]

	// --- Send a file from the front desk into the PC's shared folder ---
	deskDir := t.TempDir()
	src := filepath.Join(deskDir, "install.txt")
	content := []byte("hello from the front desk")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	e.must("POST", "/api/fs/send", map[string]any{"pcId": pc.ID, "srcPath": src, "destRel": ""}, nil)
	eventually(t, "file arrives in the PC shared folder", func() bool {
		b, err := os.ReadFile(filepath.Join(shared, "install.txt"))
		return err == nil && bytes.Equal(b, content)
	})

	// It shows in the PC's shared-folder listing over the control channel.
	var listing protocol.FSListing
	e.must("GET", fmt.Sprintf("/api/fs/pc/%d?rel=", pc.ID), nil, &listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "install.txt" {
		t.Fatalf("PC shared listing = %+v", listing.Entries)
	}

	// The front-desk filesystem is browsable: the source folder lists our file.
	var local LocalListing
	e.must("GET", "/api/fs/local?path="+url.QueryEscape(deskDir), nil, &local)
	if !hasEntry(local.Entries, "install.txt") {
		t.Fatalf("local listing missing install.txt: %+v", local.Entries)
	}

	// --- Get a file from the PC's shared folder back to the front desk ---
	pulled := []byte("a save file from the gaming PC")
	if err := os.WriteFile(filepath.Join(shared, "save.dat"), pulled, 0o644); err != nil {
		t.Fatal(err)
	}
	recvDir := t.TempDir()
	e.must("POST", "/api/fs/get", map[string]any{"pcId": pc.ID, "srcRel": "", "name": "save.dat", "destDir": recvDir}, nil)
	eventually(t, "file arrives on the front desk", func() bool {
		b, err := os.ReadFile(filepath.Join(recvDir, "save.dat"))
		return err == nil && bytes.Equal(b, pulled)
	})

	// Both transfers are recorded as done.
	eventually(t, "two transfers finish", func() bool {
		var tfs []transfer
		if e.call("GET", "/api/fs/transfers", nil, &tfs) != nil || len(tfs) != 2 {
			return false
		}
		for _, tf := range tfs {
			if tf.Status != tfDone {
				return false
			}
		}
		return true
	})

	// --- The shared folder cannot be escaped: ".." resolves back to the root,
	// never a parent directory, so it still shows only our shared file. ---
	var esc protocol.FSListing
	e.must("GET", fmt.Sprintf("/api/fs/pc/%d?rel=%s", pc.ID, url.QueryEscape("../../..")), nil, &esc)
	for _, en := range esc.Entries {
		if en.Name != "install.txt" && en.Name != "save.dat" {
			t.Fatalf("path traversal leaked an entry: %q", en.Name)
		}
	}

	// A transfer to an offline PC fails cleanly.
	e.must("DELETE", fmt.Sprintf("/api/pcs/%d", pc.ID), nil, nil)
	eventually(t, "PC goes offline", func() bool { return !e.s.hub.Online(pc.ID) })
	if err := e.call("POST", "/api/fs/send", map[string]any{"pcId": pc.ID, "srcPath": src, "destRel": ""}, nil); err == nil {
		t.Fatal("sending to an offline PC should fail")
	}
}

func hasEntry(es []localEntry, name string) bool {
	for _, e := range es {
		if e.Name == name {
			return true
		}
	}
	return false
}

func TestCallMatchingAndDuplicates(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)

	var c struct{ ID int64 }
	e.must("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "+99365123456"}, &c)

	// An incoming number in any spelling matches the same client.
	for _, number := range []string{"+99365123456", "+993 65 12 34 56", "065123456", "65123456", "8 65 12 34 56"} {
		got, found, err := e.s.MatchClientByPhone(number)
		if err != nil || !found || got.ID != c.ID {
			t.Fatalf("match %q = (%+v, %v, %v), want client %d", number, got, found, err, c.ID)
		}
	}
	// A number no client has, and an unusable number, do not match.
	for _, number := range []string{"65999999", "123", ""} {
		if _, found, err := e.s.MatchClientByPhone(number); err != nil || found {
			t.Fatalf("match %q unexpectedly found (err=%v)", number, err)
		}
	}

	// No duplicates yet.
	if dups, err := e.s.DuplicateClientPhones(); err != nil || len(dups) != 0 {
		t.Fatalf("unexpected duplicates: %+v (err %v)", dups, err)
	}

	// Force a pre-existing duplicate the API would normally refuse (simulating
	// data from before numbers were unique), and confirm it is reported and
	// that matching stays deterministic (lowest id wins).
	var dupID int64
	if err := e.s.st.Tx(func(q store.Q) error {
		var err error
		dupID, err = store.CreateClient(q, "Merdan (dup)", "+99365123456", "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dups, err := e.s.DuplicateClientPhones()
	if err != nil || len(dups) != 1 || len(dups[0].Clients) != 2 || dups[0].Key != "65123456" {
		t.Fatalf("duplicates = %+v (err %v)", dups, err)
	}
	if got, found, _ := e.s.MatchClientByPhone("65123456"); !found || got.ID != c.ID {
		t.Fatalf("ambiguous match should pick lowest id %d, got %d (found=%v)", c.ID, got.ID, found)
	}
	_ = dupID
}

func TestIncomingCallFlow(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)

	phoneCall := func(number, key string) int {
		req, _ := http.NewRequest("POST", e.base+"/api/phone/call?number="+url.QueryEscape(number), nil)
		req.Header.Set("X-NextGaming-Phone", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Wrong key is rejected.
	if code := phoneCall("+99365123456", "wrong-key"); code != http.StatusForbidden {
		t.Fatalf("wrong key: status %d, want 403", code)
	}

	// An answered call from an unknown number becomes a pending prompt.
	if code := phoneCall("+993 65 12 34 56", e.s.cfg.ClubKey); code != 200 {
		t.Fatalf("call: status %d", code)
	}
	snap := e.snapshot()
	if len(snap.PendingCalls) != 1 || snap.PendingCalls[0].ClientID != 0 || snap.PendingCalls[0].Number != "+99365123456" {
		t.Fatalf("pending calls = %+v", snap.PendingCalls)
	}
	callID := snap.PendingCalls[0].ID

	// Adding the caller as a client resolves the same prompt to them (live match).
	var c struct{ ID int64 }
	e.must("POST", "/api/clients", map[string]string{"name": "Merdan", "phone": "065123456"}, &c)
	snap = e.snapshot()
	if len(snap.PendingCalls) != 1 || snap.PendingCalls[0].ClientID != c.ID || snap.PendingCalls[0].ClientName != "Merdan" {
		t.Fatalf("pending call after add = %+v", snap.PendingCalls)
	}

	// A repeat call from the same number coalesces into one prompt.
	phoneCall("65123456", e.s.cfg.ClubKey)
	if snap = e.snapshot(); len(snap.PendingCalls) != 1 {
		t.Fatalf("repeat call should coalesce, got %d prompts", len(snap.PendingCalls))
	}

	// Dismiss clears it.
	e.must("POST", fmt.Sprintf("/api/calls/%d/dismiss", callID), nil, nil)
	if snap = e.snapshot(); len(snap.PendingCalls) != 0 {
		t.Fatalf("after dismiss: %+v", snap.PendingCalls)
	}

	// An unusable number is refused.
	if code := phoneCall("123", e.s.cfg.ClubKey); code == 200 {
		t.Fatal("unusable number should be rejected")
	}

	// The in-panel simulate endpoint (admin-authed) records a call too.
	e.must("POST", "/api/calls/simulate", map[string]string{"number": "65777777"}, nil)
	if snap = e.snapshot(); len(snap.PendingCalls) != 1 || snap.PendingCalls[0].Number != "+99365777777" {
		t.Fatalf("simulate: %+v", snap.PendingCalls)
	}
}

func TestHDSnapshot(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	local := fmt.Sprintf("http://127.0.0.1:%d", e.ports[0])
	hdFrame := []byte("\xff\xd8\xff\xe0HDSNAPSHOTBYTES\xff\xd9")

	// Stand in for the capture helper: long-poll, and when asked for a frame,
	// echo the snap flag back on the post (as the real helper does).
	go func() {
		for i := 0; i < 60; i++ {
			req, _ := http.NewRequest("GET", local+"/capture-wait", nil)
			req.Header.Set("X-NextGaming-Client", "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				continue
			}
			var cmd struct{ Seq, Width, Quality, Snap int }
			json.NewDecoder(resp.Body).Decode(&cmd)
			resp.Body.Close()
			pr, _ := http.NewRequest("POST", fmt.Sprintf("%s/screen?w=1920&h=1080&snap=%d", local, cmd.Snap), bytes.NewReader(hdFrame))
			pr.Header.Set("X-NextGaming-Client", "1")
			pr.Header.Set("Content-Type", "image/jpeg")
			if rp, err := http.DefaultClient.Do(pr); err == nil {
				rp.Body.Close()
			}
		}
	}()

	// No snapshot before one is requested.
	if code, _ := e.getRaw(fmt.Sprintf("/api/pcs/%d/snapshot.jpg", pc.ID)); code != http.StatusNoContent {
		t.Fatalf("expected 204 before a snapshot request, got %d", code)
	}

	// Request one (works without watching the grid); it arrives on the snapshot slot.
	e.must("POST", fmt.Sprintf("/api/pcs/%d/snapshot", pc.ID), nil, nil)
	eventually(t, "HD snapshot arrives", func() bool {
		code, body := e.getRaw(fmt.Sprintf("/api/pcs/%d/snapshot.jpg", pc.ID))
		return code == 200 && bytes.Equal(body, hdFrame)
	})

	// Requesting a snapshot for an offline PC fails cleanly.
	e.must("DELETE", fmt.Sprintf("/api/pcs/%d", pc.ID), nil, nil)
	eventually(t, "PC offline", func() bool { return !e.s.hub.Online(pc.ID) })
	if err := e.call("POST", fmt.Sprintf("/api/pcs/%d/snapshot", pc.ID), nil, nil); err == nil {
		t.Fatal("snapshot on an offline PC should fail")
	}
}

func TestPhoneCallBrowserGet(t *testing.T) {
	e := setup(t, 0)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)

	get := func(q string) int {
		req, _ := http.NewRequest("GET", e.base+"/api/phone/call?"+q, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A plain browser GET with the key in the URL (no header) works.
	if code := get("number=65121212&key=" + url.QueryEscape(e.s.cfg.ClubKey)); code != 200 {
		t.Fatalf("GET with ?key= : status %d, want 200", code)
	}
	snap := e.snapshot()
	if len(snap.PendingCalls) != 1 || snap.PendingCalls[0].Number != "+99365121212" {
		t.Fatalf("pending after browser GET = %+v", snap.PendingCalls)
	}
	// Wrong key in the URL is refused.
	if code := get("number=65121213&key=nope"); code != http.StatusForbidden {
		t.Fatalf("GET wrong key: status %d, want 403", code)
	}
}

func TestRemoteControlInput(t *testing.T) {
	e := setup(t, 1)
	e.must("POST", "/api/setup", map[string]string{"username": "owner", "password": "secret1"}, nil)
	pc := e.pc("PC 1")
	local := fmt.Sprintf("http://127.0.0.1:%d", e.ports[0])

	// Control is refused while the PC is locked.
	eventually(t, "PC 1 reports locked", func() bool { return e.s.hub.Runtime(pc.ID).Locked })
	if err := e.call("POST", fmt.Sprintf("/api/pcs/%d/control", pc.ID), map[string]any{"active": true}, nil); err == nil {
		t.Fatal("control on a locked PC should be refused")
	}

	// Unlock it with an open session.
	e.must("POST", "/api/shift/open", map[string]any{"openingCash": 5000}, nil)
	e.must("POST", fmt.Sprintf("/api/pcs/%d/start", pc.ID), StartRequest{Mode: core.ModeOpen}, nil)
	eventually(t, "PC 1 unlocks", func() bool { return !e.s.hub.Runtime(pc.ID).Locked })

	// Stand in for the capture helper's input loop: drain /input-wait.
	var mu sync.Mutex
	var got []protocol.InputEvent
	go func() {
		for i := 0; i < 40; i++ {
			req, _ := http.NewRequest("GET", local+"/input-wait", nil)
			req.Header.Set("X-NextGaming-Client", "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				continue
			}
			var b protocol.InputBatch
			json.NewDecoder(resp.Body).Decode(&b)
			resp.Body.Close()
			mu.Lock()
			got = append(got, b.Events...)
			mu.Unlock()
		}
	}()

	// Take control and send a click.
	e.must("POST", fmt.Sprintf("/api/pcs/%d/control", pc.ID), map[string]any{"active": true}, nil)
	e.must("POST", fmt.Sprintf("/api/pcs/%d/control/input", pc.ID), map[string]any{"events": []map[string]any{
		{"kind": "move", "x": 0.5, "y": 0.5},
		{"kind": "down", "button": "left", "x": 0.5, "y": 0.5},
		{"kind": "up", "button": "left", "x": 0.5, "y": 0.5},
		{"kind": "key", "vk": 13, "keyDown": true},
		{"kind": "key", "vk": 13, "keyDown": false},
	}}, nil)

	eventually(t, "the PC receives the input events", func() bool {
		mu.Lock()
		defer mu.Unlock()
		var mouse []protocol.InputEvent // ignore control signals (block/unblock)
		for _, e := range got {
			if e.Kind == "move" || e.Kind == "down" || e.Kind == "up" || e.Kind == "wheel" {
				mouse = append(mouse, e)
			}
		}
		return len(mouse) >= 3 && mouse[0].Kind == "move" && mouse[1].Kind == "down" && mouse[1].Button == "left"
	})

	// A key event (Enter) also reaches the PC.
	mu.Lock()
	var key bool
	for _, e := range got {
		if e.Kind == "key" && e.Vk == 13 && e.Down {
			key = true
		}
	}
	mu.Unlock()
	if !key {
		t.Fatal("expected the key event to reach the PC")
	}

	// Taking control also told the PC to block the player's physical input.
	mu.Lock()
	var blocked bool
	for _, e := range got {
		if e.Kind == "block" {
			blocked = true
		}
	}
	mu.Unlock()
	if !blocked {
		t.Fatal("expected a block signal when control started")
	}

	// Input is refused once control stops.
	e.must("POST", fmt.Sprintf("/api/pcs/%d/control", pc.ID), map[string]any{"active": false}, nil)
	if err := e.call("POST", fmt.Sprintf("/api/pcs/%d/control/input", pc.ID), map[string]any{"events": []map[string]any{{"kind": "move", "x": 0.1, "y": 0.1}}}, nil); err == nil {
		t.Fatal("input after releasing control should be refused")
	}
}
