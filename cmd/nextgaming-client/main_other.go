//go:build !windows

// Command nextgaming-client is the Windows lock screen. On other systems it
// prints the state the agent would show, which is handy when developing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"

	"nextgaming/internal/protocol"
)

func main() {
	port := flag.Int("port", protocol.DefaultClientPort, "agent local port")
	flag.Parse()
	fmt.Println("The NextGaming lock screen runs on Windows. Showing the agent state instead (Ctrl+C to stop).")
	c := &http.Client{Timeout: time.Second}
	for {
		var st protocol.ClientState
		resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/state", *port))
		if err != nil {
			fmt.Println("LOCKED (agent not reachable)")
		} else {
			json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			switch {
			case st.Locked:
				fmt.Printf("LOCKED  %s  paused=%v\n", st.PCName, st.Paused)
			case st.Mode == "prepaid":
				fmt.Printf("OPEN    %s  %ds left\n", st.PCName, st.RemainingSec)
			default:
				fmt.Printf("OPEN    %s  %ds used (%s)\n", st.PCName, st.ElapsedSec, st.Mode)
			}
		}
		time.Sleep(2 * time.Second)
	}
}
