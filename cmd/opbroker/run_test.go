package main

import (
	"strings"
	"testing"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/config"
)

func TestNoTTYError_ProfileWithIdentityFlagAndAccountArg(t *testing.T) {
	opts := []agent.AccountOption{
		{Account: "account1", Title: "T1", ItemID: "id1"},
		{Account: "account2", Title: "T2", ItemID: "id2"},
	}
	prof := &agent.ProfileConfig{
		Args:       map[string]string{"--account": config.ArgTemplateAccount, "--region": "aws_region"},
		AccountArg: config.DefaultAccountArg,
	}

	got := noTTYError(opts, prof).Error()

	// Must list accounts.
	if !strings.Contains(got, "account1, account2") {
		t.Errorf("missing account list; got:\n%s", got)
	}
	// account_arg (primary hint) — stripped by opbroker.
	if !strings.Contains(got, "--opbroker-account account1") {
		t.Errorf("missing account_arg hint; got:\n%s", got)
	}
	if !strings.Contains(got, "opbroker consumes it") {
		t.Errorf("account_arg hint should explain the strip behavior; got:\n%s", got)
	}
	// Identity flag (secondary hint) — target's own flag.
	if !strings.Contains(got, "target's own --account flag") {
		t.Errorf("missing target-flag hint; got:\n%s", got)
	}
	if !strings.Contains(got, "--account account1") {
		t.Errorf("missing --account example; got:\n%s", got)
	}
	// Top-level opbroker fallback.
	if !strings.Contains(got, "opbroker run --account account1") {
		t.Errorf("missing opbroker fallback hint; got:\n%s", got)
	}
}

func TestNoTTYError_ProfileWithoutIdentityFlag(t *testing.T) {
	// bar-shaped profile: identity in env, no args identity flag. account_arg
	// is still available as the primary way to force a selection.
	opts := []agent.AccountOption{
		{Account: "prod"},
		{Account: "staging"},
	}
	prof := &agent.ProfileConfig{
		Env:        map[string]string{"BAR_TOKEN": "bar_token", "BAR_ACCOUNT": config.ArgTemplateAccount},
		AccountArg: config.DefaultAccountArg,
	}

	got := noTTYError(opts, prof).Error()

	if !strings.Contains(got, "prod, staging") {
		t.Errorf("missing account list; got:\n%s", got)
	}
	// account_arg hint present.
	if !strings.Contains(got, "--opbroker-account prod") {
		t.Errorf("missing account_arg hint; got:\n%s", got)
	}
	// Should NOT mention target's own flag — profile has none.
	if strings.Contains(got, "target's own") {
		t.Errorf("should not suggest target's own flag when profile has none; got:\n%s", got)
	}
	// Should still suggest opbroker fallback.
	if !strings.Contains(got, "opbroker run --account prod") {
		t.Errorf("missing opbroker fallback; got:\n%s", got)
	}
}

func TestNoTTYError_CustomAccountArg(t *testing.T) {
	opts := []agent.AccountOption{{Account: "only"}}
	prof := &agent.ProfileConfig{
		AccountArg: "--opbroker-foo-account",
	}
	got := noTTYError(opts, prof).Error()
	if !strings.Contains(got, "--opbroker-foo-account only") {
		t.Errorf("missing custom account_arg in hint; got:\n%s", got)
	}
}

func TestNoTTYError_ZeroOptions(t *testing.T) {
	got := noTTYError(nil, nil).Error()
	if !strings.Contains(got, "no accounts available") {
		t.Errorf("missing zero-options hint; got:\n%s", got)
	}
}

func TestNoTTYError_NilProfile(t *testing.T) {
	// If prof is nil but there ARE accounts, we get only the opbroker fallback.
	opts := []agent.AccountOption{{Account: "only"}}
	got := noTTYError(opts, nil).Error()

	if !strings.Contains(got, "opbroker run --account only") {
		t.Errorf("nil-profile path should include opbroker fallback; got:\n%s", got)
	}
	if strings.Contains(got, "--opbroker-account") {
		t.Errorf("nil-profile path should not suggest account_arg; got:\n%s", got)
	}
	if strings.Contains(got, "target's own") {
		t.Errorf("nil-profile path should not mention target flag; got:\n%s", got)
	}
}
