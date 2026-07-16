package main

import (
	"bytes"
	"testing"
)

func TestPrintDryRun_MasksEnvAndInjectedArgs(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]string{
		"FOO_TOKEN":  "s3cret",
		"AWS_REGION": "us-east-1",
	}
	argv := []string{"--account", "account1", "--token", "s3cret", "positional"}
	secrets := map[string]bool{"FOO_TOKEN": true, "--token": true}
	injected := map[string]bool{"--account": true, "--token": true}

	printDryRun(&buf, env, argv, "/bin/foo", secrets, injected)

	want := "opbroker wrapped:\n" +
		"  environment:\n" +
		"    AWS_REGION: us-east-1\n" +
		"    FOO_TOKEN: <masked>\n" +
		"  command:\n" +
		"    /bin/foo --account account1 --token <masked> positional\n" +
		"\n" +
		"(--opbroker-debug set; target not executed)\n" +
		"(<masked> = value sourced from a CONCEALED 1Password field)\n"
	if got := buf.String(); got != want {
		t.Errorf("printDryRun output mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestPrintDryRun_UserSuppliedArgsNeverMasked(t *testing.T) {
	// The user typed --token themselves; opbroker didn't inject it, so we
	// don't know it's a secret and must NOT mask.
	var buf bytes.Buffer
	env := map[string]string{"K": "v"}
	argv := []string{"--token", "userpassword"}
	secrets := map[string]bool{"--token": true} // even if secrets says so...
	injected := map[string]bool{}               // ...opbroker didn't inject it.

	printDryRun(&buf, env, argv, "/bin/foo", secrets, injected)

	got := buf.String()
	if !bytes.Contains([]byte(got), []byte("--token userpassword")) {
		t.Errorf("expected user-supplied token to render verbatim, got:\n%s", got)
	}
}

func TestPrintDryRun_EqualsForm(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]string{}
	argv := []string{"--token=s3cret"}
	secrets := map[string]bool{"--token": true}
	injected := map[string]bool{"--token": true}

	printDryRun(&buf, env, argv, "/bin/foo", secrets, injected)

	got := buf.String()
	if !bytes.Contains([]byte(got), []byte("--token=<masked>")) {
		t.Errorf("expected equals-form to be masked, got:\n%s", got)
	}
}

func TestPrintDryRun_NoMaskedValuesSkipsLegend(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]string{"K": "v"}
	printDryRun(&buf, env, []string{"--x", "y"}, "/bin/foo", nil, nil)

	got := buf.String()
	if bytes.Contains([]byte(got), []byte("<masked>")) {
		t.Errorf("no secrets present but output contains legend:\n%s", got)
	}
}
