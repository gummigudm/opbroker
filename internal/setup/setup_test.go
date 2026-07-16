package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gummigudm/opbroker/internal/opcli"
	"gopkg.in/yaml.v3"
)

// fakeLister is a static AccountLister for tests.
type fakeLister struct {
	accounts []opcli.OpAccount
	err      error
}

func (f *fakeLister) ListAccounts() ([]opcli.OpAccount, error) {
	return f.accounts, f.err
}

// pickIndex returns a chooser that always picks the given index.
func pickIndex(i int) AccountChooser {
	return func(accs []opcli.OpAccount) (opcli.OpAccount, error) { return accs[i], nil }
}

// devnull returns a Deps.Stdout that discards output — a file pointing at
// /dev/null. Simplifies making test output quiet without shuffling logic.
func devnull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestEnsure_AlreadyInitialized_NoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	original := "op_account: EXISTING\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := EnsureInitialized(dir); err != nil {
		t.Fatalf("EnsureInitialized: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != original {
		t.Errorf("config was rewritten: got %q, want %q", string(got), original)
	}
}

func TestRunInteractive_SingleAccount_WritesConfig(t *testing.T) {
	dir := t.TempDir()
	// Use the dir itself as a stable path we can put in allowed_callers.
	fakeExe := filepath.Join(dir, "opbroker")
	if err := os.WriteFile(fakeExe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}

	deps := Deps{
		Lister: &fakeLister{accounts: []opcli.OpAccount{
			{URL: "my.1password.com", Email: "me@example.com", UserUUID: "UUID1"},
		}},
		Chooser: pickIndex(0),
		ExePath: func() (string, error) { return fakeExe, nil },
		Stdout:  devnull(t),
	}

	if err := runInteractive(dir, deps); err != nil {
		t.Fatalf("runInteractive: %v", err)
	}

	// Check config.yaml contents.
	configPath := filepath.Join(dir, "config.yaml")
	cfgBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var parsed struct {
		OpAccount string `yaml:"op_account"`
		Agent     struct {
			AllowedCallers []string `yaml:"allowed_callers"`
			TTL            string   `yaml:"ttl"`
			Socket         string   `yaml:"socket"`
		} `yaml:"agent"`
	}
	if err := yaml.Unmarshal(cfgBytes, &parsed); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if parsed.OpAccount != "UUID1" {
		t.Errorf("op_account = %q, want UUID1", parsed.OpAccount)
	}
	// runInteractive canonicalizes exe via filepath.EvalSymlinks, so expected
	// path must be the canonical form too.
	wantExe, err := filepath.EvalSymlinks(fakeExe)
	if err != nil {
		wantExe = fakeExe
	}
	if len(parsed.Agent.AllowedCallers) != 1 || parsed.Agent.AllowedCallers[0] != wantExe {
		t.Errorf("allowed_callers = %v, want [%s]", parsed.Agent.AllowedCallers, wantExe)
	}
	if parsed.Agent.TTL == "" || parsed.Agent.Socket == "" {
		t.Errorf("expected ttl+socket populated, got %+v", parsed.Agent)
	}

	// Check profiles.yaml contents.
	profilesPath := filepath.Join(dir, "profiles.yaml")
	profBytes, err := os.ReadFile(profilesPath)
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if !strings.Contains(string(profBytes), "profiles: {}") {
		t.Errorf("profiles.yaml missing `profiles: {}`:\n%s", profBytes)
	}
	var profParsed struct {
		Profiles map[string]any `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(profBytes, &profParsed); err != nil {
		t.Fatalf("parse profiles: %v", err)
	}
	if profParsed.Profiles == nil {
		t.Error("profiles map is nil; expected empty map")
	}
	if len(profParsed.Profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profParsed.Profiles))
	}

	// Check permissions.
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	cfgInfo, _ := os.Stat(configPath)
	if cfgInfo.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", cfgInfo.Mode().Perm())
	}
	profInfo, _ := os.Stat(profilesPath)
	if profInfo.Mode().Perm() != 0o600 {
		t.Errorf("profiles mode = %v, want 0600", profInfo.Mode().Perm())
	}
}

func TestRunInteractive_MultipleAccounts_UsesChooser(t *testing.T) {
	dir := t.TempDir()
	deps := Deps{
		Lister: &fakeLister{accounts: []opcli.OpAccount{
			{Email: "a@x", UserUUID: "AAA"},
			{Email: "b@x", UserUUID: "BBB"},
			{Email: "c@x", UserUUID: "CCC"},
		}},
		Chooser: pickIndex(1),
		ExePath: func() (string, error) { return "/opt/opbroker", nil },
		Stdout:  devnull(t),
	}
	if err := runInteractive(dir, deps); err != nil {
		t.Fatalf("runInteractive: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if !strings.Contains(string(cfg), "op_account: BBB") {
		t.Errorf("expected op_account: BBB in config, got:\n%s", cfg)
	}
}

func TestRunInteractive_ZeroAccounts_Errors(t *testing.T) {
	dir := t.TempDir()
	deps := Deps{
		Lister:  &fakeLister{accounts: nil},
		Chooser: pickIndex(0),
		ExePath: func() (string, error) { return "/opt/opbroker", nil },
		Stdout:  devnull(t),
	}
	err := runInteractive(dir, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "op account add") {
		t.Errorf("error should hint at `op account add`, got: %v", err)
	}
}

func TestRunInteractive_OpNotInstalled_Errors(t *testing.T) {
	dir := t.TempDir()
	deps := Deps{
		Lister:  &fakeLister{err: errors.New(`exec: "op": executable file not found in $PATH`)},
		Chooser: pickIndex(0),
		ExePath: func() (string, error) { return "/opt/opbroker", nil },
		Stdout:  devnull(t),
	}
	err := runInteractive(dir, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "install") {
		t.Errorf("error should hint at installation, got: %v", err)
	}
}
