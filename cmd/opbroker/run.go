package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/argparse"
	"github.com/gummigudm/opbroker/internal/config"
	"github.com/gummigudm/opbroker/internal/selector"
	"github.com/gummigudm/opbroker/internal/setup"
)

// joinAccounts formats account names from a select response as "a, b, c" for
// display in error messages.
func joinAccounts(opts []agent.AccountOption) string {
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Account
	}
	return strings.Join(names, ", ")
}

// noTTYError builds a user-facing error for when the interactive picker
// can't attach a controlling terminal. Includes the available accounts and
// a copy-paste-friendly suggestion, choosing between the target-argv form
// (if the profile has an identity flag) and the opbroker `--account` form.
func noTTYError(opts []agent.AccountOption, prof *agent.ProfileConfig) error {
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Account
	}

	var idFlag string
	if prof != nil {
		idFlag = identityFlagOf(prof)
	}

	var hint string
	switch {
	case len(names) == 0:
		hint = "  (no accounts available for this tag)"
	case idFlag != "":
		hint = fmt.Sprintf(
			"  pass one via the target's %s flag, e.g.\n    <cmd> %s %s\n  or via opbroker directly:\n    opbroker run --account %s -- <cmd> …",
			idFlag, idFlag, names[0], names[0],
		)
	default:
		hint = fmt.Sprintf(
			"  pass --account to opbroker, e.g.\n    opbroker run --account %s -- <cmd> …",
			names[0],
		)
	}

	return fmt.Errorf(
		"no controlling terminal available for account picker\n"+
			"  available accounts: %s\n%s",
		strings.Join(names, ", "), hint,
	)
}

// fieldFlag captures repeatable --field NAME=field flags.
type fieldFlag map[string]string

func (f fieldFlag) String() string {
	parts := make([]string, 0, len(f))
	for k, v := range f {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (f fieldFlag) Set(s string) error {
	i := strings.Index(s, "=")
	if i <= 0 {
		return fmt.Errorf("expected NAME=field, got %q", s)
	}
	f[s[:i]] = s[i+1:]
	return nil
}

// runContext bundles everything cmdRun needs after parsing flags and loading
// config: the request to send, the resolved agent.ProfileConfig-equivalent
// data for argv extraction/injection, and the target command info.
type runContext struct {
	req     agent.Request
	profile *agent.ProfileConfig // effective profile (from --profile + overrides, or fully inline)
	target  string
	args    []string
	debug   bool // --opbroker-debug was set on the target argv
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	profileName := fs.String("profile", "", "named profile from profiles.yaml")
	fs.StringVar(profileName, "p", "", "named profile (short)")
	tag := fs.String("tag", "", "1Password tag filter (overrides profile)")
	accountField := fs.String("account-field", "", "field identifying the account (overrides profile)")
	account := fs.String("account", "", "pre-select account (skip picker)")
	opAccount := fs.String("op-account", "", "1Password account ID (overrides profile/global)")
	fields := fieldFlag{}
	fs.Var(fields, "field", "field mapping ENV=field_name (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("missing target command\nusage: opbroker run [--profile NAME] -- CMD [ARGS...]")
	}

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

	rctx, err := buildRunContext(cfg, *profileName, *tag, *accountField, *account, *opAccount, fields, rest)
	if err != nil {
		return err
	}

	// Strip --opbroker-debug from the target argv before extraction/injection
	// so it doesn't confuse the argv walker and doesn't leak to the target.
	rctx.args, rctx.debug = argparse.TakeBoolFlag(rctx.args, DebugFlag)

	// If the profile has an identity flag (args entry mapped to ${account})
	// and the user didn't set --account on opbroker, try to pick the account
	// off the target argv.
	if rctx.req.Account == "" && rctx.profile != nil {
		if idFlag := identityFlagOf(rctx.profile); idFlag != "" {
			if v, ok := argparse.ExtractFlag(rctx.args, idFlag); ok {
				rctx.req.Account = v
			}
		}
	}

	client := agent.NewClient(sock)
	resp, err := client.DoOrStart(rctx.req, agent.AutoStart{Enabled: true})
	if err != nil {
		return err
	}

	// Handle multi-account: run the picker and re-request.
	if resp.Type == agent.TypeSelectRequired {
		filtered := selector.Filter(resp.Options, rctx.req.Account)
		if len(filtered) == 0 {
			return fmt.Errorf("account %q not found (available: %s)", rctx.req.Account, joinAccounts(resp.Options))
		}
		choice, err := selector.Pick(filtered, "Select account")
		if err != nil {
			if errors.Is(err, selector.ErrNoTTY) {
				return noTTYError(resp.Options, rctx.profile)
			}
			return err
		}
		rctx.req.Type = agent.TypeSelect
		rctx.req.Account = choice.Account
		resp, err = client.Do(rctx.req)
		if err != nil {
			return err
		}
	}
	if resp.Type == agent.TypeError {
		return fmt.Errorf("%s", resp.Error)
	}
	if resp.Type != agent.TypeOK {
		return fmt.Errorf("agent returned unexpected response type %q", resp.Type)
	}

	// Track which flags opbroker will actually inject (i.e., were not already
	// in the user's argv). Needed for debug rendering to know which values to
	// mask.
	injectedFlags := map[string]bool{}
	for flag := range resp.Args {
		if !argparse.HasFlag(rctx.args, flag) {
			injectedFlags[flag] = true
		}
	}

	// Inject resolved args into the target argv. Any flag the user already
	// supplied is left alone — user's explicit value wins.
	finalArgs := injectArgs(rctx.args, resp.Args, rctx.profile)

	exePath, err := lookPath(rctx.target)
	if err != nil {
		return err
	}

	if rctx.debug {
		printDryRun(os.Stdout, resp.Env, finalArgs, exePath, resp.Secrets, injectedFlags)
		return nil
	}

	// Build env and exec.
	env := os.Environ()
	for k, v := range resp.Env {
		env = append(env, k+"="+v)
	}
	argv := append([]string{exePath}, finalArgs...)
	return syscall.Exec(exePath, argv, env)
}

// buildRunContext assembles the outgoing request and the effective profile
// (used for extraction/injection) from parsed flags + loaded config.
func buildRunContext(cfg *config.Merged, profileName, tag, accountField, account, opAccount string, fields fieldFlag, rest []string) (*runContext, error) {
	rctx := &runContext{
		req:    agent.Request{Type: agent.TypeGet, Profile: profileName, Account: account},
		target: rest[0],
		args:   rest[1:],
	}

	if profileName == "" {
		if tag == "" || accountField == "" || len(fields) == 0 {
			return nil, fmt.Errorf("no profile selected; pass --profile NAME, or supply --tag, --account-field, and at least one --field")
		}
		p := &agent.ProfileConfig{
			Tag:          tag,
			AccountField: accountField,
			Env:          map[string]string(fields),
			OpAccount:    opAccount,
			ArgStyle:     config.ArgStyleSeparate,
			ArgPlacement: config.ArgPlacementFirst,
		}
		rctx.req.Config = p
		rctx.profile = p
		return rctx, nil
	}

	// --profile is set; load and validate.
	prof, err := cfg.Profile(profileName)
	if err != nil {
		return nil, err
	}
	if err := prof.Validate(); err != nil {
		return nil, err
	}

	// Build the effective wire profile (starting from stored profile, applying
	// any flag overlays).
	effective := toAgentProfile(prof)
	if tag != "" {
		effective.Tag = tag
	}
	if accountField != "" {
		effective.AccountField = accountField
	}
	if opAccount != "" {
		effective.OpAccount = opAccount
	}
	if len(fields) > 0 {
		effective.Env = mergeEnv(prof.Env, fields)
	}
	rctx.profile = effective

	// Only send an inline config if the user actually overrode something —
	// otherwise the agent will pull the profile from its own registry.
	if tag != "" || accountField != "" || opAccount != "" || len(fields) > 0 {
		rctx.req.Config = effective
		rctx.req.Profile = ""
	}
	return rctx, nil
}

// identityFlagOf returns the args entry (flag name) whose value is
// ${account}, or "" if the profile doesn't declare an identity flag.
func identityFlagOf(p *agent.ProfileConfig) string {
	for flag, source := range p.Args {
		if source == config.ArgTemplateAccount {
			return flag
		}
	}
	return ""
}

// injectArgs merges resolved args from the agent into the target argv.
// Flags already present in argv (in either style) are skipped — user's
// explicit value wins. Style + placement come from the effective profile.
func injectArgs(argv []string, resolved map[string]string, prof *agent.ProfileConfig) []string {
	if len(resolved) == 0 {
		return argv
	}
	// Build pairs in a stable order (by flag name) so multi-flag injections
	// are reproducible.
	flags := make([]string, 0, len(resolved))
	for f := range resolved {
		flags = append(flags, f)
	}
	sortStrings(flags)
	pairs := make([]argparse.Pair, 0, len(flags))
	for _, f := range flags {
		pairs = append(pairs, argparse.Pair{Flag: f, Value: resolved[f]})
	}

	style := argparse.StyleSeparate
	placement := argparse.PlacementFirst
	if prof != nil {
		if prof.ArgStyle == config.ArgStyleEquals {
			style = argparse.StyleEquals
		}
		if prof.ArgPlacement == config.ArgPlacementLast {
			placement = argparse.PlacementLast
		}
	}
	return argparse.Inject(argv, pairs, style, placement)
}

// sortStrings is a tiny insertion sort — avoids pulling in sort just for one
// slice a few entries long.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func mergeEnv(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func lookPath(name string) (string, error) {
	if strings.Contains(name, "/") {
		if _, err := os.Stat(name); err != nil {
			return "", err
		}
		return name, nil
	}
	p, err := execLookPath(name)
	if err != nil {
		return "", fmt.Errorf("command %q not found on PATH", name)
	}
	return p, nil
}
