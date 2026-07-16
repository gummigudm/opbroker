// Package argparse extracts and injects flag/value pairs in a target command's
// argv. It's used by opbroker to (a) read the account identity flag from a
// user-supplied argv, and (b) inject resolved profile args before exec'ing
// the target.
//
// It intentionally handles only long-form flags (--flag). Short flags and
// GNU-style flag bundling are out of scope — profiles must configure exact
// long flag names.
package argparse

import "strings"

// Style controls how injected flag/value pairs are written.
type Style int

const (
	// StyleSeparate writes "--flag value" (two argv entries).
	StyleSeparate Style = iota
	// StyleEquals writes "--flag=value" (one argv entry).
	StyleEquals
)

// Placement controls where injected pairs land in the target argv.
type Placement int

const (
	// PlacementFirst prepends injected pairs before existing argv entries.
	PlacementFirst Placement = iota
	// PlacementLast appends injected pairs after existing argv entries.
	PlacementLast
)

// Pair is a single flag/value to inject.
type Pair struct {
	Flag  string
	Value string
}

// HasFlag reports whether argv already contains flag in either separate
// ("--flag value") or equals ("--flag=value") form. Prefix matches like
// "--flags" against a search for "--flag" do NOT count.
func HasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
		if strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// ExtractFlag returns the value associated with flag in argv, searching both
// styles. Missing or empty values return ("", false). A flag whose next argv
// entry looks like another flag (starts with "-") is treated as missing so
// callers can fall through to picker/last-selection semantics.
func ExtractFlag(argv []string, flag string) (string, bool) {
	prefix := flag + "="
	for i, a := range argv {
		if a == flag {
			if i+1 >= len(argv) {
				return "", false
			}
			v := argv[i+1]
			if v == "" || strings.HasPrefix(v, "-") {
				return "", false
			}
			return v, true
		}
		if strings.HasPrefix(a, prefix) {
			v := strings.TrimPrefix(a, prefix)
			if v == "" {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

// TakeBoolFlag removes every occurrence of flag from argv (boolean form only
// — flag has no value). Returns the remaining argv and true if flag was
// present at least once.
func TakeBoolFlag(argv []string, flag string) ([]string, bool) {
	out := make([]string, 0, len(argv))
	found := false
	for _, a := range argv {
		if a == flag {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// Inject returns argv with the given pairs added. Pairs whose flag is already
// present in argv (in either style) are skipped so the user's explicit choice
// wins. `pairs` order in the output is preserved to give callers a
// deterministic layout.
func Inject(argv []string, pairs []Pair, style Style, placement Placement) []string {
	var added []string
	for _, p := range pairs {
		if HasFlag(argv, p.Flag) {
			continue
		}
		switch style {
		case StyleEquals:
			added = append(added, p.Flag+"="+p.Value)
		default: // StyleSeparate
			added = append(added, p.Flag, p.Value)
		}
	}
	if len(added) == 0 {
		out := make([]string, len(argv))
		copy(out, argv)
		return out
	}
	switch placement {
	case PlacementLast:
		return append(append([]string{}, argv...), added...)
	default: // PlacementFirst
		return append(append([]string{}, added...), argv...)
	}
}
