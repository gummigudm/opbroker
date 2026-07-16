// Package config loads opbroker configuration files.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultTTL        = 30 * time.Minute
	defaultSocketPath = "~/.opbroker/run/agent.sock"

	// AccountFieldTitle is the reserved value for `account_field` that tells
	// opbroker to use the 1Password item's title as the account identifier,
	// bypassing field lookup entirely. Useful for items that don't have a
	// dedicated account/name field.
	AccountFieldTitle = "title"

	// ArgTemplateAccount and ArgTemplateTitle are reserved values that can
	// appear in a profile's `args:` map. They expand to the resolved account
	// name and item title respectively without touching 1Password. Any other
	// value in `args:` is treated as a 1P field name.
	ArgTemplateAccount = "${account}"
	ArgTemplateTitle   = "${title}"

	// ArgStyleSeparate writes injected args as `--flag value`. ArgStyleEquals
	// writes them as `--flag=value`. Default: separate.
	ArgStyleSeparate = "separate"
	ArgStyleEquals   = "equals"

	// ArgPlacementFirst injects flags immediately after the command name.
	// ArgPlacementLast appends them after any user-supplied args. Default: first.
	ArgPlacementFirst = "first"
	ArgPlacementLast  = "last"
)

// Global is the top-level config from config.yaml.
type Global struct {
	OpAccount string      `yaml:"op_account"`
	Agent     AgentConfig `yaml:"agent"`
}

// AgentConfig holds agent-specific settings.
type AgentConfig struct {
	TTL            time.Duration `yaml:"ttl"`
	Socket         string        `yaml:"socket"`
	AllowedCallers []string      `yaml:"allowed_callers"`
}

// Profile is a single named executable profile.
type Profile struct {
	Name         string            `yaml:"-"`
	Tag          string            `yaml:"tag"`
	AccountField string            `yaml:"account_field"`
	Command      string            `yaml:"command"`
	Env          map[string]string `yaml:"env"`
	OpAccount    string            `yaml:"op_account,omitempty"`
	Args         map[string]string `yaml:"args,omitempty"`          // flag → 1P field name or ${account}/${title}
	ArgStyle     string            `yaml:"arg_style,omitempty"`     // "separate" (default) | "equals"
	ArgPlacement string            `yaml:"arg_placement,omitempty"` // "first" (default) | "last"
}

// Profiles is the map from profiles.yaml.
type Profiles struct {
	Profiles map[string]*Profile `yaml:"profiles"`
}

// Merged is the resolved configuration ready for use.
type Merged struct {
	Global   Global
	Profiles map[string]*Profile
}

// DefaultDir returns ~/.opbroker.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".opbroker"), nil
}

// ExpandPath expands a leading "~" to the user's home directory.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

// Load reads config.yaml and profiles.yaml from the given directory.
// Either file may be missing; only the intersection of what's needed for a
// given command will be validated by callers.
func Load(dir string) (*Merged, error) {
	m := &Merged{
		Profiles: map[string]*Profile{},
	}

	// Global config.
	globalPath := filepath.Join(dir, "config.yaml")
	if data, err := os.ReadFile(globalPath); err == nil {
		if err := yaml.Unmarshal(data, &m.Global); err != nil {
			return nil, fmt.Errorf("parse %s: %w", globalPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", globalPath, err)
	}

	// Profiles.
	profilesPath := filepath.Join(dir, "profiles.yaml")
	if data, err := os.ReadFile(profilesPath); err == nil {
		var p Profiles
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", profilesPath, err)
		}
		for name, prof := range p.Profiles {
			if prof == nil {
				continue
			}
			prof.Name = name
			m.Profiles[name] = prof
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", profilesPath, err)
	}

	// Apply defaults.
	if m.Global.Agent.TTL == 0 {
		m.Global.Agent.TTL = defaultTTL
	}
	if m.Global.Agent.Socket == "" {
		m.Global.Agent.Socket = defaultSocketPath
	}

	// Inherit op_account into profiles that don't override it.
	for _, prof := range m.Profiles {
		if prof.OpAccount == "" {
			prof.OpAccount = m.Global.OpAccount
		}
	}

	return m, nil
}

// SocketPath returns the resolved (expanded) socket path.
func (m *Merged) SocketPath() (string, error) {
	return ExpandPath(m.Global.Agent.Socket)
}

// Profile fetches a profile by name.
func (m *Merged) Profile(name string) (*Profile, error) {
	p, ok := m.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile for %q not found", name)
	}
	return p, nil
}

// PIDFilePath returns the resolved PID file path (sibling of the socket).
func (m *Merged) PIDFilePath() (string, error) {
	sock, err := m.SocketPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(sock), "agent.pid"), nil
}

// Validate checks that a profile has the fields required to fetch credentials.
func (p *Profile) Validate() error {
	if p == nil {
		return errors.New("nil profile")
	}
	var missing []string
	if p.Tag == "" {
		missing = append(missing, "tag")
	}
	if p.AccountField == "" {
		missing = append(missing, "account_field")
	}
	if len(p.Env) == 0 && len(p.Args) == 0 {
		// A profile must produce *something* — either env vars, args, or both.
		missing = append(missing, "env or args")
	}
	if len(missing) > 0 {
		return fmt.Errorf("profile %q missing required fields: %s", p.Name, strings.Join(missing, ", "))
	}

	// Args-specific validation.
	accountArgSeen := 0
	for flag, source := range p.Args {
		if !strings.HasPrefix(flag, "-") {
			return fmt.Errorf("profile %q: args key %q must start with '-'", p.Name, flag)
		}
		if source == ArgTemplateAccount {
			accountArgSeen++
		}
	}
	if accountArgSeen > 1 {
		return fmt.Errorf("profile %q: at most one args entry may use %s", p.Name, ArgTemplateAccount)
	}

	switch p.ArgStyle {
	case "", ArgStyleSeparate, ArgStyleEquals:
	default:
		return fmt.Errorf("profile %q: arg_style must be %q or %q, got %q", p.Name, ArgStyleSeparate, ArgStyleEquals, p.ArgStyle)
	}

	switch p.ArgPlacement {
	case "", ArgPlacementFirst, ArgPlacementLast:
	default:
		return fmt.Errorf("profile %q: arg_placement must be %q or %q, got %q", p.Name, ArgPlacementFirst, ArgPlacementLast, p.ArgPlacement)
	}

	return nil
}

// EffectiveArgStyle returns ArgStyle with the default (separate) filled in.
func (p *Profile) EffectiveArgStyle() string {
	if p.ArgStyle == "" {
		return ArgStyleSeparate
	}
	return p.ArgStyle
}

// EffectiveArgPlacement returns ArgPlacement with the default (first) filled in.
func (p *Profile) EffectiveArgPlacement() string {
	if p.ArgPlacement == "" {
		return ArgPlacementFirst
	}
	return p.ArgPlacement
}

// IdentityFlag returns the flag whose Args value is ${account} (the extraction
// target for account identity), or "" if the profile has no such entry.
func (p *Profile) IdentityFlag() string {
	for flag, source := range p.Args {
		if source == ArgTemplateAccount {
			return flag
		}
	}
	return ""
}
