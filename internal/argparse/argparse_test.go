package argparse

import (
	"reflect"
	"testing"
)

func TestHasFlag(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		flag string
		want bool
	}{
		{"separate", []string{"--account", "x"}, "--account", true},
		{"equals", []string{"--account=x"}, "--account", true},
		{"missing", []string{"--other", "y"}, "--account", false},
		{"prefix collision", []string{"--accounts", "x"}, "--account", false},
		{"empty argv", nil, "--account", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasFlag(tc.argv, tc.flag); got != tc.want {
				t.Errorf("HasFlag = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractFlag(t *testing.T) {
	cases := []struct {
		name      string
		argv      []string
		flag      string
		wantValue string
		wantOK    bool
	}{
		{"separate", []string{"--account", "acct1", "--other"}, "--account", "acct1", true},
		{"equals", []string{"--account=acct1"}, "--account", "acct1", true},
		{"missing", []string{"--other", "x"}, "--account", "", false},
		{"empty separate", []string{"--account", "--other"}, "--account", "", false},
		{"empty equals", []string{"--account="}, "--account", "", false},
		{"last arg no value", []string{"--account"}, "--account", "", false},
		{"prefix collision", []string{"--accounts", "x"}, "--account", "", false},
		{"short flag ignored", []string{"-a", "x"}, "--account", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotOK := ExtractFlag(tc.argv, tc.flag)
			if gotV != tc.wantValue || gotOK != tc.wantOK {
				t.Errorf("ExtractFlag = (%q,%v), want (%q,%v)", gotV, gotOK, tc.wantValue, tc.wantOK)
			}
		})
	}
}

func TestInject(t *testing.T) {
	cases := []struct {
		name      string
		argv      []string
		pairs     []Pair
		style     Style
		placement Placement
		want      []string
	}{
		{
			"separate first",
			[]string{"user-arg"},
			[]Pair{{Flag: "--account", Value: "acct1"}},
			StyleSeparate, PlacementFirst,
			[]string{"--account", "acct1", "user-arg"},
		},
		{
			"equals first",
			[]string{"user-arg"},
			[]Pair{{Flag: "--account", Value: "acct1"}},
			StyleEquals, PlacementFirst,
			[]string{"--account=acct1", "user-arg"},
		},
		{
			"separate last",
			[]string{"user-arg"},
			[]Pair{{Flag: "--account", Value: "acct1"}},
			StyleSeparate, PlacementLast,
			[]string{"user-arg", "--account", "acct1"},
		},
		{
			"skips existing flag",
			[]string{"--account", "user-picked"},
			[]Pair{{Flag: "--account", Value: "opbroker-picked"}},
			StyleSeparate, PlacementFirst,
			[]string{"--account", "user-picked"},
		},
		{
			"skips existing equals flag",
			[]string{"--account=user-picked"},
			[]Pair{{Flag: "--account", Value: "opbroker-picked"}},
			StyleSeparate, PlacementFirst,
			[]string{"--account=user-picked"},
		},
		{
			"multiple pairs preserved order",
			[]string{"s3", "ls"},
			[]Pair{{Flag: "--profile", Value: "p"}, {Flag: "--region", Value: "r"}},
			StyleSeparate, PlacementFirst,
			[]string{"--profile", "p", "--region", "r", "s3", "ls"},
		},
		{
			"empty pairs returns copy",
			[]string{"a", "b"},
			nil,
			StyleSeparate, PlacementFirst,
			[]string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Inject(tc.argv, tc.pairs, tc.style, tc.placement)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Inject = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTakeFlag(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		flag        string
		wantArgv    []string
		wantValue   string
		wantPresent bool
	}{
		{
			"separate", []string{"--account", "acct1", "other"}, "--account",
			[]string{"other"}, "acct1", true,
		},
		{
			"equals", []string{"--account=acct1", "other"}, "--account",
			[]string{"other"}, "acct1", true,
		},
		{
			"absent", []string{"a", "b"}, "--account",
			[]string{"a", "b"}, "", false,
		},
		{
			"empty separate value (next is a flag) — flag still stripped",
			[]string{"--account", "--other"}, "--account",
			[]string{"--other"}, "", false,
		},
		{
			"empty equals value", []string{"--account="}, "--account",
			[]string{}, "", false,
		},
		{
			"prefix collision preserved",
			[]string{"--accounts", "x", "--account", "y"}, "--account",
			[]string{"--accounts", "x"}, "y", true,
		},
		{
			"multiple copies — remove all, keep first value",
			[]string{"--account", "first", "middle", "--account", "second"}, "--account",
			[]string{"middle"}, "first", true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotArgv, gotValue, gotPresent := TakeFlag(tc.argv, tc.flag)
			if gotPresent != tc.wantPresent {
				t.Errorf("present = %v, want %v", gotPresent, tc.wantPresent)
			}
			if gotValue != tc.wantValue {
				t.Errorf("value = %q, want %q", gotValue, tc.wantValue)
			}
			if !reflect.DeepEqual(gotArgv, tc.wantArgv) {
				t.Errorf("argv = %v, want %v", gotArgv, tc.wantArgv)
			}
		})
	}
}

func TestTakeBoolFlag(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		flag        string
		wantArgv    []string
		wantPresent bool
	}{
		{"absent", []string{"a", "b"}, "--debug", []string{"a", "b"}, false},
		{"present at start", []string{"--debug", "a"}, "--debug", []string{"a"}, true},
		{"present in middle", []string{"a", "--debug", "b"}, "--debug", []string{"a", "b"}, true},
		{"present at end", []string{"a", "--debug"}, "--debug", []string{"a"}, true},
		{"multiple copies", []string{"--debug", "a", "--debug"}, "--debug", []string{"a"}, true},
		{"empty argv", nil, "--debug", []string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotArgv, gotPresent := TakeBoolFlag(tc.argv, tc.flag)
			if gotPresent != tc.wantPresent {
				t.Errorf("present = %v, want %v", gotPresent, tc.wantPresent)
			}
			if !reflect.DeepEqual(gotArgv, tc.wantArgv) {
				t.Errorf("argv = %v, want %v", gotArgv, tc.wantArgv)
			}
		})
	}
}

func TestInject_DoesNotMutateInput(t *testing.T) {
	argv := []string{"a", "b"}
	orig := append([]string{}, argv...)
	_ = Inject(argv, []Pair{{Flag: "--x", Value: "y"}}, StyleSeparate, PlacementFirst)
	if !reflect.DeepEqual(argv, orig) {
		t.Errorf("Inject mutated argv: got %v, want %v", argv, orig)
	}
}
