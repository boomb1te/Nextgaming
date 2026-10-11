package agent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nextgaming/internal/protocol"
)

// File sharing (LAN only). The agent exposes exactly one folder — a.sharedDir —
// and never anything outside it. Listings travel on the control connection;
// the bytes move over a plain HTTP request to the server, so a big transfer
// does not block heartbeats or screen frames.

// errNoShare is returned when file sharing is not available on this PC.
var errNoShare = errors.New("file sharing is not available on this PC")

// fsResolve turns a path relative to the shared root into an absolute path and
// guarantees it stays inside the shared folder (no "..", no absolute escape, no
// symlink break-out).
func (a *Agent) fsResolve(rel string) (string, error) {
	if a.sharedDir == "" {
		return "", errNoShare
	}
	root, err := filepath.Abs(a.sharedDir)
	if err != nil {
		return "", errNoShare
	}
	// Normalise separators and clean as if rooted, so ".." can never climb above root.
	rel = strings.ReplaceAll(rel, "\\", "/")
	clean := filepath.Clean("/" + rel) // always starts with "/", no ".." left
	full := filepath.Join(root, filepath.FromSlash(clean))
	// Final containment check against the real path.
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", errors.New("path is outside the shared folder")
	}
	return full, nil
}

// fsList lists one folder inside the shared folder.
func (a *Agent) fsList(rel string) (protocol.FSListing, error) {
	dir, err := a.fsResolve(rel)
	if err != nil {
		return protocol.FSListing{}, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) && rel == "" {
			// The shared root was removed under us; recreate it empty.
			os.MkdirAll(dir, 0o755)
			return protocol.FSListing{Rel: ""}, nil
		}
		return protocol.FSListing{}, errors.New("cannot read the folder")
	}
	out := protocol.FSListing{Rel: strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/"), Entries: []protocol.FSEntry{}}
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Skip symlinks entirely: they could point outside the shared folder.
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		out.Entries = append(out.Entries, protocol.FSEntry{
			Name: e.Name(), IsDir: e.IsDir(), Size: info.Size(), ModUnix: info.ModTime().Unix(),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].IsDir != out.Entries[j].IsDir {
			return out.Entries[i].IsDir // folders first
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

// serverURL builds a URL to the server's HTTP port from the agent's view of the
// server host and the port the command carried.
func (a *Agent) serverURL(port int, path string) string {
	host, _, err := net.SplitHostPort(a.cfg.Server)
	if err != nil || host == "" {
		host = a.cfg.Server
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, path)
}

var fsHTTP = &http.Client{Timeout: 30 * time.Minute}

// fsPush downloads one file from the server into the shared folder.
func (a *Agent) fsPush(req protocol.FSPush) {
	n, err := a.fsPushDo(req)
	a.fsResult(protocol.FSResult{Token: req.Token, OK: err == nil, Bytes: n, Error: errStr(err)})
}

func (a *Agent) fsPushDo(req protocol.FSPush) (int64, error) {
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(req.Name, "\\", "/")))
	if name == "" || name == "." || strings.ContainsRune(name, os.PathSeparator) {
		return 0, errors.New("bad file name")
	}
	dir, err := a.fsResolve(req.Rel)
	if err != nil {
		return 0, err
	}
	dest, err := a.fsResolve(filepath.Join(strings.Trim(strings.ReplaceAll(req.Rel, "\\", "/"), "/"), name))
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	hr, err := http.NewRequest("GET", a.serverURL(req.Port, req.Path), nil)
	if err != nil {
		return 0, err
	}
	hr.Header.Set("X-NextGaming-Agent", a.cfg.ClubKey)
	resp, err := fsHTTP.Do(hr)
	if err != nil {
		return 0, errors.New("could not reach the server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server refused the transfer (%d)", resp.StatusCode)
	}

	tmp := dest + ".ngpart"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	cerr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return 0, errors.New("the transfer was interrupted")
	}
	if cerr != nil {
		os.Remove(tmp)
		return 0, cerr
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// fsPull uploads one file from the shared folder to the server.
func (a *Agent) fsPull(req protocol.FSPull) {
	n, err := a.fsPullDo(req)
	a.fsResult(protocol.FSResult{Token: req.Token, OK: err == nil, Bytes: n, Error: errStr(err)})
}

func (a *Agent) fsPullDo(req protocol.FSPull) (int64, error) {
	name := filepath.Base(filepath.FromSlash(strings.ReplaceAll(req.Name, "\\", "/")))
	src, err := a.fsResolve(filepath.Join(strings.Trim(req.Rel, "/"), name))
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return 0, errors.New("file not found in the shared folder")
	}
	f, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	hr, err := http.NewRequest("POST", a.serverURL(req.Port, req.Path), f)
	if err != nil {
		return 0, err
	}
	hr.Header.Set("X-NextGaming-Agent", a.cfg.ClubKey)
	hr.Header.Set("Content-Type", "application/octet-stream")
	hr.ContentLength = info.Size()
	resp, err := fsHTTP.Do(hr)
	if err != nil {
		return 0, errors.New("could not reach the server")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the server rejected the file (%d)", resp.StatusCode)
	}
	return info.Size(), nil
}

// fsResult reports a finished transfer to the server.
func (a *Agent) fsResult(res protocol.FSResult) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn != nil {
		conn.Send(protocol.TypeFSResult, "", res)
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
