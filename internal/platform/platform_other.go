//go:build !windows

package platform

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"nextgaming/internal/protocol"
)

// Simulated reports that this is not a real Windows agent.
const Simulated = true

// DefaultDataDir is ./nextgaming-agent-data outside Windows.
func DefaultDataDir() string { return "nextgaming-agent-data" }

// GetInventory identifies this machine. NEXTGAMING_MACHINE_ID overrides the
// ID so several simulated agents can run on one development machine.
func GetInventory(dataDir string) Inventory {
	nics := genericNICs()
	inv := Inventory{Hostname: hostname(), BoardMaker: "Simulated", BoardProduct: "Dev machine", NICs: nics}
	if id := os.Getenv("NEXTGAMING_MACHINE_ID"); id != "" {
		inv.MachineID = id
		if h := os.Getenv("NEXTGAMING_HOSTNAME"); h != "" {
			inv.Hostname = h
		} else {
			inv.Hostname += "-" + id
		}
		return inv
	}
	mac := ""
	if len(nics) > 0 {
		mac = nics[0].MAC
	}
	inv.MachineID = makeMachineID(inv.Hostname, mac)
	return inv
}

// NICs lists this machine's network adapters.
func NICs() []protocol.NIC { return genericNICs() }

// Shutdown simulates power off by exiting the agent process.
func Shutdown(restart bool) error {
	if restart {
		log.Printf("SIMULATION: restart requested (exiting; restart the agent by hand)")
	} else {
		log.Printf("SIMULATION: shutdown requested (exiting)")
	}
	go func() { os.Exit(0) }()
	return nil
}

// WoLCheck cannot inspect Wake-on-LAN outside Windows.
func WoLCheck(dataDir string, fix bool) protocol.WoLReport {
	return protocol.WoLReport{Supported: false, Error: "Wake-on-LAN checks need Windows"}
}

// NewLauncher returns a launcher that runs the client as a normal child
// process if the client binary exists (useful with the console simulator);
// otherwise it does nothing.
func NewLauncher(clientPath string, args []string, asSystem bool) Launcher {
	return &procLauncher{path: clientPath, args: args}
}

type procLauncher struct {
	mu   sync.Mutex
	path string
	args []string
	cmd  *exec.Cmd
	done chan struct{}
}

func (l *procLauncher) Ensure() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" {
		return false, ""
	}
	if _, err := os.Stat(l.path); err != nil {
		return false, ""
	}
	restarted := false
	if l.cmd != nil {
		select {
		case <-l.done:
			restarted = true
			l.cmd = nil
		default:
			return false, ""
		}
	}
	cmd := exec.Command(l.path, l.args...)
	cmd.Dir = filepath.Dir(l.path)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return false, ""
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	l.cmd, l.done = cmd, done
	return restarted, "process exited"
}

func (l *procLauncher) Alive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmd == nil {
		// No client binary in simulation mode: report healthy.
		_, err := os.Stat(l.path)
		return l.path == "" || err != nil
	}
	select {
	case <-l.done:
		return false
	default:
		return true
	}
}

func (l *procLauncher) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmd != nil && l.cmd.Process != nil {
		l.cmd.Process.Kill()
	}
}
