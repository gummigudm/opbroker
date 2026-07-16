package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/config"
	"github.com/gummigudm/opbroker/internal/opcli"
	"github.com/gummigudm/opbroker/internal/setup"
	"github.com/gummigudm/opbroker/internal/version"
)

func cmdSessionStart(args []string) error {
	fs := flag.NewFlagSet("session start", flag.ContinueOnError)
	background := fs.Bool("background", false, "run agent as a detached background process")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *background {
		return spawnBackground()
	}
	return runAgentForeground()
}

// spawnBackground forks a detached child running `session start` (without
// --background), so the parent can return immediately.
func spawnBackground() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve opbroker binary path: %w", err)
	}
	cmd := exec.Command(exe, "session", "start")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach: new session so the child survives after parent exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Do NOT Wait — let the child run.
	return nil
}

func runAgentForeground() error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	if err := setup.EnsureInitialized(dir); err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	sock, err := cfg.SocketPath()
	if err != nil {
		return err
	}
	pidFile, err := cfg.PIDFilePath()
	if err != nil {
		return err
	}

	// Resolve allowed_callers with symlink expansion so the agent doesn't
	// reject a caller whose configured path is a symlink.
	callers := cfg.Global.Agent.AllowedCallers
	if len(callers) == 0 {
		// Default: allow the current executable.
		if exe, err := os.Executable(); err == nil {
			callers = []string{exe}
		}
	}

	fetcher := opcli.NewAdapter(opcli.New(""))
	profiles := profileLookup{m: cfg}

	srv := agent.NewServer(agent.Config{
		SocketPath:     sock,
		PIDFile:        pidFile,
		AllowedCallers: callers,
		TTL:            cfg.Global.Agent.TTL,
		Fetcher:        fetcher,
		Profiles:       profiles,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return srv.Start(ctx)
}

func cmdSessionStop(_ []string) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(agent.Request{Type: agent.TypeStop})
	if err != nil {
		if isSocketMissing(err) {
			fmt.Println("agent: not running")
			return nil
		}
		return err
	}
	if resp.Type != agent.TypeOK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Println("agent: stopped")
	return nil
}

func cmdSessionRefresh(_ []string) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(agent.Request{Type: agent.TypeRefresh})
	if err != nil {
		if isSocketMissing(err) {
			fmt.Println("agent: not running (nothing to clear)")
			return nil
		}
		return err
	}
	if resp.Type != agent.TypeOK {
		return fmt.Errorf("%s", resp.Error)
	}
	n := resp.Meta["cleared"]
	if n == "0" {
		fmt.Println("cache already empty")
	} else {
		fmt.Printf("cleared %s cached entries\n", n)
	}
	return nil
}

func cmdSessionStatus(_ []string) error {
	client, err := newClient()
	if err != nil {
		return err
	}
	resp, err := client.Do(agent.Request{Type: agent.TypeStatus})
	if err != nil {
		if isSocketMissing(err) {
			fmt.Println("agent: not running")
			return nil
		}
		return err
	}
	if resp.Type != agent.TypeOK || resp.Status == nil {
		return fmt.Errorf("%s", resp.Error)
	}
	s := resp.Status
	fmt.Printf("agent: running (pid=%d)\n", s.PID)
	fmt.Printf("  client:  opbroker %s\n", version.Version)
	fmt.Printf("  socket:  %s\n", s.SocketPath)
	fmt.Printf("  uptime:  %s\n", s.Uptime.Round(1_000_000_000))
	fmt.Printf("  ttl:     %s\n", s.TTL)
	fmt.Printf("  cached:  %d account lists, %d resolved items, %d selections\n",
		s.AccountListCount, s.ResolvedCount, s.SelectionCount)
	return nil
}

func newClient() (*agent.Client, error) {
	dir, err := config.DefaultDir()
	if err != nil {
		return nil, err
	}
	// Diagnostic commands (stop/refresh/status) must not launch interactive
	// setup — return a friendly error if config is missing.
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); os.IsNotExist(err) {
		return nil, fmt.Errorf("opbroker is not configured yet — run `opbroker` to set it up")
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	sock, err := cfg.SocketPath()
	if err != nil {
		return nil, err
	}
	return agent.NewClient(sock), nil
}

// isSocketMissing reports whether err suggests the agent isn't running.
func isSocketMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such file") || strings.Contains(msg, "connection refused")
}
