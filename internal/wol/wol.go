// Package wol sends Wake-on-LAN "magic packets".
//
// A magic packet is 6 bytes of 0xFF followed by the target MAC address
// repeated 16 times, sent as a UDP broadcast. The network card of a PC that
// is switched off (but still has standby power) watches for it and powers
// the PC on.
package wol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ParseMAC accepts "3C-7C-3F-1A-2B-4D", "3c:7c:3f:1a:2b:4d" or "3C7C3F1A2B4D".
func ParseMAC(s string) (net.HardwareAddr, error) {
	clean := strings.NewReplacer("-", "", ":", "", ".", "", " ", "").Replace(strings.TrimSpace(s))
	if len(clean) != 12 {
		return nil, fmt.Errorf("invalid MAC address %q", s)
	}
	var b [6]byte
	for i := 0; i < 6; i++ {
		var v byte
		if _, err := fmt.Sscanf(clean[i*2:i*2+2], "%02x", &v); err != nil {
			return nil, fmt.Errorf("invalid MAC address %q", s)
		}
		b[i] = v
	}
	return net.HardwareAddr(b[:]), nil
}

// FormatMAC renders a MAC the way Windows shows it: 3C-7C-3F-1A-2B-4D.
func FormatMAC(s string) string {
	hw, err := ParseMAC(s)
	if err != nil {
		return strings.ToUpper(s)
	}
	return strings.ToUpper(strings.ReplaceAll(hw.String(), ":", "-"))
}

// MagicPacket builds the 102-byte payload.
func MagicPacket(mac net.HardwareAddr) []byte {
	p := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		p = append(p, 0xFF)
	}
	for i := 0; i < 16; i++ {
		p = append(p, mac...)
	}
	return p
}

// Targets returns every broadcast address the packet should go to:
// the limited broadcast plus the directed broadcast of each local IPv4
// network, on both common WoL ports.
func Targets(extraIPs ...string) []*net.UDPAddr {
	seen := map[string]bool{}
	var out []*net.UDPAddr
	add := func(ip net.IP) {
		for _, port := range []int{9, 7} {
			a := &net.UDPAddr{IP: ip, Port: port}
			if !seen[a.String()] {
				seen[a.String()] = true
				out = append(out, a)
			}
		}
	}
	add(net.IPv4bcast)
	if ifaces, err := net.Interfaces(); err == nil {
		for _, ifc := range ifaces {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok {
					if b := directedBroadcast(n); b != nil {
						add(b)
					}
				}
			}
		}
	}
	// If we know the PC's last IP, also target its /24 broadcast.
	for _, s := range extraIPs {
		if ip := net.ParseIP(s).To4(); ip != nil {
			add(net.IPv4(ip[0], ip[1], ip[2], 255))
		}
	}
	return out
}

func directedBroadcast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	if ip == nil || len(n.Mask) != 4 {
		return nil
	}
	b := make(net.IP, 4)
	for i := range ip {
		b[i] = ip[i] | ^n.Mask[i]
	}
	return b
}

// Send broadcasts the magic packet for mac several times.
// knownIP is the PC's last known IP address (optional).
func Send(mac string, knownIP string) error {
	hw, err := ParseMAC(mac)
	if err != nil {
		return err
	}
	lc := net.ListenConfig{Control: enableBroadcast}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":0")
	if err != nil {
		return err
	}
	defer pc.Close()
	pkt := MagicPacket(hw)
	targets := Targets(knownIP)
	sent := 0
	var lastErr error
	for round := 0; round < 3; round++ {
		for _, t := range targets {
			if _, err := pc.WriteTo(pkt, t); err != nil {
				lastErr = err
			} else {
				sent++
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sent == 0 {
		if lastErr == nil {
			lastErr = errors.New("no network interface could send the packet")
		}
		return fmt.Errorf("wake-on-lan: %w", lastErr)
	}
	return nil
}
