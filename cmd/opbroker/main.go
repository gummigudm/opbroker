// Command opbroker is a 1Password credential broker with a caching agent.
package main

import (
	"fmt"
	"os"

	"github.com/gummigudm/opbroker/internal/config"
	"github.com/gummigudm/opbroker/internal/setup"
	"github.com/gummigudm/opbroker/internal/version"
)

const usage = `opbroker — 1Password credential broker

Usage:
  opbroker run [--profile <name>] [flags] -- <command> [args...]
  opbroker session start [--background]
  opbroker session stop
  opbroker session refresh
  opbroker session status
  opbroker --version

Run "opbroker <command> --help" for command-specific flags.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if usageErr, ok := err.(*usageError); ok {
			if usageErr.msg != "" {
				fmt.Fprintf(os.Stderr, "opbroker: %s\n\n", usageErr.msg)
			}
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "opbroker: %v\n", err)
		os.Exit(1)
	}
}

// usageError signals a usage problem; main prints the message (if any)
// followed by full usage and exits 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) *usageError {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

func run(args []string) error {
	// --version short-circuits setup and dispatch — no config needed to
	// report the build-time version string.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v" || args[0] == "version") {
		fmt.Println(version.Version)
		return nil
	}

	// First-run setup runs before dispatch so that bare `opbroker` (no
	// subcommand) also triggers it. We skip it for:
	//   - `session start --background` — invoked by auto-start; the background
	//     daemon has no tty to prompt on.
	//   - `session stop|refresh|status` — diagnostic commands that must remain
	//     scriptable. If config is missing they return a friendly error via
	//     newClient() instead of prompting.
	if !shouldSkipEnsure(args) {
		if err := ensureInitialized(); err != nil {
			return err
		}
	}

	if len(args) == 0 {
		// Bare invocation: after setup ran (or was a no-op), print usage so
		// the user sees what commands exist.
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "session":
		if len(args) < 2 {
			return usageErrorf("session needs a subcommand (start, stop, refresh, or status)")
		}
		switch args[1] {
		case "start":
			return cmdSessionStart(args[2:])
		case "stop":
			return cmdSessionStop(args[2:])
		case "refresh":
			return cmdSessionRefresh(args[2:])
		case "status":
			return cmdSessionStatus(args[2:])
		default:
			return usageErrorf("unknown session subcommand %q", args[1])
		}
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return usageErrorf("unknown command %q", args[0])
	}
}

// shouldSkipEnsure reports whether the given argv should NOT trigger
// interactive first-run setup.
func shouldSkipEnsure(args []string) bool {
	if len(args) < 2 || args[0] != "session" {
		return false
	}
	switch args[1] {
	case "stop", "refresh", "status":
		return true
	case "start":
		for _, a := range args[2:] {
			if a == "--background" {
				return true
			}
		}
	}
	return false
}

func ensureInitialized() error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	return setup.EnsureInitialized(dir)
}

// Subcommand implementations live in run.go and session.go.
