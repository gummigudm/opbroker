package main

import (
	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/config"
)

// profileLookup adapts *config.Merged to agent.ProfileLookup.
type profileLookup struct{ m *config.Merged }

func (p profileLookup) Profile(name string) (*agent.ProfileConfig, error) {
	prof, err := p.m.Profile(name)
	if err != nil {
		return nil, err
	}
	if err := prof.Validate(); err != nil {
		return nil, err
	}
	return toAgentProfile(prof), nil
}

// toAgentProfile converts a config.Profile to the wire type agent.ProfileConfig.
// Kept as a helper so run.go's inline-config path can produce a wire-compatible
// value without duplicating the field-copy logic.
func toAgentProfile(prof *config.Profile) *agent.ProfileConfig {
	return &agent.ProfileConfig{
		Tag:          prof.Tag,
		AccountField: prof.AccountField,
		Env:          prof.Env,
		OpAccount:    prof.OpAccount,
		Args:         prof.Args,
		ArgStyle:     prof.EffectiveArgStyle(),
		ArgPlacement: prof.EffectiveArgPlacement(),
	}
}
