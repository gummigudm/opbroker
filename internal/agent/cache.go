package agent

import (
	"sync"
	"time"
)

// Cache is the agent's in-memory two-level cache with selection memory.
//
// Level 1 (accountLists) — "what accounts exist for this tag?"
//
//	key:   tag string
//	value: []AccountOption + expiry
//
// Level 2 (resolved) — "what env + args + account values are set for this item?"
//
//	key:   tag ":" account
//	value: *ResolvedResult + expiry
//
// Selection memory: last-used account per profile. No TTL; dies with agent.
type Cache struct {
	mu            sync.RWMutex
	ttl           time.Duration
	now           func() time.Time
	accountLists  map[string]accountListEntry
	resolved      map[string]resolvedEntry
	lastSelection map[string]string // profile → account
}

// ResolvedResult is the fully-resolved payload for one (tag, account) pair:
// credential env vars, argv flag values, sensitivity metadata, and the
// resolved account name. Held in the cache and returned to the client via
// the Response.
type ResolvedResult struct {
	Env     map[string]string
	Args    map[string]string
	Secrets map[string]bool // keys shared with Env or Args; value true = source was CONCEALED
	Account string
}

// Clone returns a deep copy so callers can't mutate the cached value.
func (r *ResolvedResult) Clone() *ResolvedResult {
	if r == nil {
		return nil
	}
	out := &ResolvedResult{Account: r.Account}
	if r.Env != nil {
		out.Env = make(map[string]string, len(r.Env))
		for k, v := range r.Env {
			out.Env[k] = v
		}
	}
	if r.Args != nil {
		out.Args = make(map[string]string, len(r.Args))
		for k, v := range r.Args {
			out.Args[k] = v
		}
	}
	if r.Secrets != nil {
		out.Secrets = make(map[string]bool, len(r.Secrets))
		for k, v := range r.Secrets {
			out.Secrets[k] = v
		}
	}
	return out
}

type accountListEntry struct {
	options []AccountOption
	expires time.Time
}

type resolvedEntry struct {
	result  *ResolvedResult
	expires time.Time
}

// NewCache creates a Cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:           ttl,
		now:           time.Now,
		accountLists:  map[string]accountListEntry{},
		resolved:      map[string]resolvedEntry{},
		lastSelection: map[string]string{},
	}
}

// GetAccounts returns the cached account list for a tag, if fresh.
func (c *Cache) GetAccounts(tag string) ([]AccountOption, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.accountLists[tag]
	if !ok || c.now().After(entry.expires) {
		return nil, false
	}
	out := make([]AccountOption, len(entry.options))
	copy(out, entry.options)
	return out, true
}

// SetAccounts stores an account list for a tag with the configured TTL.
func (c *Cache) SetAccounts(tag string, opts []AccountOption) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]AccountOption, len(opts))
	copy(cp, opts)
	c.accountLists[tag] = accountListEntry{options: cp, expires: c.now().Add(c.ttl)}
}

// resolvedKey builds the composite key for the resolved cache.
func resolvedKey(tag, account string) string {
	return tag + ":" + account
}

// GetResolved returns the cached resolved payload for tag+account, if fresh.
// The returned pointer is a defensive copy — mutating it does not affect the
// cache.
func (c *Cache) GetResolved(tag, account string) (*ResolvedResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.resolved[resolvedKey(tag, account)]
	if !ok || c.now().After(entry.expires) {
		return nil, false
	}
	return entry.result.Clone(), true
}

// SetResolved stores the resolved payload for tag+account with the configured TTL.
func (c *Cache) SetResolved(tag, account string, r *ResolvedResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolved[resolvedKey(tag, account)] = resolvedEntry{result: r.Clone(), expires: c.now().Add(c.ttl)}
}

// RememberSelection records the last account chosen for a profile.
func (c *Cache) RememberSelection(profile, account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSelection[profile] = account
}

// LastSelection returns the last remembered account for a profile.
func (c *Cache) LastSelection(profile string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	a, ok := c.lastSelection[profile]
	return a, ok
}

// Clear evicts everything.
func (c *Cache) Clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.accountLists) + len(c.resolved) + len(c.lastSelection)
	c.accountLists = map[string]accountListEntry{}
	c.resolved = map[string]resolvedEntry{}
	c.lastSelection = map[string]string{}
	return n
}

// Counts returns per-map entry counts (for status).
func (c *Cache) Counts() (accounts, resolved, selections int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.accountLists), len(c.resolved), len(c.lastSelection)
}

// TTL returns the configured TTL.
func (c *Cache) TTL() time.Duration { return c.ttl }
