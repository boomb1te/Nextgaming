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
	TypeHello     = "hello"
	TypeHeartbeat = "heartbeat"
	TypeWoLReport = "wol_report"
	TypeEvent     = "event"
	TypeAck       = "ack"

	// server -> agent
	TypeWelcome  = "welcome"
	TypeReject   = "reject"
	TypeState    = "state"
	TypeMessage  = "message"
	TypeShutdown = "shutdown"
	TypeRestart  = "restart"
	TypeWoLCheck = "wol_check"
)

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

// Ack answers a command that has an ID.
type Ack struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
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
const MaxLine = 1 << 20

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
}
