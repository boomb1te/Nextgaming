package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"nextgaming/internal/protocol"
)

// Heartbeat timing. Agents send a heartbeat every HeartbeatEvery; a PC that
// has been silent for OfflineAfter is considered offline.
const (
	HeartbeatEvery = 3 * time.Second
	OfflineAfter   = 12 * time.Second
)

// agentConn is one connected gaming PC.
type agentConn struct {
	pcID     int64
	conn     *protocol.Conn
	raw      net.Conn
	ip       string
	lastSeen atomic.Int64 // unix nanos
	hb       atomic.Pointer[protocol.Heartbeat]

	pmu     sync.Mutex
	pending map[string]chan protocol.Ack
}

// Runtime is the live (not persisted) status of a PC.
type Runtime struct {
	Online      bool      `json:"online"`
	LastSeen    time.Time `json:"lastSeen"`
	IP          string    `json:"ip"`
	Locked      bool      `json:"locked"`
	ClientAlive bool      `json:"clientAlive"`
}

// Hub accepts agent connections and routes commands to them.
type Hub struct {
	srv *Server

	mu       sync.RWMutex
	conns    map[int64]*agentConn
	lastSeen map[int64]time.Time
	cmdSeq   atomic.Int64
}

func newHub(s *Server) *Hub {
	return &Hub{srv: s, conns: map[int64]*agentConn{}, lastSeen: map[int64]time.Time{}}
}

// Listen accepts agents until ctx is cancelled.
func (h *Hub) Listen(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("agent listener %s: %w", addr, err)
	}
	log.Printf("agents: listening on %s", addr)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go h.watchdog(ctx)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("agents: accept: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go h.serve(ctx, c)
	}
}

// watchdog drops agents that stopped sending heartbeats.
func (h *Hub) watchdog(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now().UnixNano()
		h.mu.RLock()
		var stale []*agentConn
		for _, ac := range h.conns {
			if time.Duration(now-ac.lastSeen.Load()) > OfflineAfter {
				stale = append(stale, ac)
			}
		}
		h.mu.RUnlock()
		for _, ac := range stale {
			log.Printf("agents: PC %d silent for %s, dropping", ac.pcID, OfflineAfter)
			ac.raw.Close()
		}
	}
}

func (h *Hub) serve(ctx context.Context, raw net.Conn) {
	defer raw.Close()
	if tc, ok := raw.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(10 * time.Second)
		tc.SetNoDelay(true)
	}
	conn := protocol.NewConn(raw)
	ip, _, _ := net.SplitHostPort(raw.RemoteAddr().String())

	raw.SetReadDeadline(time.Now().Add(10 * time.Second))
	env, err := conn.Recv()
	if err != nil || env.Type != protocol.TypeHello {
		return
	}
	hello, err := protocol.Decode[protocol.Hello](env)
	if err != nil {
		return
	}
	if hello.ProtocolVersion != protocol.Version {
		conn.Send(protocol.TypeReject, "", protocol.Reject{Reason: fmt.Sprintf("protocol version %d not supported (server speaks %d); update the agent", hello.ProtocolVersion, protocol.Version)})
		return
	}
	if subtle.ConstantTimeCompare([]byte(hello.ClubKey), []byte(h.srv.cfg.ClubKey)) != 1 {
		log.Printf("agents: rejected %s: wrong club key", ip)
		conn.Send(protocol.TypeReject, "", protocol.Reject{Reason: "wrong club key"})
		time.Sleep(time.Second) // slow down guessing
		return
	}
	if hello.MachineID == "" {
		conn.Send(protocol.TypeReject, "", protocol.Reject{Reason: "missing machine id"})
		return
	}

	pc, err := h.srv.registerAgent(hello, ip)
	if err != nil {
		log.Printf("agents: register %s: %v", ip, err)
		conn.Send(protocol.TypeReject, "", protocol.Reject{Reason: "server error"})
		return
	}
	ac := &agentConn{pcID: pc.ID, conn: conn, raw: raw, ip: ip, pending: map[string]chan protocol.Ack{}}
	ac.lastSeen.Store(time.Now().UnixNano())
	ac.hb.Store(&protocol.Heartbeat{Locked: hello.Locked, SessionID: hello.SessionID})

	h.mu.Lock()
	if old := h.conns[pc.ID]; old != nil {
		old.raw.Close() // a PC reconnected before its old connection timed out
	}
	h.conns[pc.ID] = ac
	h.mu.Unlock()
	log.Printf("agents: %s connected from %s (%s)", pc.Name, ip, hello.Hostname)

	defer func() {
		h.mu.Lock()
		if h.conns[pc.ID] == ac {
			delete(h.conns, pc.ID)
			h.lastSeen[pc.ID] = time.Now()
		}
		h.mu.Unlock()
		ac.failPending()
		log.Printf("agents: PC %d disconnected", pc.ID)
		h.srv.agentDisconnected(pc.ID)
	}()

	settings := h.srv.Settings()
	if err := conn.Send(protocol.TypeWelcome, "", protocol.Welcome{
		PCID: pc.ID, PCName: pc.Name, ClubName: settings.ClubName,
		ServerTime: time.Now(), AutoFixWoL: settings.AutoFixWoL,
	}); err != nil {
		return
	}
	h.srv.agentConnected(pc.ID)

	for {
		raw.SetReadDeadline(time.Now().Add(OfflineAfter + 5*time.Second))
		env, err := conn.Recv()
		if err != nil {
			return
		}
		ac.lastSeen.Store(time.Now().UnixNano())
		switch env.Type {
		case protocol.TypeHeartbeat:
			hb, err := protocol.Decode[protocol.Heartbeat](env)
			if err == nil {
				prev := ac.hb.Swap(&hb)
				if prev == nil || prev.Locked != hb.Locked || prev.ClientAlive != hb.ClientAlive {
					h.srv.changed()
				}
			}
		case protocol.TypeEvent:
			if ev, err := protocol.Decode[protocol.Event](env); err == nil {
				h.srv.agentEvent(pc.ID, ev)
			}
		case protocol.TypeWoLReport:
			if rep, err := protocol.Decode[protocol.WoLReport](env); err == nil {
				h.srv.wolReport(pc.ID, rep, env.Data)
			}
		case protocol.TypeAck:
			if a, err := protocol.Decode[protocol.Ack](env); err == nil {
				ac.resolve(env.ID, a)
			}
		}
	}
}

func (ac *agentConn) resolve(id string, a protocol.Ack) {
	ac.pmu.Lock()
	ch := ac.pending[id]
	delete(ac.pending, id)
	ac.pmu.Unlock()
	if ch != nil {
		ch <- a
	}
}

func (ac *agentConn) failPending() {
	ac.pmu.Lock()
	for id, ch := range ac.pending {
		ch <- protocol.Ack{OK: false, Error: "PC disconnected"}
		delete(ac.pending, id)
	}
	ac.pmu.Unlock()
}

// ErrOffline is returned when a command targets a PC that is not connected.
var ErrOffline = errors.New("PC is offline")

// Send pushes a message without waiting for an answer.
func (h *Hub) Send(pcID int64, typ string, v any) error {
	h.mu.RLock()
	ac := h.conns[pcID]
	h.mu.RUnlock()
	if ac == nil {
		return ErrOffline
	}
	return ac.conn.Send(typ, "", v)
}

// Command sends a message and waits for the agent's acknowledgement.
func (h *Hub) Command(pcID int64, typ string, v any, timeout time.Duration) error {
	h.mu.RLock()
	ac := h.conns[pcID]
	h.mu.RUnlock()
	if ac == nil {
		return ErrOffline
	}
	id := strconv.FormatInt(h.cmdSeq.Add(1), 10)
	ch := make(chan protocol.Ack, 1)
	ac.pmu.Lock()
	ac.pending[id] = ch
	ac.pmu.Unlock()
	if err := ac.conn.Send(typ, id, v); err != nil {
		ac.resolve(id, protocol.Ack{})
		return err
	}
	select {
	case a := <-ch:
		if !a.OK {
			if a.Error == "" {
				a.Error = "command failed"
			}
			return errors.New(a.Error)
		}
		return nil
	case <-time.After(timeout):
		ac.pmu.Lock()
		delete(ac.pending, id)
		ac.pmu.Unlock()
		return errors.New("PC did not answer in time")
	}
}

// Runtime returns the live status of a PC.
func (h *Hub) Runtime(pcID int64) Runtime {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ac := h.conns[pcID]
	if ac == nil {
		return Runtime{Online: false, LastSeen: h.lastSeen[pcID], Locked: true}
	}
	rt := Runtime{Online: true, LastSeen: time.Unix(0, ac.lastSeen.Load()), IP: ac.ip, Locked: true}
	if hb := ac.hb.Load(); hb != nil {
		rt.Locked = hb.Locked
		rt.ClientAlive = hb.ClientAlive
	}
	return rt
}

// Online reports whether a PC is connected.
func (h *Hub) Online(pcID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[pcID] != nil
}

// Disconnect closes a PC's connection (used when a PC is deleted).
func (h *Hub) Disconnect(pcID int64) {
	h.mu.RLock()
	ac := h.conns[pcID]
	h.mu.RUnlock()
	if ac != nil {
		ac.raw.Close()
	}
}
