// Package setup provides the first-run interactive configuration for
// ~/.opbroker.
//
// Detection is a single stat on config.yaml — if present, we assume the user
// has been through setup and no-op. If missing, we prompt for a 1Password
// account (via `op account list`) and write config.yaml + an empty
// profiles.yaml.
package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/opcli"
	"github.com/gummigudm/opbroker/internal/selector"
)

// AccountLister fetches the list of 1Password accounts. Provided as an
// interface so tests can inject a fake without shelling out to `op`.
type AccountLister interface {
	ListAccounts() ([]opcli.OpAccount, error)
}

// AccountChooser picks one account from a non-empty list, typically via an
// interactive picker.
type AccountChooser func(accounts []opcli.OpAccount) (opcli.OpAccount, error)

// Deps bundles the injectable dependencies of runInteractive.
type Deps struct {
	Lister  AccountLister
	Chooser AccountChooser
	ExePath func() (string, error)
	Stdout  *os.File // where the banner is printed; defaults to os.Stdout
}

// EnsureInitialized checks whether ~/.opbroker/config.yaml exists. If not, it
// runs the interactive first-run setup, which writes config.yaml and an empty
// profiles.yaml to dir. Callers pass their configured base directory (usually
// ~/.opbroker).
func EnsureInitialized(dir string) error {
	if isInitialized(dir) {
		return nil
	}
	return runInteractive(dir, defaultDeps())
}

// isInitialized reports whether config.yaml exists in dir.
func isInitialized(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "config.yaml"))
	return err == nil
}

func defaultDeps() Deps {
	return Deps{
		Lister:  opcli.New(""),
		Chooser: chooseInteractively,
		ExePath: os.Executable,
		Stdout:  os.Stdout,
	}
}

func runInteractive(dir string, deps Deps) error {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	fmt.Fprintln(deps.Stdout, "First-time setup for opbroker")

	// Ensure dir with 0700.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}

	// Query op accounts.
	accounts, err := deps.Lister.ListAccounts()
	if err != nil {
		return translateOpError(err)
	}
	if len(accounts) == 0 {
		return errors.New("no 1Password accounts found — sign in with `op account add` and try again")
	}

	var chosen opcli.OpAccount
	if len(accounts) == 1 {
		chosen = accounts[0]
		fmt.Fprintf(deps.Stdout, "Using 1Password account: %s (%s)\n", chosen.Email, chosen.URL)
	} else {
		chosen, err = deps.Chooser(accounts)
		if err != nil {
			if errors.Is(err, selector.ErrNoTTY) {
				return errors.New("first-time setup needs an interactive terminal — run `opbroker` in a regular shell")
			}
			return err
		}
	}

	// Resolve exe path for allowed_callers.
	exe, err := deps.ExePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if canonical, err := filepath.EvalSymlinks(exe); err == nil {
		exe = canonical
	}

	// Write config.yaml.
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(renderConfig(chosen.UserUUID, exe)), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}

	// Write profiles.yaml.
	profilesPath := filepath.Join(dir, "profiles.yaml")
	if err := os.WriteFile(profilesPath, []byte(emptyProfilesTemplate), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", profilesPath, err)
	}

	fmt.Fprintln(deps.Stdout, "Setup complete. Add profiles to ~/.opbroker/profiles.yaml to get started.")
	return nil
}

// translateOpError converts common `op` failures into user-actionable messages.
func translateOpError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "executable file not found"):
		return errors.New("1Password CLI (`op`) is not installed — install it with `brew install 1password-cli` and try again")
	default:
		return fmt.Errorf("could not list 1Password accounts: %w", err)
	}
}

// chooseInteractively wraps the selector.Pick call so the picker's option
// model is only imported here.
func chooseInteractively(accounts []opcli.OpAccount) (opcli.OpAccount, error) {
	opts := make([]agent.AccountOption, len(accounts))
	for i, a := range accounts {
		opts[i] = agent.AccountOption{
			Account: a.Email,
			Title:   a.URL,
			ItemID:  a.UserUUID,
		}
	}
	choice, err := selector.Pick(opts, "Pick a 1Password account")
	if err != nil {
		return opcli.OpAccount{}, err
	}
	// Map back to OpAccount via UserUUID.
	for _, a := range accounts {
		if a.UserUUID == choice.ItemID {
			return a, nil
		}
	}
	return opcli.OpAccount{}, fmt.Errorf("picker returned unknown account %q", choice.ItemID)
}

// Templates.

func renderConfig(opAccount, exePath string) string {
	return fmt.Sprintf(`op_account: %s

agent:
  ttl: 30m
  socket: ~/.opbroker/run/agent.sock
  allowed_callers:
    - %s
`, opAccount, exePath)
}

const emptyProfilesTemplate = `# Add profiles here. Example:
#
# profiles:
#   myprofile:
#     tag: MyService/creds
#     account_field: account
#     env:
#       API_TOKEN: token

profiles: {}
`
