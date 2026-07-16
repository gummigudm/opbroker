package agent

import (
	"testing"
	"time"
)

func TestCache_AccountsRoundtripAndExpiry(t *testing.T) {
	c := NewCache(1 * time.Minute)
	fakeNow := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return fakeNow }

	opts := []AccountOption{{Account: "a1", Title: "t1", ItemID: "id1"}}
	c.SetAccounts("tag", opts)

	got, ok := c.GetAccounts("tag")
	if !ok || len(got) != 1 || got[0].Account != "a1" {
		t.Fatalf("GetAccounts fresh: got=%v ok=%v", got, ok)
	}

	// Advance beyond TTL.
	fakeNow = fakeNow.Add(2 * time.Minute)
	if _, ok := c.GetAccounts("tag"); ok {
		t.Fatal("expected expiry")
	}
}

func TestCache_ResolvedRoundtripAndExpiry(t *testing.T) {
	c := NewCache(30 * time.Second)
	fakeNow := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return fakeNow }

	c.SetResolved("tag", "acct", &ResolvedResult{
		Env:     map[string]string{"K": "v"},
		Args:    map[string]string{"--flag": "val"},
		Account: "acct",
	})
	got, ok := c.GetResolved("tag", "acct")
	if !ok || got.Env["K"] != "v" || got.Args["--flag"] != "val" || got.Account != "acct" {
		t.Fatalf("GetResolved fresh: got=%+v ok=%v", got, ok)
	}

	// Isolation: mutating returned maps must not affect cache.
	got.Env["K"] = "tampered"
	got.Args["--flag"] = "tampered"
	got2, _ := c.GetResolved("tag", "acct")
	if got2.Env["K"] != "v" || got2.Args["--flag"] != "val" {
		t.Errorf("cache mutated via returned maps: %+v", got2)
	}

	fakeNow = fakeNow.Add(time.Minute)
	if _, ok := c.GetResolved("tag", "acct"); ok {
		t.Fatal("expected expiry")
	}
}

func TestCache_SelectionMemoryAndClear(t *testing.T) {
	c := NewCache(time.Hour)
	if _, ok := c.LastSelection("foo"); ok {
		t.Fatal("unexpected selection")
	}
	c.RememberSelection("foo", "acct1")
	got, ok := c.LastSelection("foo")
	if !ok || got != "acct1" {
		t.Errorf("LastSelection = %q,%v", got, ok)
	}

	c.SetAccounts("tag", []AccountOption{{Account: "a"}})
	c.SetResolved("tag", "a", &ResolvedResult{Env: map[string]string{"K": "v"}, Account: "a"})

	acc, res, sel := c.Counts()
	if acc != 1 || res != 1 || sel != 1 {
		t.Errorf("counts before clear: %d/%d/%d", acc, res, sel)
	}

	n := c.Clear()
	if n != 3 {
		t.Errorf("Clear returned %d, want 3", n)
	}
	acc, res, sel = c.Counts()
	if acc != 0 || res != 0 || sel != 0 {
		t.Errorf("counts after clear: %d/%d/%d", acc, res, sel)
	}
}
