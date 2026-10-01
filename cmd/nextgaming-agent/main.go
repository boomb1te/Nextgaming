// Command nextgaming-agent runs on every gaming PC. It connects to the
// NextGaming server, enforces session time, keeps the lock screen running,
// and carries out power and Wake-on-LAN commands.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nextgaming/internal/agent"
	"nextgaming/internal/platform"
	"nextgaming/internal/protocol"
	"nextgaming/internal/svcrun"
)

const serviceName = "NextGamingAgent"

func usage() {
	fmt.Fprintf(os.Stderr, `NextGaming Agent %s

Usage:
  nextgaming-agent install -server ADDRESS -key CLUBKEY   Install as a Windows service (Administrator).
  nextgaming-agent uninstall                               Remove the service.
  nextgaming-agent run [-server ADDRESS -key CLUBKEY]      Run in this window (testing / simulation).
  nextgaming-agent wol-check [-fix]                        Check Wake-on-LAN settings on this PC.
  nextgaming-agent config                                  Show the saved configuration.

Options:
  -server ADDRESS   Server address, e.g. 192.168.1.10 or 192.168.1.10:%d
  -key CLUBKEY      Club key shown in the admin panel (Settings > Club)
  -client PATH      Lock screen program (default: nextgaming-client.exe next to the agent)
  -user-client      Run the lock screen as the logged-in user instead of SYSTEM
  -data DIR         Data folder (default %s)
`, agent.Version, protocol.DefaultAgentPort, platform.DefaultDataDir())
}

func main() {
	cmd := "run"
	args := os.Args[1:]
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = usage
	dataDir := fs.String("data", "", "data folder")
	srv := fs.String("server", "", "server address")
	key := fs.String("key", "", "club key")
	client := fs.String("client", "", "lock screen executable")
	userClient := fs.Bool("user-client", false, "run the lock screen as the user")
	fix := fs.Bool("fix", false, "fix Wake-on-LAN settings")
	asService := fs.Bool("service", false, "started by the Windows Service Control Manager (set automatically)")
	fs.Parse(args)
	if *asService {
		svcrun.MarkService()
	}
	if *dataDir == "" {
		*dataDir = platform.DefaultDataDir()
	}
	if abs, err := filepath.Abs(*dataDir); err == nil {
		*dataDir = abs
	}

	cfg, err := agent.LoadConfig(*dataDir)
	if err != nil {
		fatal("%v", err)
	}
	if *srv != "" {
		cfg.Server = normalizeServer(*srv)
	}
	if *key != "" {
		cfg.ClubKey = strings.ToUpper(strings.TrimSpace(*key))
	}
	if *client != "" {
		abs, _ := filepath.Abs(*client)
		cfg.ClientPath = abs
	}
	if *userClient {
		cfg.ClientAsSystem = false
	}

	switch cmd {
	case "install":
		if !svcrun.IsAdmin() {
			fatal("please run this from an Administrator command prompt")
		}
		if cfg.Server == "" || cfg.ClubKey == "" {
			fatal("both -server and -key are required")
		}
		cfg.InstalledAt = time.Now()
		if err := cfg.Save(); err != nil {
			fatal("%v", err)
		}
		err := svcrun.Install(svcrun.Spec{
			Name: serviceName, DisplayName: "NextGaming Agent",
			Description: "Connects this gaming PC to the NextGaming server and controls the lock screen.",
			Args:        []string{"run", "-data", *dataDir},
		})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("Installed and started %s. This PC will appear in the admin panel within a few seconds.\n", serviceName)
	case "uninstall":
		if err := svcrun.Uninstall(serviceName); err != nil {
			fatal("%v", err)
		}
		fmt.Println("Service removed.")
	case "config":
		b, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(b))
	case "wol-check":
		rep := platform.WoLCheck(*dataDir, *fix)
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
	case "run":
		if *srv != "" || *key != "" || *client != "" || *userClient {
			if err := cfg.Save(); err != nil {
				fatal("%v", err)
			}
		}
		os.MkdirAll(*dataDir, 0o755)
		if f, err := os.OpenFile(filepath.Join(*dataDir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			log.SetOutput(svcrun.LogWriter(f))
		}
		if platform.Simulated {
			log.Printf("SIMULATION MODE: not on Windows, so no real lock screen, power or Wake-on-LAN control")
		}
		err := svcrun.Run(serviceName, func(ctx context.Context) error {
			return agent.New(cfg).Run(ctx)
		})
		if err != nil {
			log.Printf("fatal: %v", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://")
	s = strings.TrimSuffix(s, "/")
	if !strings.Contains(s, ":") {
		s = fmt.Sprintf("%s:%d", s, protocol.DefaultAgentPort)
	}
	return s
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}
