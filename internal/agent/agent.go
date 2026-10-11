// Package agent is the NextGaming agent that runs on every gaming PC.
//
// It keeps a connection to the server, holds the authoritative-for-this-PC
// copy of the session timer (so the PC still locks on time if the network
// drops), keeps the lock screen running, and executes power and
// Wake-on-LAN commands.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nextgaming/internal/core"
	"nextgaming/internal/platform"
	"nextgaming/internal/protocol"
	"nextgaming/internal/svcrun"
)

// Version of the agent.
var Version = "1.0.0"

// Config is stored in agent.json in the data directory.
type Config struct {
	// Server is "host:port" of the NextGaming server.
	Server  string `json:"server"`
	ClubKey string `json:"clubKey"`
	// ClientPath is the lock-screen executable. Empty = next to the agent.
	ClientPath string `json:"clientPath"`
	// ClientAsSystem runs the lock screen as SYSTEM so gamers cannot kill it.
	ClientAsSystem bool `json:"clientAsSystem"`
	// LocalPort is where the lock screen talks to the agent (127.0.0.1 only).
	LocalPort int `json:"localPort"`
	// SharedDir is the one folder the front desk can read/write over the LAN
	// (file sharing). Empty = the platform default. The agent never exposes
	// anything outside it.
	SharedDir string `json:"sharedDir,omitempty"`
	// MachineID overrides the hardware-derived ID (simulation and tests only).
	MachineID string `json:"machineId,omitempty"`
	// InstalledAt is set by "install". A PC removed in the admin panel only
	// comes back if the agent was installed again after the removal.
	InstalledAt time.Time `json:"installedAt"`

	dataDir string
}

// LoadConfig reads agent.json.
func LoadConfig(dataDir string) (Config, error) {
	if dataDir == "" {
		dataDir = platform.DefaultDataDir()
	}
	c := Config{ClientAsSystem: true, LocalPort: protocol.DefaultClientPort}
	b, err := os.ReadFile(filepath.Join(dataDir, "agent.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("agent.json: %w", err)
		}
	}
	c.dataDir = dataDir
	if c.LocalPort == 0 {
		c.LocalPort = protocol.DefaultClientPort
	}
	return c, nil
}

// SetDataDir sets where config and state live.
func (c *Config) SetDataDir(d string) { c.dataDir = d }

// DataDir returns the data directory.
func (c Config) DataDir() string { return c.dataDir }

// Save writes agent.json.
func (c Config) Save() error {
	if err := os.MkdirAll(c.dataDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return writeAtomic(filepath.Join(c.dataDir, "agent.json"), b)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// localState is the session as this PC knows it. It is saved to disk so a
// reboot or agent restart during a session does not lose the timer.
type localState struct {
	Locked    bool   `json:"locked"`
	Paused    bool   `json:"paused"`
	SessionID int64  `json:"sessionId"`
	Mode      string `json:"mode"`
	// Prepaid: wall-clock deadline. Open/free: the moment the count-up started.
	Deadline    time.Time `json:"deadline"`
	CountFrom   time.Time `json:"countFrom"`
	PCName      string    `json:"pcName"`
	ClubName    string    `json:"clubName"`
	WarnMinutes []int     `json:"warnMinutes"`
	// Pricing for the player's timer.
	TariffName       string `json:"tariffName"`
	RatePerHour      int64  `json:"ratePerHour"`
	Paid             int64  `json:"paid"`
	Currency         string `json:"currency"`
	GraceSeconds     int    `json:"graceSeconds"`
	MinChargeMinutes int    `json:"minChargeMinutes"`
	// WasStopped is set when someone stopped the agent service; it is reported
	// to the server (and cleared) on the next connection.
	WasStopped bool `json:"wasStopped,omitempty"`
}

// Agent is the running agent.
type Agent struct {
	cfg      Config
	inv      platform.Inventory
	launcher platform.Launcher

	capLauncher platform.Launcher

	sharedDir string // the one folder exposed to the front desk (file sharing)

	mu        sync.Mutex
	st        localState
	conn      *protocol.Conn
	online    bool
	expiredID int64 // session we already reported as locally expired
	autoFix   bool
	message   string
	messageID int64
	messageAt time.Time

	// staff-login requests we sent to the server, awaiting the server's ack
	pending map[string]chan protocol.Ack
	reqSeq  int64

	// screen capture: the server asks for a frame, the lock screen long-polls
	// capWake and grabs one. capWake is closed (and replaced) on each request.
	capMu   sync.Mutex
	capSeq  int64
	capW    int
	capQ    int
	capSnap bool
	capWake chan struct{}

	now func() time.Time
}

// New prepares the agent.
func New(cfg Config) *Agent {
	a := &Agent{cfg: cfg, now: time.Now, pending: map[string]chan protocol.Ack{}, capWake: make(chan struct{})}
	a.st = localState{Locked: true}
	a.loadState()
	return a
}

func (a *Agent) statePath() string { return filepath.Join(a.cfg.dataDir, "agent-state.json") }

func (a *Agent) loadState() {
	b, err := os.ReadFile(a.statePath())
	if err != nil {
		return
	}
	var st localState
	if json.Unmarshal(b, &st) == nil {
		a.st = st
	}
}

func (a *Agent) saveStateLocked() {
	b, _ := json.Marshal(a.st)
	if err := os.MkdirAll(a.cfg.dataDir, 0o755); err == nil {
		writeAtomic(a.statePath(), b)
	}
}

// Run runs the agent until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.Server == "" || a.cfg.ClubKey == "" {
		return errors.New("agent is not configured: run 'nextgaming-agent install -server <address> -key <club key>'")
	}
	a.sharedDir = a.cfg.SharedDir
	if a.sharedDir == "" {
		a.sharedDir = platform.DefaultSharedDir()
	}
	if err := os.MkdirAll(a.sharedDir, 0o755); err != nil {
		log.Printf("shared folder %s: %v (file sharing disabled)", a.sharedDir, err)
		a.sharedDir = ""
	} else {
		log.Printf("shared folder: %s", a.sharedDir)
	}

	a.inv = platform.GetInventory(a.cfg.dataDir)
	if a.cfg.MachineID != "" {
		a.inv.MachineID = a.cfg.MachineID
		a.inv.Hostname = a.inv.Hostname + "-" + a.cfg.MachineID
	} else {
		a.inv.MachineID = a.stableID(a.inv)
	}
	log.Printf("agent %s: machine %s (%s), board %s %s", Version, a.inv.MachineID, a.inv.Hostname, a.inv.BoardMaker, a.inv.BoardProduct)

	clientPath := a.cfg.ClientPath
	if clientPath == "" {
		exe, _ := os.Executable()
		name := "nextgaming-client"
		if filepath.Ext(exe) == ".exe" {
			name += ".exe"
		}
		clientPath = filepath.Join(filepath.Dir(exe), name)
	}
	a.launcher = platform.NewLauncher(clientPath, []string{"-port", fmt.Sprint(a.cfg.LocalPort)}, a.cfg.ClientAsSystem)
	defer a.launcher.Stop()
	// Screen capture must run as the logged-in user: a SYSTEM process's GDI
	// screen grab of the composited desktop comes back black. So the lock
	// screen stays SYSTEM (anti-kill) and a separate helper (-capture), run as
	// the user, does the capturing. Both launchers are built before any
	// watchdog starts, so the shared "kill leftover clients" step cannot kill a
	// freshly started one.
	a.capLauncher = platform.NewLauncher(clientPath, []string{"-port", fmt.Sprint(a.cfg.LocalPort), "-capture"}, false)
	defer a.capLauncher.Stop()

	if err := a.serveLocal(ctx); err != nil {
		return err
	}
	go a.watchdog(ctx)
	go a.captureWatchdog(ctx)
	go a.tick(ctx)
	a.connectLoop(ctx)
	if svcrun.StopRequested() {
		a.mu.Lock()
		a.st.WasStopped = true
		a.saveStateLocked()
		a.mu.Unlock()
	}
	return nil
}

// SelfUninstall removes this agent from the PC. A variable so tests can replace it.
var SelfUninstall = platform.SelfUninstall

// identity is the machine ID saved on first run.
type identity struct {
	MachineID string `json:"machineId"`
	MAC       string `json:"mac"`
}

// stableID keeps the PC's identity the same across reboots, even if during
// boot the network card is not ready yet or a VPN adapter appears. The saved
// ID is dropped only when its network card is gone (e.g. the disk was cloned
// into another PC), so cloned PCs still get their own IDs.
func (a *Agent) stableID(inv platform.Inventory) string {
	path := filepath.Join(a.cfg.dataDir, "identity.json")
	var saved identity
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &saved) == nil && saved.MachineID != "" {
		present := len(inv.NICs) == 0 // nothing detected yet: trust the saved ID
		for _, n := range inv.NICs {
			if strings.EqualFold(n.MAC, saved.MAC) {
				present = true
			}
		}
		if present || saved.MAC == "" {
			return saved.MachineID
		}
		log.Printf("identity: network card %s is gone; this looks like a different PC, using a new ID", saved.MAC)
	}
	if inv.MachineID != "" && inv.IDMAC != "" {
		b, _ := json.Marshal(identity{MachineID: inv.MachineID, MAC: inv.IDMAC})
		os.MkdirAll(a.cfg.dataDir, 0o755)
		writeAtomic(path, b)
	}
	return inv.MachineID
}

// watchdog keeps the lock screen alive and reports if it was killed.
func (a *Agent) watchdog(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		if restarted, detail := a.launcher.Ensure(); restarted {
			log.Printf("lock screen died (%s); restarted", detail)
			a.sendEvent(protocol.Event{Kind: protocol.EventClientDied, Detail: detail})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// captureWatchdog keeps the user-level screen-capture helper running. Unlike
// the lock screen, its death is not a security event, so it is simply
// restarted without alerting the admin.
func (a *Agent) captureWatchdog(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if a.capLauncher != nil {
			a.capLauncher.Ensure()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick enforces the local timer: a prepaid session locks at its deadline
// even when the server cannot be reached.
func (a *Agent) tick(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		expired := !a.st.Locked && a.st.Mode == "prepaid" && !a.now().Before(a.st.Deadline)
		var sid int64
		if expired {
			a.st.Locked = true
			sid = a.st.SessionID
			a.saveStateLocked()
			log.Printf("session %d: time is up, locking", sid)
		}
		report := expired && a.expiredID != sid
		if report {
			a.expiredID = sid
		}
		a.mu.Unlock()
		if report {
			a.sendEvent(protocol.Event{Kind: protocol.EventLocalExpired, SessionID: sid})
		}
	}
}

// connectLoop keeps the server connection up, reconnecting with backoff.
func (a *Agent) connectLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		log.Printf("server connection ended: %v (retrying in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) session(ctx context.Context) error {
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", a.cfg.Server)
	if err != nil {
		return err
	}
	defer raw.Close()
	go func() {
		<-ctx.Done()
		raw.Close()
	}()
	conn := protocol.NewConn(raw)

	a.mu.Lock()
	hello := protocol.Hello{
		ProtocolVersion: protocol.Version, ClubKey: a.cfg.ClubKey, MachineID: a.inv.MachineID,
		Hostname: a.inv.Hostname, AgentVersion: Version, NICs: platform.NICs(),
		BoardMaker: a.inv.BoardMaker, BoardProduct: a.inv.BoardProduct,
		Locked: a.st.Locked, SessionID: a.st.SessionID,
		InstalledAt: a.cfg.InstalledAt,
	}
	a.mu.Unlock()
	if err := conn.Send(protocol.TypeHello, "", hello); err != nil {
		return err
	}
	raw.SetReadDeadline(time.Now().Add(15 * time.Second))
	env, err := conn.Recv()
	if err != nil {
		return err
	}
	switch env.Type {
	case protocol.TypeReject:
		rej, _ := protocol.Decode[protocol.Reject](env)
		if rej.Reason == protocol.RejectRemoved {
			log.Printf("this PC was removed in the admin panel; uninstalling NextGaming")
			SelfUninstall()
			<-ctx.Done()
			return ctx.Err()
		}
		time.Sleep(20 * time.Second) // a wrong key will not fix itself quickly
		return fmt.Errorf("server refused connection: %s", rej.Reason)
	case protocol.TypeWelcome:
	default:
		return fmt.Errorf("unexpected %q from server", env.Type)
	}
	w, _ := protocol.Decode[protocol.Welcome](env)
	log.Printf("connected to %s as %s (%s)", a.cfg.Server, w.PCName, w.ClubName)

	a.mu.Lock()
	a.conn, a.online, a.autoFix = conn, true, w.AutoFixWoL
	a.st.PCName, a.st.ClubName = w.PCName, w.ClubName
	wasStopped := a.st.WasStopped
	if wasStopped {
		a.st.WasStopped = false
		a.saveStateLocked()
	}
	a.mu.Unlock()
	if wasStopped {
		conn.Send(protocol.TypeEvent, "", protocol.Event{Kind: protocol.EventAgentStopped})
	}
	defer func() {
		a.mu.Lock()
		a.conn, a.online = nil, false
		for id, ch := range a.pending {
			ch <- protocol.Ack{OK: false, Error: "lost connection to the server"}
			delete(a.pending, id)
		}
		a.mu.Unlock()
	}()

	go a.reportWoL(conn, "", w.AutoFixWoL)

	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go a.heartbeat(hbCtx, conn)

	for {
		raw.SetReadDeadline(time.Now().Add(90 * time.Second))
		env, err := conn.Recv()
		if err != nil {
			return err
		}
		a.handle(conn, env)
	}
}

func (a *Agent) heartbeat(ctx context.Context, conn *protocol.Conn) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		a.mu.Lock()
		hb := protocol.Heartbeat{Locked: a.st.Locked, SessionID: a.st.SessionID, RemainingSec: a.remainingLocked()}
		a.mu.Unlock()
		hb.ClientAlive = a.launcher.Alive()
		if err := conn.Send(protocol.TypeHeartbeat, "", hb); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) handle(conn *protocol.Conn, env protocol.Envelope) {
	ack := func(err error) {
		if env.ID == "" {
			return
		}
		r := protocol.Ack{OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		conn.Send(protocol.TypeAck, env.ID, r)
	}
	switch env.Type {
	case protocol.TypeAck:
		if r, err := protocol.Decode[protocol.Ack](env); err == nil {
			a.resolvePending(env.ID, r)
		}
		return
	case protocol.TypeState:
		ds, err := protocol.Decode[protocol.DesiredState](env)
		if err == nil {
			a.apply(ds)
		}
		ack(err)
	case protocol.TypeMessage:
		m, err := protocol.Decode[protocol.Message](env)
		if err == nil {
			a.mu.Lock()
			a.message, a.messageAt = m.Text, a.now()
			a.messageID++
			a.mu.Unlock()
		}
		ack(err)
	case protocol.TypeShutdown, protocol.TypeRestart:
		ack(nil)
		time.Sleep(300 * time.Millisecond) // let the ack leave
		if err := platform.Shutdown(env.Type == protocol.TypeRestart); err != nil {
			log.Printf("power command failed: %v", err)
		}
	case protocol.TypeUninstall:
		ack(nil)
		log.Printf("this PC was removed in the admin panel; uninstalling NextGaming")
		time.Sleep(300 * time.Millisecond)
		SelfUninstall()
	case protocol.TypeWoLCheck:
		chk, _ := protocol.Decode[protocol.WoLCheck](env)
		go a.reportWoL(conn, env.ID, chk.Fix)
	case protocol.TypeCapture:
		req, err := protocol.Decode[protocol.CaptureRequest](env)
		if err == nil {
			a.requestCapture(req.Width, req.Quality, req.Snap)
		}
	case protocol.TypeFSList:
		req, _ := protocol.Decode[protocol.FSListRequest](env)
		listing, err := a.fsList(req.Rel)
		if env.ID == "" {
			return
		}
		if err != nil {
			conn.Send(protocol.TypeAck, env.ID, protocol.Ack{OK: false, Error: err.Error()})
			return
		}
		data, _ := json.Marshal(listing)
		conn.Send(protocol.TypeAck, env.ID, protocol.Ack{OK: true, Data: data})
	case protocol.TypeFSPush:
		req, err := protocol.Decode[protocol.FSPush](env)
		ack(err)
		if err == nil {
			go a.fsPush(req)
		}
	case protocol.TypeFSPull:
		req, err := protocol.Decode[protocol.FSPull](env)
		ack(err)
		if err == nil {
			go a.fsPull(req)
		}
	}
}

// requestCapture records the server's capture parameters and wakes the lock
// screen's /capture-wait long-poll so it grabs a frame immediately.
func (a *Agent) requestCapture(width, quality int, snap bool) {
	if width <= 0 {
		return
	}
	a.capMu.Lock()
	a.capSeq++
	a.capW, a.capQ, a.capSnap = width, quality, snap
	old := a.capWake
	a.capWake = make(chan struct{})
	a.capMu.Unlock()
	close(old) // release every waiting /capture-wait at once
}

// sendScreen relays one captured frame from the lock screen to the server.
func (a *Agent) sendScreen(data []byte, w, h int, snap bool) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn != nil {
		conn.Send(protocol.TypeScreenFrame, "", protocol.ScreenFrame{Data: data, W: w, H: h, Snap: snap})
	}
}

// reportWoL runs the Wake-on-LAN check and sends the result, then acks.
func (a *Agent) reportWoL(conn *protocol.Conn, ackID string, fix bool) {
	rep := platform.WoLCheck(a.cfg.dataDir, fix)
	if err := conn.Send(protocol.TypeWoLReport, "", rep); err != nil {
		// The adapter may have restarted after a fix; the next connect re-reports.
		return
	}
	if ackID != "" {
		r := protocol.Ack{OK: true}
		if rep.Error != "" {
			r = protocol.Ack{OK: false, Error: rep.Error}
		}
		conn.Send(protocol.TypeAck, ackID, r)
	}
}

// apply adopts the server's desired state.
func (a *Agent) apply(ds protocol.DesiredState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	prevLocked := a.st.Locked
	a.st.Locked = ds.Locked
	a.st.Paused = ds.Paused
	a.st.SessionID = ds.SessionID
	a.st.Mode = ds.Mode
	a.st.PCName, a.st.ClubName, a.st.WarnMinutes = ds.PCName, ds.ClubName, ds.WarnMinutes
	a.st.TariffName, a.st.RatePerHour, a.st.Paid, a.st.Currency = ds.TariffName, ds.RatePerHour, ds.Paid, ds.Currency
	a.st.GraceSeconds, a.st.MinChargeMinutes = ds.GraceSeconds, ds.MinChargeMinutes
	a.st.Deadline, a.st.CountFrom = time.Time{}, time.Time{}
	if !ds.Locked {
		if ds.Mode == "prepaid" {
			a.st.Deadline = now.Add(time.Duration(ds.RemainingSec) * time.Second)
		} else {
			a.st.CountFrom = now.Add(-time.Duration(ds.ElapsedSec) * time.Second)
		}
	}
	if ds.Locked {
		a.expiredID = 0
	}
	a.saveStateLocked()
	if prevLocked != ds.Locked {
		log.Printf("%s: now %s (session %d, %s)", ds.PCName, map[bool]string{true: "LOCKED", false: "UNLOCKED"}[ds.Locked], ds.SessionID, ds.Mode)
	}
}

func (a *Agent) remainingLocked() int64 {
	if a.st.Locked || a.st.Mode != "prepaid" {
		return 0
	}
	r := int64(a.st.Deadline.Sub(a.now()).Round(time.Second) / time.Second)
	if r < 0 {
		return 0
	}
	return r
}

func (a *Agent) sendEvent(ev protocol.Event) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn != nil {
		conn.Send(protocol.TypeEvent, "", ev)
	}
}

// ClientState is what the lock screen shows right now.
func (a *Agent) ClientState() protocol.ClientState {
	a.mu.Lock()
	defer a.mu.Unlock()
	cs := protocol.ClientState{
		Locked: a.st.Locked, Paused: a.st.Paused, Mode: a.st.Mode, SessionID: a.st.SessionID,
		PCName: a.st.PCName, ClubName: a.st.ClubName, ServerOnline: a.online,
		WarnMinutes: a.st.WarnMinutes, Message: a.message, MessageID: a.messageID,
	}
	if cs.PCName == "" {
		cs.PCName = a.inv.Hostname
	}
	cs.TariffName, cs.RatePerHour, cs.Currency = a.st.TariffName, a.st.RatePerHour, a.st.Currency
	if !a.st.Locked {
		if a.st.Mode == "prepaid" {
			cs.RemainingSec = a.remainingLocked()
		} else if !a.st.CountFrom.IsZero() {
			cs.ElapsedSec = int64(a.now().Sub(a.st.CountFrom) / time.Second)
		}
	}
	switch a.st.Mode {
	case "prepaid":
		cs.Cost = a.st.Paid
	case "open":
		cs.Cost = int64(core.OpenCharge(core.Money(a.st.RatePerHour), cs.ElapsedSec, a.st.GraceSeconds, a.st.MinChargeMinutes))
	}
	return cs
}

// errNotConnected is returned when the player tries to end a session offline.
var errNotConnected = errors.New("not connected to the club server; please ask the administrator")

func (a *Agent) resolvePending(id string, r protocol.Ack) {
	a.mu.Lock()
	ch := a.pending[id]
	delete(a.pending, id)
	a.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// StaffLogin sends staff credentials to the server; on success the server
// unlocks this PC as a staff session. Returns the server's answer.
func (a *Agent) StaffLogin(username, password string) error {
	a.mu.Lock()
	conn := a.conn
	if conn == nil {
		a.mu.Unlock()
		return errNotConnected
	}
	a.reqSeq++
	id := "sl" + fmt.Sprint(a.reqSeq)
	ch := make(chan protocol.Ack, 1)
	a.pending[id] = ch
	a.mu.Unlock()

	if err := conn.Send(protocol.TypeStaffLogin, id, protocol.StaffLogin{Username: username, Password: password}); err != nil {
		a.resolvePending(id, protocol.Ack{})
		return errNotConnected
	}
	select {
	case r := <-ch:
		if !r.OK {
			if r.Error == "" {
				r.Error = "login failed"
			}
			return errors.New(r.Error)
		}
		return nil
	case <-time.After(8 * time.Second):
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return errors.New("the server did not answer in time")
	}
}

// PlayerLogout is called when the player presses "End session".
func (a *Agent) PlayerLogout(sessionID int64) error {
	a.mu.Lock()
	conn, locked, sid := a.conn, a.st.Locked, a.st.SessionID
	a.mu.Unlock()
	if locked || sid == 0 || sid != sessionID {
		return errors.New("no active session")
	}
	if conn == nil {
		return errNotConnected
	}
	return conn.Send(protocol.TypeEvent, "", protocol.Event{Kind: protocol.EventUserLogout, SessionID: sid})
}
