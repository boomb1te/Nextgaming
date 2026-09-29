package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"nextgaming/internal/protocol"
)

// Config is read from server.json in the data directory.
type Config struct {
	// HTTPAddr is where the admin panel listens. ":8080" makes it reachable
	// from other devices on the club network (e.g. the owner's phone).
	HTTPAddr string `json:"httpAddr"`
	// AgentAddr is where gaming-PC agents connect.
	AgentAddr string `json:"agentAddr"`
	// ClubKey is the shared secret agents must present. Generated on first run.
	ClubKey string `json:"clubKey"`
	// BackupKeep is how many daily backups to keep.
	BackupKeep int `json:"backupKeep"`

	dataDir string
}

// DataDir is where the database, backups and config live.
func (c Config) DataDir() string { return c.dataDir }

// DefaultDataDir is C:\ProgramData\NextGaming\server on Windows and
// ./nextgaming-data elsewhere (development).
func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "NextGaming", "server")
	}
	return "nextgaming-data"
}

// LoadConfig reads server.json, creating it with defaults on first run.
func LoadConfig(dataDir string) (Config, error) {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return Config{}, err
	}
	path := filepath.Join(dataDir, "server.json")
	c := Config{
		HTTPAddr:   fmt.Sprintf(":%d", protocol.DefaultHTTPPort),
		AgentAddr:  fmt.Sprintf(":%d", protocol.DefaultAgentPort),
		BackupKeep: 30,
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// first run
	default:
		return c, err
	}
	c.dataDir = dataDir
	if c.ClubKey == "" {
		c.ClubKey = NewClubKey()
		if err := c.Save(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Save writes server.json.
func (c Config) Save() error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(c.dataDir, "server.json"), b)
}

// NewClubKey returns a key like "K7P2-9QXA-M3TD" (no ambiguous characters).
func NewClubKey() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 14)
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(v)%len(alphabet)])
	}
	return string(out)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
