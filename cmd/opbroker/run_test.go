package main

import (
	"strings"
	"testing"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/config"
)

func TestNoTTYError_ProfileWithIdentityFlag(t *testing.T) {
	opts := []agent.AccountOption{
		{Account: "account1", Title: "T1", ItemID: "id1"},
		{Account: "account2", Title: "T2", ItemID: "id2"},
	}
	prof := &agent.ProfileConfig{
		Args: map[string]string{"--account": config.ArgTemplateAccount, "--region": "aws_region"},
	}

	got := noTTYError(opts, prof).Error()

	// Must list accounts.
	if !strings.Contains(got, "account1, account2") {
		t.Errorf("missing account list; got:\n%s", got)
	}
	// Must mention the target-flag form.
	if !strings.Contains(got, "target's --account flag") {
		t.Errorf("missing target-flag hint; got:\n%s", got)
	}
	// Must show a concrete copy-paste example with the first account.
	if !strings.Contains(got, "--account account1") {
		t.Errorf("missing copy-paste example; got:\n%s", got)
	}
	// Must also offer the opbroker fallback form.
	if !strings.Contains(got, "opbroker run --account account1") {
		t.Errorf("missing opbroker fallback hint; got:\n%s", got)
	}
}

func TestNoTTYError_ProfileWithoutIdentityFlag(t *testing.T) {
	opts := []agent.AccountOption{
		{Account: "prod"},
		{Account: "staging"},
	}
	prof := &agent.ProfileConfig{
		// No args entry mapped to ${account} — no identity flag.
		Env: map[string]string{"FOO_TOKEN": "foo_token"},
	}

	got := noTTYError(opts, prof).Error()

	if !strings.Contains(got, "prod, staging") {
		t.Errorf("missing account list; got:\n%s", got)
	}
	// Should NOT mention target flag when profile has none.
	if strings.Contains(got, "target's") {
		t.Errorf("should not suggest target flag when profile has none; got:\n%s", got)
	}
	// Should suggest the opbroker form.
	if !strings.Contains(got, "opbroker run --account prod") {
		t.Errorf("missing opbroker hint; got:\n%s", got)
	}
}

func TestNoTTYError_ZeroOptions(t *testing.T) {
	got := noTTYError(nil, nil).Error()
	if !strings.Contains(got, "no accounts available") {
		t.Errorf("missing zero-options hint; got:\n%s", got)
	}
}

func TestNoTTYError_NilProfile(t *testing.T) {
	// If prof is nil but there ARE accounts, we should still get the opbroker-form hint.
	opts := []agent.AccountOption{{Account: "only"}}
	got := noTTYError(opts, nil).Error()

	if !strings.Contains(got, "opbroker run --account only") {
		t.Errorf("nil-profile path should fall back to opbroker form; got:\n%s", got)
	}
	if strings.Contains(got, "target's") {
		t.Errorf("nil-profile path should not mention target flag; got:\n%s", got)
	}
}
