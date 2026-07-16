// Probe binary: attempts to connect to the opbroker agent socket as an
// unauthorized caller. Used for negative-security tests only.
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gummigudm/opbroker/internal/agent"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal("home: %v", err)
	}
	sock := filepath.Join(home, ".opbroker", "run", "agent.sock")
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		fatal("dial %s: %v", sock, err)
	}
	defer conn.Close()

	if err := agent.WriteMessage(conn, agent.Request{Type: agent.TypeStatus}); err != nil {
		fatal("write: %v", err)
	}
	var resp agent.Response
	if err := agent.ReadMessage(conn, &resp); err != nil {
		fatal("read: %v", err)
	}
	fmt.Printf("response: type=%q error=%q\n", resp.Type, resp.Error)
	if resp.Type == agent.TypeError {
		// Success — the agent rejected us.
		os.Exit(0)
	}
	// If we get here, the agent accepted an unauthorized caller.
	fmt.Fprintln(os.Stderr, "FAIL: agent accepted unauthorized caller")
	os.Exit(1)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
