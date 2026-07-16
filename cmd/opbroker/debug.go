package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// DebugFlag is the boolean flag users add to a wrapped command's argv to see
// what opbroker would exec without actually running the target.
const DebugFlag = "--opbroker-debug"

const maskedPlaceholder = "<masked>"

// printDryRun writes a human-readable summary of what opbroker would exec.
//
//	env         resolved env vars (name → value)
//	argv        final target argv (already includes any injections)
//	target      absolute path to the target executable
//	secrets     keys shared with env / injectedFlags; true → value came from a
//	            CONCEALED 1P field and should be masked
//	injected    set of flag names opbroker injected into argv (rather than the
//	            user typing them). Masking only applies to values that
//	            opbroker itself put into argv; user-supplied values are always
//	            shown verbatim.
func printDryRun(w io.Writer, env map[string]string, argv []string, target string, secrets map[string]bool, injected map[string]bool) {
	fmt.Fprintln(w, "opbroker wrapped:")

	fmt.Fprintln(w, "  environment:")
	if len(env) == 0 {
		fmt.Fprintln(w, "    (none)")
	} else {
		names := make([]string, 0, len(env))
		for k := range env {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v := env[k]
			if secrets[k] {
				v = maskedPlaceholder
			}
			fmt.Fprintf(w, "    %s: %s\n", k, v)
		}
	}

	fmt.Fprintln(w, "  command:")
	fmt.Fprintf(w, "    %s", target)
	tokens := formatArgvForDisplay(argv, secrets, injected)
	for _, t := range tokens {
		fmt.Fprintf(w, " %s", t)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "(--opbroker-debug set; target not executed)")
	if hasAnyMasked(env, argv, secrets, injected) {
		fmt.Fprintln(w, "(<masked> = value sourced from a CONCEALED 1Password field)")
	}
}

// formatArgvForDisplay walks argv and returns each token as it should be
// rendered, replacing values that opbroker injected AND that are marked
// secret with maskedPlaceholder. Both separate ("--flag value") and equals
// ("--flag=value") forms are handled.
func formatArgvForDisplay(argv []string, secrets, injected map[string]bool) []string {
	out := make([]string, 0, len(argv))
	i := 0
	for i < len(argv) {
		tok := argv[i]
		// equals form: --flag=value
		if eq := strings.Index(tok, "="); eq > 0 {
			flag := tok[:eq]
			if injected[flag] && secrets[flag] {
				out = append(out, flag+"="+maskedPlaceholder)
				i++
				continue
			}
		}
		// separate form: --flag value (both tokens exist)
		if injected[tok] && secrets[tok] && i+1 < len(argv) {
			out = append(out, tok, maskedPlaceholder)
			i += 2
			continue
		}
		out = append(out, tok)
		i++
	}
	return out
}

// hasAnyMasked reports whether the summary is going to contain a masked value
// — used to decide whether to print the legend line.
func hasAnyMasked(env map[string]string, argv []string, secrets, injected map[string]bool) bool {
	for k := range env {
		if secrets[k] {
			return true
		}
	}
	for _, tok := range argv {
		flag := tok
		if eq := strings.Index(tok, "="); eq > 0 {
			flag = tok[:eq]
		}
		if injected[flag] && secrets[flag] {
			return true
		}
	}
	return false
}
