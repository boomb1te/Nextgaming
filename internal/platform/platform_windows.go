//go:build windows

package platform

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"nextgaming/internal/protocol"
)

// Simulated is false on Windows: this is the real agent.
const Simulated = false

const createNoWindow = 0x08000000

// DefaultDataDir is C:\ProgramData\NextGaming\agent.
func DefaultDataDir() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "NextGaming", "agent")
}

// DefaultSharedDir is the one folder on this PC that the front desk can read
// and write over the LAN. It is a plain, easy-to-find path so staff can drop
// files into it in Explorer.
func DefaultSharedDir() string {
	sd := os.Getenv("SystemDrive")
	if sd == "" {
		sd = "C:"
	}
	return filepath.Join(sd+`\`, "NextGaming", "Shared")
}

// powershell runs a script file and returns its stdout.
func powershell(ctx context.Context, script string, args ...string) ([]byte, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(exe); err != nil {
		exe = "powershell.exe"
	}
	full := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}, args...)
	cmd := exec.CommandContext(ctx, exe, full...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("powershell: %s", msg)
	}
	return out.Bytes(), nil
}

// writeScript saves an embedded PowerShell script into the data directory.
func writeScript(dataDir, name, body string) (string, error) {
	dir := filepath.Join(dataDir, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	// UTF-8 with BOM so Windows PowerShell 5.1 reads non-ASCII correctly.
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(body)...)
	return p, os.WriteFile(p, data, 0o644)
}

const inventoryScript = `$ErrorActionPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$p = Get-CimInstance Win32_ComputerSystemProduct
$b = Get-CimInstance Win32_BaseBoard
[ordered]@{
  uuid = "$($p.UUID)".Trim()
  boardMaker = "$($b.Manufacturer)".Trim()
  boardProduct = "$($b.Product)".Trim()
} | ConvertTo-Json -Compress
`

// GetInventory identifies this PC (hardware UUID + primary MAC).
func GetInventory(dataDir string) Inventory {
	inv := Inventory{Hostname: hostname(), NICs: windowsNICs()}
	var hw struct {
		UUID         string `json:"uuid"`
		BoardMaker   string `json:"boardMaker"`
		BoardProduct string `json:"boardProduct"`
	}
	if script, err := writeScript(dataDir, "inventory.ps1", inventoryScript); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := powershell(ctx, script)
		cancel()
		if err == nil {
			json.Unmarshal(bytes.TrimSpace(out), &hw)
		}
	}
	inv.BoardMaker, inv.BoardProduct = hw.BoardMaker, hw.BoardProduct
	// The ID uses the lowest wired MAC: stable no matter which adapter is
	// connected or got an address first during boot.
	mac := ""
	for _, n := range inv.NICs {
		if n.Wired && (mac == "" || n.MAC < mac) {
			mac = n.MAC
		}
	}
	if mac == "" {
		for _, n := range inv.NICs {
			if mac == "" || n.MAC < mac {
				mac = n.MAC
			}
		}
	}
	inv.IDMAC = mac
	// Some cheap boards share a placeholder UUID, so the MAC is always mixed in.
	inv.MachineID = makeMachineID(hw.UUID, mac)
	if inv.MachineID == "" {
		inv.MachineID = makeMachineID(inv.Hostname)
	}
	return inv
}

// NICs lists this PC's network adapters.
func NICs() []protocol.NIC { return windowsNICs() }

// windowsNICs lists physical adapters with their real type (Ethernet vs Wi-Fi).
func windowsNICs() []protocol.NIC {
	var size uint32 = 15000
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_INCLUDE_GATEWAYS,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return genericNICs()
		}
	}
	var out []protocol.NIC
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.IfType != windows.IF_TYPE_ETHERNET_CSMACD && a.IfType != windows.IF_TYPE_IEEE80211 {
			continue
		}
		if a.PhysicalAddressLength != 6 {
			continue
		}
		desc := strings.ToLower(windows.UTF16PtrToString(a.Description))
		if isVirtualAdapter(desc) {
			continue
		}
		mac := a.PhysicalAddress[:6]
		if mac[0]&0x02 != 0 {
			continue // locally administered address: a virtual adapter (VPN, VM), not a real card
		}
		n := protocol.NIC{
			Name:  windows.UTF16PtrToString(a.FriendlyName),
			MAC:   fmt.Sprintf("%02X-%02X-%02X-%02X-%02X-%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]),
			Wired: a.IfType == windows.IF_TYPE_ETHERNET_CSMACD,
		}
		if a.OperStatus == windows.IfOperStatusUp && a.FirstGatewayAddress != nil {
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if ip := u.Address.IP(); ip != nil && ip.To4() != nil {
					n.IP = ip.String()
					break
				}
			}
		}
		out = append(out, n)
	}
	sortNICs(out)
	return out
}

func isVirtualAdapter(desc string) bool {
	for _, v := range []string{"virtual", "hyper-v", "vmware", "virtualbox", "vpn", "tap-", "tap ", "tap-windows", "wintun",
		"wireguard", "openvpn", "outline", "hiddify", "v2ray", "xray", "nekobox", "warp", "cloudflare", "proton", "nord", "fortinet",
		"anyconnect", "npcap", "loopback", "bluetooth", "wan miniport", "zerotier", "hamachi", "radmin", "tailscale", "kernel debug"} {
		if strings.Contains(desc, v) {
			return true
		}
	}
	return false
}

// Shutdown powers the PC off (a full shutdown, not Fast Startup's hybrid one)
// or restarts it.
func Shutdown(restart bool) error {
	flag := "/s"
	if restart {
		flag = "/r"
	}
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "shutdown.exe")
	cmd := exec.Command(exe, flag, "/t", "0", "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.Run()
}

//go:embed wolcheck.ps1
var wolCheckScript string

// WoLCheck inspects (and with fix=true corrects) the Windows side of
// Wake-on-LAN. BIOS settings cannot be read here; a wake test verifies them.
func WoLCheck(dataDir string, fix bool) protocol.WoLReport {
	rep := protocol.WoLReport{Supported: true}
	script, err := writeScript(dataDir, "wolcheck.ps1", wolCheckScript)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	mode := "check"
	if fix {
		mode = "fix"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	out, err := powershell(ctx, script, mode)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	out = bytes.TrimSpace(out)
	if i := bytes.IndexByte(out, '{'); i > 0 {
		out = out[i:] // skip any stray output before the JSON
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		rep.Supported = true
		rep.Error = "could not read the check result: " + err.Error()
	}
	return rep
}

// SelfUninstall removes the agent service from a separate process (a service
// cannot wait for its own removal). The lock screen closes with it.
func SelfUninstall() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "uninstall")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
		CreationFlags: 0x00000200 | 0x00000008} // CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS
	cmd.Start()
}
