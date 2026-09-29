// Command nextgaming-server runs the NextGaming server on the front-desk PC:
// the database, the connection to every gaming PC, and the admin panel.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"nextgaming/internal/protocol"
	"nextgaming/internal/server"
	"nextgaming/internal/store"
	"nextgaming/internal/svcrun"
)

const serviceName = "NextGamingServer"

func usage() {
	fmt.Fprintf(os.Stderr, `NextGaming Server %s

Usage:
  nextgaming-server [command] [-data DIR]

Commands:
  run              Run in this window (default). Opens the admin panel.
  install          Install as a Windows service that starts with Windows (run as Administrator).
  uninstall        Remove the Windows service.
  backup           Write a database backup now.
  reset-password USER NEWPASSWORD
                   Reset an admin password (stop the server first).

Options:
  -data DIR        Data folder (default %s)
`, server.Version, server.DefaultDataDir())
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
	noBrowser := fs.Bool("no-browser", false, "do not open the admin panel in a browser")
	fs.Parse(args)
	if *dataDir == "" {
		*dataDir = server.DefaultDataDir()
	}
	abs, err := filepath.Abs(*dataDir)
	if err == nil {
		*dataDir = abs
	}

	switch cmd {
	case "run":
		os.Exit(run(*dataDir, !*noBrowser))
	case "install":
		if !svcrun.IsAdmin() {
			fatal("please run this from an Administrator command prompt")
		}
		cfg, err := server.LoadConfig(*dataDir)
		if err != nil {
			fatal("%v", err)
		}
		err = svcrun.Install(svcrun.Spec{
			Name: serviceName, DisplayName: "NextGaming Server",
			Description: "NextGaming gaming-club management server (admin panel and PC control).",
			Args:        []string{"run", "-data", *dataDir, "-no-browser"},
		})
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("Installed and started the %s service.\n", serviceName)
		fmt.Printf("Admin panel:  http://localhost:%d\n", protocol.DefaultHTTPPort)
		fmt.Printf("Club key:     %s   (needed when installing the agent on each gaming PC)\n", cfg.ClubKey)
		fmt.Println("Allow the ports in Windows Firewall:  scripts\\firewall-server.ps1")
	case "uninstall":
		if err := svcrun.Uninstall(serviceName); err != nil {
			fatal("%v", err)
		}
		fmt.Println("Service removed. Your data is kept in", *dataDir)
	case "backup":
		cfg, err := server.LoadConfig(*dataDir)
		if err != nil {
			fatal("%v", err)
		}
		s, err := server.New(cfg)
		if err != nil {
			fatal("%v", err)
		}
		defer s.Close()
		path, err := s.BackupNow()
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println("Backup written:", path)
	case "reset-password":
		if fs.NArg() != 2 {
			usage()
			os.Exit(2)
		}
		if err := resetPassword(*dataDir, fs.Arg(0), fs.Arg(1)); err != nil {
			fatal("%v", err)
		}
		fmt.Println("Password changed.")
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func run(dataDir string, openBrowser bool) int {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Log to the console and to a file (a service has no console).
	if f, err := os.OpenFile(filepath.Join(dataDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		if svcrun.IsService() {
			log.SetOutput(f)
		} else {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}
	err := svcrun.Run(serviceName, func(ctx context.Context) error {
		cfg, err := server.LoadConfig(dataDir)
		if err != nil {
			return err
		}
		s, err := server.New(cfg)
		if err != nil {
			return err
		}
		defer s.Close()
		log.Printf("NextGaming server %s, data in %s", server.Version, dataDir)
		log.Printf("club key: %s", cfg.ClubKey)
		if openBrowser && !svcrun.IsService() {
			go func() {
				time.Sleep(700 * time.Millisecond)
				openURL(fmt.Sprintf("http://localhost%s", portSuffix(cfg.HTTPAddr)))
			}()
		}
		return s.Run(ctx)
	})
	if err != nil {
		log.Printf("fatal: %v", err)
		return 1
	}
	return 0
}

func portSuffix(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return ""
}

func openURL(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	c.Start()
}

func resetPassword(dataDir, user, pass string) error {
	if err := server.ValidatePassword(pass); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "nextgaming.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	a, _, err := store.AdminByUsername(st.DB, user)
	if err != nil {
		return fmt.Errorf("no admin called %q", user)
	}
	hash, err := server.HashPassword(pass)
	if err != nil {
		return err
	}
	if err := store.SetAdminPassword(st.DB, a.ID, hash); err != nil {
		return err
	}
	return store.Audit(st.DB, 0, "password_reset_cli", 0, user)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}
