package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoad_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
op_account: ACCT123
agent:
  ttl: 15m
  socket: ~/.opbroker/agent.sock
  allowed_callers:
    - /usr/local/bin/opbroker
`)
	writeFile(t, filepath.Join(dir, "profiles.yaml"), `
profiles:
  foo:
    tag: FooService/creds
    account_field: account
    command: /bin/foo.sh
    env:
      FOO_TOKEN: foo_token
  bar:
    tag: Bar/api
    account_field: name
    command: bar
    op_account: OVERRIDE
    env:
      API_KEY: api_key
`)

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if m.Global.OpAccount != "ACCT123" {
		t.Errorf("op_account = %q, want ACCT123", m.Global.OpAccount)
	}
	if m.Global.Agent.TTL != 15*time.Minute {
		t.Errorf("TTL = %v, want 15m", m.Global.Agent.TTL)
	}
	if len(m.Global.Agent.AllowedCallers) != 1 {
		t.Errorf("AllowedCallers len = %d, want 1", len(m.Global.Agent.AllowedCallers))
	}

	foo, err := m.Profile("foo")
	if err != nil {
		t.Fatalf("Profile(foo): %v", err)
	}
	if foo.Name != "foo" {
		t.Errorf("foo.Name = %q, want foo", foo.Name)
	}
	if foo.OpAccount != "ACCT123" {
		t.Errorf("foo inherited op_account = %q, want ACCT123", foo.OpAccount)
	}
	if foo.Env["FOO_TOKEN"] != "foo_token" {
		t.Errorf("foo.Env[FOO_TOKEN] = %q, want foo_token", foo.Env["FOO_TOKEN"])
	}

	bar := m.Profiles["bar"]
	if bar.OpAccount != "OVERRIDE" {
		t.Errorf("bar override op_account = %q, want OVERRIDE", bar.OpAccount)
	}
}

func TestLoad_MissingFilesUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load empty dir: %v", err)
	}
	if m.Global.Agent.TTL != 30*time.Minute {
		t.Errorf("default TTL = %v, want 30m", m.Global.Agent.TTL)
	}
	if m.Global.Agent.Socket == "" {
		t.Error("default socket path empty")
	}
	if len(m.Profiles) != 0 {
		t.Errorf("profiles len = %d, want 0", len(m.Profiles))
	}
}

func TestLoad_EmptyProfilesMap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "profiles.yaml"), "profiles: {}\n")
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(m.Profiles))
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "op_account: [not a string")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error on malformed yaml, got nil")
	}
}

func TestProfileValidate(t *testing.T) {
	cases := []struct {
		name    string
		p       *Profile
		wantErr bool
	}{
		{"valid env only", &Profile{Name: "x", Tag: "t", AccountField: "a", Env: map[string]string{"K": "v"}}, false},
		{"valid args only", &Profile{Name: "x", Tag: "t", AccountField: "a", Args: map[string]string{"--x": "y"}}, false},
		{"missing tag", &Profile{Name: "x", AccountField: "a", Env: map[string]string{"K": "v"}}, true},
		{"missing account_field", &Profile{Name: "x", Tag: "t", Env: map[string]string{"K": "v"}}, true},
		{"missing env AND args", &Profile{Name: "x", Tag: "t", AccountField: "a"}, true},
		{"nil", nil, true},
		{"args flag missing dash", &Profile{Name: "x", Tag: "t", AccountField: "a", Args: map[string]string{"foo": "bar"}}, true},
		{"two ${account} entries", &Profile{Name: "x", Tag: "t", AccountField: "a", Args: map[string]string{
			"--account": ArgTemplateAccount, "--profile": ArgTemplateAccount,
		}}, true},
		{"invalid arg_style", &Profile{Name: "x", Tag: "t", AccountField: "a", Env: map[string]string{"K": "v"}, ArgStyle: "weird"}, true},
		{"invalid arg_placement", &Profile{Name: "x", Tag: "t", AccountField: "a", Env: map[string]string{"K": "v"}, ArgPlacement: "middle"}, true},
		{"valid separate", &Profile{Name: "x", Tag: "t", AccountField: "a", Env: map[string]string{"K": "v"}, ArgStyle: ArgStyleSeparate, ArgPlacement: ArgPlacementFirst}, false},
		{"valid equals+last", &Profile{Name: "x", Tag: "t", AccountField: "a", Env: map[string]string{"K": "v"}, ArgStyle: ArgStyleEquals, ArgPlacement: ArgPlacementLast}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestProfileEffectiveDefaults(t *testing.T) {
	p := &Profile{}
	if p.EffectiveArgStyle() != ArgStyleSeparate {
		t.Errorf("EffectiveArgStyle() = %q, want %q", p.EffectiveArgStyle(), ArgStyleSeparate)
	}
	if p.EffectiveArgPlacement() != ArgPlacementFirst {
		t.Errorf("EffectiveArgPlacement() = %q, want %q", p.EffectiveArgPlacement(), ArgPlacementFirst)
	}
	if p.IdentityFlag() != "" {
		t.Errorf("empty profile IdentityFlag() = %q, want empty", p.IdentityFlag())
	}
}

func TestProfileIdentityFlag(t *testing.T) {
	p := &Profile{Args: map[string]string{
		"--account": ArgTemplateAccount,
		"--region":  "aws_region",
	}}
	if got := p.IdentityFlag(); got != "--account" {
		t.Errorf("IdentityFlag() = %q, want --account", got)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got, err := ExpandPath("~/foo/bar")
	if err != nil {
		t.Fatalf("ExpandPath: %v", err)
	}
	want := filepath.Join(home, "foo/bar")
	if got != want {
		t.Errorf("ExpandPath = %q, want %q", got, want)
	}

	got, err = ExpandPath("/abs/path")
	if err != nil {
		t.Fatalf("ExpandPath abs: %v", err)
	}
	if got != "/abs/path" {
		t.Errorf("abs path changed: %q", got)
	}
}
