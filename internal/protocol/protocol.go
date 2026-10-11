// Package protocol defines the messages exchanged between the NextGaming
// server and the agent on each gaming PC, and between the agent and the
// lock-screen client running on the same PC.
//
// Server <-> agent: a TCP connection carrying one JSON Envelope per line.
// The server is the source of truth: it pushes the full desired PC state
// (DesiredState) whenever it changes and on every (re)connect, so a
// reconnecting agent always resynchronises.
package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Version is bumped when the wire format changes incompatibly.
const Version = 1

// Default ports.
const (
	DefaultAgentPort  = 47810 // server listens here for agents
	DefaultHTTPPort   = 8080  // admin panel
	DefaultClientPort = 47812 // agent listens on 127.0.0.1 for the lock screen
)

// Message types.
const (
	// agent -> server
	TypeHello      = "hello"
	TypeHeartbeat  = "heartbeat"
	TypeWoLReport   = "wol_report"
	TypeEvent       = "event"
	TypeAck         = "ack"
	TypeStaffLogin  = "staff_login"  // agent -> server: unlock this PC for staff after a password
	TypeScreenFrame = "screen_frame" // agent -> server: a captured screen thumbnail
	TypeFSResult    = "fs_result"    // agent -> server: a file transfer finished

	// server -> agent
	TypeWelcome   = "welcome"
	TypeReject    = "reject"
	TypeState     = "state"
	TypeMessage   = "message"
	TypeShutdown  = "shutdown"
	TypeRestart   = "restart"
	TypeWoLCheck  = "wol_check"
	TypeUninstall = "uninstall" // the PC was removed in the admin panel
	TypeCapture   = "capture"   // capture one screen frame now (screen monitoring)
	TypeFSList    = "fs_list"   // list the PC's shared folder (reply carries the listing in the Ack)
	TypeFSPush    = "fs_push"   // download a file from the server into the shared folder
	TypeFSPull    = "fs_pull"   // upload a file from the shared folder to the server
)

// RejectRemoved is the Reject reason for a PC that was removed in the admin panel.
const RejectRemoved = "removed"

// Envelope wraps every message on the wire.
type Envelope struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"` // correlates commands with acks
	Data json.RawMessage `json:"data,omitempty"`
}

// NIC describes a network adapter reported by the agent.
type NIC struct {
	Name  string `json:"name"`
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
	Wired bool   `json:"wired"`
}

// Hello is the first message an agent sends.
type Hello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	ClubKey         string `json:"clubKey"`
	MachineID       string `json:"machineId"`
	Hostname        string `json:"hostname"`
	AgentVersion    string `json:"agentVersion"`
	NICs            []NIC  `json:"nics"`
	BoardMaker      string `json:"boardMaker"`
	BoardProduct    string `json:"boardProduct"`
	Locked          bool   `json:"locked"`
	SessionID       int64  `json:"sessionId"`
	// InstalledAt is when the agent was (re)installed. A PC removed in the
	// admin panel comes back only if its agent was installed after the removal.
	InstalledAt time.Time `json:"installedAt"`
}

// Welcome answers a valid Hello.
type Welcome struct {
	PCID       int64     `json:"pcId"`
	PCName     string    `json:"pcName"`
	ClubName   string    `json:"clubName"`
	ServerTime time.Time `json:"serverTime"`
	AutoFixWoL bool      `json:"autoFixWol"`
}

// Reject refuses a connection (wrong club key, protocol mismatch).
type Reject struct {
	Reason string `json:"reason"`
}

// DesiredState is the full state the server wants the PC to be in.
// Times are sent as durations, never absolute clock values, so a PC with a
// wrong clock still counts correctly.
type DesiredState struct {
	Locked    bool   `json:"locked"`
	SessionID int64  `json:"sessionId"`
	Mode      string `json:"mode"`   // prepaid | open | free | "" when locked
	Paused    bool   `json:"paused"` // locked because the session is paused
	// RemainingSec for prepaid sessions, ElapsedSec for open/free sessions.
	RemainingSec int64  `json:"remainingSec"`
	ElapsedSec   int64  `json:"elapsedSec"`
	PCName       string `json:"pcName"`
	ClubName     string `json:"clubName"`
	WarnMinutes  []int  `json:"warnMinutes"`
	// Pricing shown on the player's timer.
	TariffName       string `json:"tariffName,omitempty"`
	RatePerHour      int64  `json:"ratePerHour,omitempty"` // minor units
	Paid             int64  `json:"paid,omitempty"`        // prepaid: money taken so far
	Currency         string `json:"currency,omitempty"`
	GraceSeconds     int    `json:"graceSeconds,omitempty"`
	MinChargeMinutes int    `json:"minChargeMinutes,omitempty"`
}

// Heartbeat is sent by the agent every few seconds.
type Heartbeat struct {
	Locked       bool  `json:"locked"`
	SessionID    int64 `json:"sessionId"`
	RemainingSec int64 `json:"remainingSec"`
	ClientAlive  bool  `json:"clientAlive"` // lock screen process is running
}

// Event kinds sent by the agent.
const (
	EventLocalExpired = "local_expired" // agent locked because its local timer ran out
	EventUserLogout   = "user_logout"   // player pressed "End session" on the PC
	EventClientDied   = "client_died"   // lock screen was killed and relaunched
	EventAgentStopped = "agent_stopped" // the agent service was stopped by someone and has started again
)

// Event is something that happened on the PC.
type Event struct {
	Kind      string `json:"kind"`
	SessionID int64  `json:"sessionId"`
	Detail    string `json:"detail,omitempty"`
}

// Message is text the admin wants shown on the PC.
type Message struct {
	Text string `json:"text"`
}

// Ack answers a command that has an ID. Data carries an optional payload for
// commands that return something (e.g. a folder listing for TypeFSList).
type Ack struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ---- file sharing (LAN only) ----
//
// Each gaming PC exposes exactly one fixed "shared" folder; the agent never
// reveals anything outside it. Directory listings travel on this control
// connection (small JSON); the file bytes themselves move over a separate HTTP
// request between the agent and the server, so a large transfer never blocks
// heartbeats or screen frames.

// FSListRequest asks the agent to list a folder inside its shared folder.
// Rel is a path relative to the shared root ("" = the root); the agent rejects
// anything that would escape the shared folder.
type FSListRequest struct {
	Rel string `json:"rel"`
}

// FSEntry is one item in a shared-folder listing.
type FSEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"modUnix"`
}

// FSListing is the agent's reply to FSListRequest (carried in Ack.Data).
type FSListing struct {
	Rel     string    `json:"rel"`
	Entries []FSEntry `json:"entries"`
}

// FSPush tells the agent to download one file from the server into its shared
// folder. The agent GETs http://<server>:<Port><Path>, authenticating with the
// club key, and saves the body as Rel/Name inside the shared folder.
type FSPush struct {
	Token string `json:"token"`
	Port  int    `json:"port"`
	Path  string `json:"path"`
	Rel   string `json:"rel"`  // destination subfolder within shared ("" = root)
	Name  string `json:"name"` // destination file name
	Size  int64  `json:"size"`
}

// FSPull tells the agent to upload one file from its shared folder to the
// server. The agent POSTs the file at Rel/Name to http://<server>:<Port><Path>.
type FSPull struct {
	Token string `json:"token"`
	Port  int    `json:"port"`
	Path  string `json:"path"`
	Rel   string `json:"rel"`  // source subfolder within shared ("" = root)
	Name  string `json:"name"` // source file name
}

// FSResult is the agent's report that a transfer finished (or failed).
type FSResult struct {
	Token string `json:"token"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Bytes int64  `json:"bytes"`
}

// StaffLogin is sent by the agent when staff enter their password on the lock
// screen. The server checks it and, if valid, starts a free (staff) session on
// that PC. It replies with an Ack (ok, or an error message).
type StaffLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// CaptureRequest tells the agent to have the lock screen grab one frame of the
// screen, downscaled to Width pixels wide and JPEG-encoded at Quality (1–100).
// The server broadcasts it to every online PC on a shared tick, so all PCs
// capture at the same moment while the monitoring tab is open.
type CaptureRequest struct {
	Width   int `json:"width"`
	Quality int `json:"quality"`
}

// ScreenFrame is one captured screen, relayed agent -> server. Data is a JPEG
// (base64 on the wire). W and H are the encoded pixel size.
type ScreenFrame struct {
	Data []byte `json:"data"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// WoLCheck asks the agent to check (and optionally fix) its Wake-on-LAN setup.
type WoLCheck struct {
	Fix bool `json:"fix"`
}

// WoLSetting is one checked Windows setting.
type WoLSetting struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	OK      bool   `json:"ok"`
	Fixed   bool   `json:"fixed"`
	Fixable bool   `json:"fixable"`
	Advice  string `json:"advice,omitempty"`
}

// WoLReport is the agent's view of Wake-on-LAN readiness on the Windows side.
// BIOS settings cannot be read from Windows; they are verified by a real wake test.
type WoLReport struct {
	Supported    bool         `json:"supported"` // false on non-Windows / simulated agents
	Wired        bool         `json:"wired"`
	Adapter      string       `json:"adapter"`
	MAC          string       `json:"mac"`
	WindowsOK    bool         `json:"windowsOk"`
	Settings     []WoLSetting `json:"settings"`
	FixedAny     bool         `json:"fixedAny"`
	BoardMaker   string       `json:"boardMaker"`
	BoardProduct string       `json:"boardProduct"`
	Error        string       `json:"error,omitempty"`
}

// ---- wire helpers ----

// MaxLine bounds a single message so a broken peer cannot exhaust memory.
// It must comfortably fit a JPEG screen frame (base64-encoded) from the
// focused-PC view, which can run to a few hundred KB.
const MaxLine = 8 << 20

// Encode builds an Envelope.
func Encode(typ, id string, v any) (Envelope, error) {
	env := Envelope{Type: typ, ID: id}
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return env, err
		}
		env.Data = b
	}
	return env, nil
}

// Decode unmarshals an envelope payload.
func Decode[T any](env Envelope) (T, error) {
	var v T
	if len(env.Data) == 0 {
		return v, nil
	}
	err := json.Unmarshal(env.Data, &v)
	return v, err
}

// Conn is a line-delimited JSON connection safe for one reader and many writers.
type Conn struct {
	rw  io.ReadWriteCloser
	sc  *bufio.Scanner
	wmu sync.Mutex
}

// NewConn wraps a network connection.
func NewConn(rw io.ReadWriteCloser) *Conn {
	sc := bufio.NewScanner(rw)
	sc.Buffer(make([]byte, 64*1024), MaxLine)
	return &Conn{rw: rw, sc: sc}
}

// Send writes one message.
func (c *Conn) Send(typ, id string, v any) error {
	env, err := Encode(typ, id, v)
	if err != nil {
		return err
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.rw.Write(b)
	return err
}

// Recv reads the next message.
func (c *Conn) Recv() (Envelope, error) {
	var env Envelope
	if !c.sc.Scan() {
		if err := c.sc.Err(); err != nil {
			return env, err
		}
		return env, io.EOF
	}
	if err := json.Unmarshal(c.sc.Bytes(), &env); err != nil {
		return env, errors.New("malformed message: " + err.Error())
	}
	return env, nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.rw.Close() }

// ---- agent <-> lock screen (local HTTP on 127.0.0.1) ----

// ClientState is what the lock screen polls from the agent.
type ClientState struct {
	Locked       bool   `json:"locked"`
	Paused       bool   `json:"paused"`
	Mode         string `json:"mode"`
	SessionID    int64  `json:"sessionId"`
	RemainingSec int64  `json:"remainingSec"`
	ElapsedSec   int64  `json:"elapsedSec"`
	PCName       string `json:"pcName"`
	ClubName     string `json:"clubName"`
	ServerOnline bool   `json:"serverOnline"`
	WarnMinutes  []int  `json:"warnMinutes"`
	// Message is the latest admin message; MessageID increases with each new one.
	Message   string `json:"message"`
	MessageID int64  `json:"messageId"`
	// Pricing for the timer: rate per hour and the session's cost so far
	// (prepaid: what was paid; open: what it costs if ended now). Minor units.
	TariffName  string `json:"tariffName"`
	RatePerHour int64  `json:"ratePerHour"`
	Cost        int64  `json:"cost"`
	Currency    string `json:"currency"`
}
