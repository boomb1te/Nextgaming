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
	"sync"
	"time"

	"nextgaming/internal/platform"
	"nextgaming/internal/protocol"
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
	// MachineID overrides the hardware-derived ID (simulation and tests only).
	MachineID string `json:"machineId,omitempty"`

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
}

// Agent is the running agent.
type Agent struct {
	cfg      Config
	inv      platform.Inventory
	launcher platform.Launcher

	mu        sync.Mutex
	st        localState
	conn      *protocol.Conn
	online    bool
	expiredID int64 // session we already reported as locally expired
	autoFix   bool
	message   string
	messageID int64
	messageAt time.Time

	now func() time.Time
}

// New prepares the agent.
func New(cfg Config) *Agent {
	a := &Agent{cfg: cfg, now: time.Now}
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
	a.inv = platform.GetInventory(a.cfg.dataDir)
	if a.cfg.MachineID != "" {
		a.inv.MachineID = a.cfg.MachineID
		a.inv.Hostname = a.inv.Hostname + "-" + a.cfg.MachineID
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

	if err := a.serveLocal(ctx); err != nil {
		return err
	}
	go a.watchdog(ctx)
	go a.tick(ctx)
	a.connectLoop(ctx)
	return nil
}

// watchdog keeps the lock screen alive and reports if it was killed.
func (a *Agent) watchdog(ctx context.Context) {
	t := time.NewTicker(time.Second)
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
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.conn, a.online = nil, false
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
	case protocol.TypeWoLCheck:
		chk, _ := protocol.Decode[protocol.WoLCheck](env)
		go a.reportWoL(conn, env.ID, chk.Fix)
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
	if !a.st.Locked {
		if a.st.Mode == "prepaid" {
			cs.RemainingSec = a.remainingLocked()
		} else if !a.st.CountFrom.IsZero() {
			cs.ElapsedSec = int64(a.now().Sub(a.st.CountFrom) / time.Second)
		}
	}
	return cs
}

// errNotConnected is returned when the player tries to end a session offline.
var errNotConnected = errors.New("not connected to the club server; please ask the administrator")

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
