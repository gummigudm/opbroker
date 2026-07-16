package agent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// Client is a simple synchronous request/response client over the agent socket.
type Client struct {
	SocketPath string
}

// NewClient returns a Client bound to the given socket path.
func NewClient(socket string) *Client { return &Client{SocketPath: socket} }

// Do sends req and returns the response. It does NOT auto-start the agent —
// use DoOrStart for that behavior.
func (c *Client) Do(req Request) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.SocketPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := WriteMessage(conn, req); err != nil {
		return nil, fmt.Errorf("send to agent: %w", err)
	}
	var resp Response
	if err := ReadMessage(conn, &resp); err != nil {
		return nil, fmt.Errorf("read from agent: %w", err)
	}
	return &resp, nil
}

// AutoStart determines how DoOrStart should behave when the socket is missing
// or the agent is unreachable.
type AutoStart struct {
	// Enabled turns on the auto-start behavior.
	Enabled bool
	// Exe is the path to the opbroker binary to launch; defaults to os.Executable().
	Exe string
	// Args to pass to the agent bootstrap command. Defaults to ["session","start","--background"].
	Args []string
	// WaitTimeout is how long to wait for the socket to appear after launch.
	WaitTimeout time.Duration
}

// DoOrStart sends req; if the agent is not running, it launches the agent and
// retries once.
func (c *Client) DoOrStart(req Request, as AutoStart) (*Response, error) {
	resp, err := c.Do(req)
	if err == nil {
		return resp, nil
	}
	if !as.Enabled {
		return nil, err
	}
	if !isConnErr(err) {
		return nil, err
	}
	if err := c.startAgent(as); err != nil {
		return nil, fmt.Errorf("could not start opbroker agent: %w", err)
	}
	return c.Do(req)
}

func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	// unix.ECONNREFUSED and os.ErrNotExist both surface here depending on
	// whether the socket file is missing or the listener is dead.
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return true
	}
	// net.OpError wraps "connect: no such file or directory" / "connection refused".
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func (c *Client) startAgent(as AutoStart) error {
	exe := as.Exe
	if exe == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve opbroker binary path: %w", err)
		}
		exe = p
	}
	args := as.Args
	if len(args) == 0 {
		args = []string{"session", "start", "--background"}
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach — the child will double-fork inside session start --background.
	go func() { _ = cmd.Wait() }()

	timeout := as.WaitTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(c.SocketPath); err == nil {
			// Also verify we can actually connect (socket exists but agent
			// might not have called Listen yet).
			if conn, err := net.DialTimeout("unix", c.SocketPath, 200*time.Millisecond); err == nil {
				_ = conn.Close()
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("agent did not become ready within %s", timeout)
}
