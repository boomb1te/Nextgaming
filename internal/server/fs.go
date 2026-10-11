package server

import (
	"crypto/subtle"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"nextgaming/internal/protocol"
	"nextgaming/internal/store"
)

// File sharing (LAN only — never exposed to any future remote/online build).
//
// The front desk (this server's own PC) can browse its whole filesystem; the
// operator is trusted and it is their own machine. Each gaming PC exposes only
// one fixed shared folder — the agent refuses anything outside it. The file
// bytes move over a direct HTTP request between the agent and this server; the
// listings and the commands travel on the agent control connection.

const fsTransferTimeout = 10 * time.Second // time allowed for the agent to accept a command

// localEntry is one item in a front-desk (server PC) folder listing.
type localEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModUnix int64  `json:"modUnix"`
}

// LocalListing is a folder on the front-desk PC.
type LocalListing struct {
	Path    string       `json:"path"`   // "" at the drive/root list
	Parent  string       `json:"parent"` // "" when there is no parent (at the root list)
	AtRoot  bool         `json:"atRoot"`
	Entries []localEntry `json:"entries"`
}

// ListLocal lists a folder on the server PC. An empty path lists the drives
// (Windows) or the filesystem root and home (elsewhere).
func (s *Server) ListLocal(path string) (LocalListing, error) {
	if path == "" {
		return s.localRoots(), nil
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return LocalListing{}, errBad("cannot open that folder")
	}
	if !info.IsDir() {
		return LocalListing{}, errBad("that is a file, not a folder")
	}
	ents, err := os.ReadDir(path)
	if err != nil {
		return LocalListing{}, errBad("cannot read that folder (permission denied?)")
	}
	out := LocalListing{Path: path, Parent: localParent(path), Entries: []localEntry{}}
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		out.Entries = append(out.Entries, localEntry{
			Name: e.Name(), Path: filepath.Join(path, e.Name()), IsDir: e.IsDir(),
			Size: fi.Size(), ModUnix: fi.ModTime().Unix(),
		})
	}
	sortLocal(out.Entries)
	return out, nil
}

func (s *Server) localRoots() LocalListing {
	out := LocalListing{AtRoot: true, Entries: []localEntry{}}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			root := string(c) + `:\`
			if _, err := os.Stat(root); err == nil {
				out.Entries = append(out.Entries, localEntry{Name: root, Path: root, IsDir: true})
			}
		}
		return out
	}
	out.Entries = append(out.Entries, localEntry{Name: "/", Path: "/", IsDir: true})
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out.Entries = append(out.Entries, localEntry{Name: "Home (" + home + ")", Path: home, IsDir: true})
	}
	return out
}

// localParent returns the parent folder, or "" to fall back to the root list.
func localParent(path string) string {
	parent := filepath.Dir(path)
	if parent == path {
		return "" // already a root ("C:\" or "/")
	}
	// A drive root like "C:\" has Dir "C:\" on Windows but "C:." elsewhere; the
	// equality check above handles the real-OS case. Keep it simple otherwise.
	return parent
}

func sortLocal(es []localEntry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

// ListPCShared asks a gaming PC's agent for a listing of its shared folder.
func (s *Server) ListPCShared(pcID int64, rel string) (protocol.FSListing, error) {
	if !s.hub.Online(pcID) {
		return protocol.FSListing{}, errConflict("%s is offline.", s.pcName(pcID))
	}
	ack, err := s.hub.Query(pcID, protocol.TypeFSList, protocol.FSListRequest{Rel: rel}, fsTransferTimeout)
	if err != nil {
		return protocol.FSListing{}, errConflict("%s", err.Error())
	}
	return protocol.Decode[protocol.FSListing](protocol.Envelope{Data: ack.Data})
}

// httpPort is the port the agent should call back on for file bytes.
func (s *Server) httpPort() int { return portOf(s.cfg.HTTPAddr, protocol.DefaultHTTPPort) }

// SendToPC copies a file from the front-desk PC into a gaming PC's shared
// folder. srcPath is on the server PC; destRel is a subfolder within shared.
func (s *Server) SendToPC(adminID, pcID int64, srcPath, destRel string) (*transfer, error) {
	if !s.hub.Online(pcID) {
		return nil, errConflict("%s is offline.", s.pcName(pcID))
	}
	info, err := os.Stat(srcPath)
	if err != nil || info.IsDir() {
		return nil, errBad("pick a file to send.")
	}
	name := filepath.Base(srcPath)
	tf := s.transfers.create(dirToPC, pcID, s.pcName(pcID), name, info.Size(), srcPath)
	cmd := protocol.FSPush{
		Token: tf.Token, Port: s.httpPort(), Path: "/api/fs/serve/" + tf.Token,
		Rel: destRel, Name: name, Size: info.Size(),
	}
	if err := s.hub.Send(pcID, protocol.TypeFSPush, cmd); err != nil {
		s.transfers.finish(tf.Token, false, "could not reach the PC", 0)
		return nil, errConflict("could not reach %s.", tf.PCName)
	}
	s.auditFS(adminID, pcID, "file_send", name)
	return tf, nil
}

// GetFromPC copies a file from a gaming PC's shared folder onto the front-desk
// PC. destDir is a folder on the server PC; name/srcRel locate the file.
func (s *Server) GetFromPC(adminID, pcID int64, srcRel, name, destDir string) (*transfer, error) {
	if !s.hub.Online(pcID) {
		return nil, errConflict("%s is offline.", s.pcName(pcID))
	}
	di, err := os.Stat(destDir)
	if err != nil || !di.IsDir() {
		return nil, errBad("pick a folder on this PC to save into.")
	}
	base := filepath.Base(name)
	if base == "" || base == "." {
		return nil, errBad("bad file name.")
	}
	dest := uniquePath(filepath.Join(destDir, base))
	tf := s.transfers.create(dirFromPC, pcID, s.pcName(pcID), base, -1, dest)
	cmd := protocol.FSPull{
		Token: tf.Token, Port: s.httpPort(), Path: "/api/fs/recv/" + tf.Token,
		Rel: srcRel, Name: base,
	}
	if err := s.hub.Send(pcID, protocol.TypeFSPull, cmd); err != nil {
		s.transfers.finish(tf.Token, false, "could not reach the PC", 0)
		return nil, errConflict("could not reach %s.", tf.PCName)
	}
	s.auditFS(adminID, pcID, "file_get", base)
	return tf, nil
}

func (s *Server) auditFS(adminID, pcID int64, action, detail string) {
	s.st.Tx(func(q store.Q) error { return store.Audit(q, adminID, action, pcID, detail) })
}

// uniquePath appends " (2)", " (3)"… before the extension if the path exists,
// so a transfer never silently overwrites a file on the front-desk PC.
func uniquePath(p string) string {
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; i < 1000; i++ {
		cand := base + " (" + itoa(i) + ")" + ext
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
	return p
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

// ---- agent-facing HTTP endpoints (club key + token, no admin cookie) ----

func (s *Server) agentAuthed(r *http.Request) bool {
	key := r.Header.Get("X-NextGaming-Agent")
	return subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.ClubKey)) == 1
}

// handleFSServe streams a staged file to the agent (direction: to the PC).
func (s *Server) handleFSServe(w http.ResponseWriter, r *http.Request) {
	if !s.agentAuthed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tf := s.transfers.get(r.PathValue("token"))
	if tf == nil || tf.Dir != dirToPC {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	f, err := os.Open(tf.localPath)
	if err != nil {
		s.transfers.finish(tf.Token, false, "source file is gone", 0)
		http.Error(w, "gone", http.StatusGone)
		return
	}
	defer f.Close()
	if tf.Size >= 0 {
		w.Header().Set("Content-Length", itoa(int(tf.Size)))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	cw := &countWriter{w: w, token: tf.Token, t: s.transfers}
	io.Copy(cw, f)
}

// handleFSRecv receives a file uploaded by the agent (direction: from the PC)
// and writes it to the staged destination on the front-desk PC.
func (s *Server) handleFSRecv(w http.ResponseWriter, r *http.Request) {
	if !s.agentAuthed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tf := s.transfers.get(r.PathValue("token"))
	if tf == nil || tf.Dir != dirFromPC {
		http.Error(w, "no such transfer", http.StatusNotFound)
		return
	}
	tmp := tf.localPath + ".ngpart"
	f, err := os.Create(tmp)
	if err != nil {
		s.transfers.finish(tf.Token, false, "cannot write to the chosen folder", 0)
		http.Error(w, "cannot write", http.StatusInternalServerError)
		return
	}
	cr := &countReader{r: r.Body, token: tf.Token, t: s.transfers}
	n, err := io.Copy(f, cr)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		s.transfers.finish(tf.Token, false, "the transfer was interrupted", 0)
		http.Error(w, "interrupted", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmp, tf.localPath); err != nil {
		os.Remove(tmp)
		s.transfers.finish(tf.Token, false, "cannot save the file", 0)
		http.Error(w, "cannot save", http.StatusInternalServerError)
		return
	}
	s.transfers.finish(tf.Token, true, "", n)
	writeJSON(w, http.StatusOK, ok())
}

// countWriter / countReader report byte progress as the transfer streams.
type countWriter struct {
	w     io.Writer
	n     int64
	token string
	t     *transfers
	last  time.Time
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.tick()
	return n, err
}

type countReader struct {
	r     io.Reader
	n     int64
	token string
	t     *transfers
	last  time.Time
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.tickR()
	return n, err
}

func (c *countWriter) tick() {
	if time.Since(c.last) < 300*time.Millisecond {
		return
	}
	c.last = time.Now()
	c.t.progress(c.token, c.n)
}
func (c *countReader) tickR() {
	if time.Since(c.last) < 300*time.Millisecond {
		return
	}
	c.last = time.Now()
	c.t.progress(c.token, c.n)
}
