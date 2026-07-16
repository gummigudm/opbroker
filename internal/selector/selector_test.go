package selector

import (
	"errors"
	"os"
	"testing"

	"github.com/gummigudm/opbroker/internal/agent"
)

// TestPick_SingleAutoSelects: len==1 returns immediately without touching /dev/tty.
func TestPick_SingleAutoSelects(t *testing.T) {
	only := agent.AccountOption{Account: "only", Title: "t", ItemID: "id"}
	got, err := Pick([]agent.AccountOption{only}, "pick")
	if err != nil {
		t.Fatalf("Pick single: %v", err)
	}
	if got != only {
		t.Errorf("got %+v, want %+v", got, only)
	}
}

// TestPick_NoTTY_ReturnsErrNoTTY: without a controlling terminal, Pick returns
// a well-formed error instead of a raw open() failure.
func TestPick_NoTTY_ReturnsErrNoTTY(t *testing.T) {
	// Guard: this environment must actually lack /dev/tty for the test to be
	// meaningful. If /dev/tty is openable, skip.
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		f.Close()
		t.Skip("this env has a /dev/tty; can't test the no-tty path")
	}
	opts := []agent.AccountOption{
		{Account: "a"}, {Account: "b"},
	}
	_, err := Pick(opts, "pick")
	if !errors.Is(err, ErrNoTTY) {
		t.Errorf("got %v, want ErrNoTTY", err)
	}
}

func TestFilter(t *testing.T) {
	opts := []agent.AccountOption{
		{Account: "a1"}, {Account: "a2"}, {Account: "a3"},
	}
	got := Filter(opts, "")
	if len(got) != 3 {
		t.Errorf("empty filter len = %d, want 3", len(got))
	}
	got = Filter(opts, "a2")
	if len(got) != 1 || got[0].Account != "a2" {
		t.Errorf("filter a2 = %v", got)
	}
	got = Filter(opts, "missing")
	if len(got) != 0 {
		t.Errorf("filter missing = %v", got)
	}
}
