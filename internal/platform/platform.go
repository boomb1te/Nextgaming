// Package platform contains everything the agent does to the operating
// system: identifying the PC, powering it off, checking Wake-on-LAN and
// keeping the lock screen running. The Windows implementation is the real
// one; other systems get a simulation so the whole system can be developed
// and tested on macOS/Linux.
package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"sort"
	"strings"

	"nextgaming/internal/protocol"
)

// Inventory describes this PC.
type Inventory struct {
	MachineID    string
	Hostname     string
	BoardMaker   string
	BoardProduct string
	NICs         []protocol.NIC
}

// Launcher keeps the lock-screen program running.
type Launcher interface {
	// Ensure starts the client if it is not running. It reports whether it had
	// to restart a client that died unexpectedly, with a short explanation.
	Ensure() (restarted bool, detail string)
	// Alive reports whether the client is running.
	Alive() bool
	// Stop terminates the client.
	Stop()
}

// makeMachineID derives a stable ID from hardware so that PCs installed from
// the same cloned disk image still get different IDs.
func makeMachineID(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(keep, "|")))
	return hex.EncodeToString(sum[:12])
}

// genericNICs lists network adapters using only the standard library.
// Wired detection is a name heuristic; Windows overrides this with the real adapter type.
func genericNICs() []protocol.NIC {
	var out []protocol.NIC
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != 6 {
			continue
		}
		n := protocol.NIC{Name: ifc.Name, MAC: strings.ToUpper(strings.ReplaceAll(ifc.HardwareAddr.String(), ":", "-"))}
		name := strings.ToLower(ifc.Name)
		n.Wired = strings.HasPrefix(name, "eth") || strings.HasPrefix(name, "en") || strings.Contains(name, "ethernet")
		if strings.HasPrefix(name, "wl") || strings.Contains(name, "wi-fi") || strings.Contains(name, "wlan") {
			n.Wired = false
		}
		if ifc.Flags&net.FlagUp != 0 {
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
					n.IP = ipn.IP.String()
					break
				}
			}
		}
		out = append(out, n)
	}
	sortNICs(out)
	return out
}

// sortNICs puts the most likely "real" adapter first: wired with an IP.
func sortNICs(n []protocol.NIC) {
	score := func(x protocol.NIC) int {
		s := 0
		if x.Wired {
			s += 2
		}
		if x.IP != "" {
			s += 1
		}
		return s
	}
	sort.SliceStable(n, func(i, j int) bool { return score(n[i]) > score(n[j]) })
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
