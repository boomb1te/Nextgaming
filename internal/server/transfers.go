package server

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"nextgaming/internal/protocol"
)

// Transfer directions, as shown in the panel.
const (
	dirToPC   = "to_pc"   // front desk -> gaming PC
	dirFromPC = "from_pc" // gaming PC -> front desk
)

// Transfer statuses.
const (
	tfActive    = "active"
	tfDone      = "done"
	tfError     = "error"
	tfCancelled = "cancelled"
)

// transfer is one in-flight or finished file copy.
type transfer struct {
	Token   string `json:"token"`
	PCID    int64  `json:"pcId"`
	PCName  string `json:"pcName"`
	Dir     string `json:"dir"`  // dirToPC | dirFromPC
	Name    string `json:"name"` // file name
	Size    int64  `json:"size"` // bytes, -1 if unknown
	Done    int64  `json:"done"` // bytes moved so far
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs,omitempty"`

	// localPath is the file on the server PC: the source to serve (dirToPC) or
	// the destination to write (dirFromPC). Not sent to the panel.
	localPath string
	started   bool // the agent-facing HTTP endpoint has been hit at least once
}

// transfers tracks active and recent file transfers.
type transfers struct {
	srv *Server
	mu  sync.Mutex
	m   map[string]*transfer
}

func newTransfers(s *Server) *transfers { return &transfers{srv: s, m: map[string]*transfer{}} }

func newToken() string {
	var b [18]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// create registers a new transfer and returns it.
func (t *transfers) create(dir string, pcID int64, pcName, name string, size int64, localPath string) *transfer {
	tf := &transfer{
		Token: newToken(), PCID: pcID, PCName: pcName, Dir: dir, Name: name,
		Size: size, Status: tfActive, StartMs: time.Now().UnixMilli(), localPath: localPath,
	}
	t.mu.Lock()
	t.m[tf.Token] = tf
	t.prune()
	t.mu.Unlock()
	return tf
}

func (t *transfers) get(token string) *transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[token]
}

// progress adds n bytes to a transfer's running total (called from the
// agent-facing HTTP endpoints as the bytes flow).
func (t *transfers) progress(token string, done int64) {
	t.mu.Lock()
	if tf := t.m[token]; tf != nil {
		tf.started = true
		tf.Done = done
	}
	t.mu.Unlock()
	t.srv.changed()
}

// finish marks a transfer complete (from the server HTTP side, dirFromPC).
func (t *transfers) finish(token string, ok bool, errMsg string, bytes int64) {
	t.mu.Lock()
	tf := t.m[token]
	if tf != nil && tf.Status == tfActive {
		if ok {
			tf.Status, tf.Done = tfDone, bytes
			if tf.Size < 0 {
				tf.Size = bytes
			}
		} else {
			tf.Status, tf.Error = tfError, errMsg
		}
		tf.EndMs = time.Now().UnixMilli()
	}
	t.mu.Unlock()
	t.srv.changed()
}

// agentDone records the agent's own report that a transfer finished. For
// dirToPC (the agent downloaded) this is authoritative; for dirFromPC the HTTP
// upload already set the result, so an agent error only downgrades a success.
func (t *transfers) agentDone(res protocol.FSResult) {
	t.mu.Lock()
	tf := t.m[res.Token]
	if tf != nil {
		switch {
		case !res.OK && tf.Status != tfDone:
			tf.Status, tf.Error = tfError, res.Error
			tf.EndMs = time.Now().UnixMilli()
		case !res.OK && tf.Status == tfDone:
			// upload reached us but the PC side still failed overall
			tf.Status, tf.Error = tfError, res.Error
		case res.OK && tf.Status == tfActive:
			tf.Status, tf.Done = tfDone, res.Bytes
			if tf.Size < 0 {
				tf.Size = res.Bytes
			}
			tf.EndMs = time.Now().UnixMilli()
		}
	}
	t.mu.Unlock()
	t.srv.changed()
}

// list returns a snapshot of transfers, newest first.
func (t *transfers) list() []transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]transfer, 0, len(t.m))
	for _, tf := range t.m {
		out = append(out, *tf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMs > out[j].StartMs })
	return out
}

// dropPC cancels any active transfers for a PC that just went offline.
func (t *transfers) dropPC(pcID int64) {
	t.mu.Lock()
	for _, tf := range t.m {
		if tf.PCID == pcID && tf.Status == tfActive {
			tf.Status, tf.Error = tfError, "PC went offline during the transfer"
			tf.EndMs = time.Now().UnixMilli()
		}
	}
	t.mu.Unlock()
}

// prune keeps the finished list from growing without bound. Caller holds mu.
func (t *transfers) prune() {
	const keepFinished = 40
	var finished []*transfer
	for _, tf := range t.m {
		if tf.Status != tfActive {
			finished = append(finished, tf)
		}
	}
	if len(finished) <= keepFinished {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].EndMs > finished[j].EndMs })
	for _, tf := range finished[keepFinished:] {
		delete(t.m, tf.Token)
	}
}
