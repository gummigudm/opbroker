package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFetcher records call counts so tests can assert cache behavior.
type fakeFetcher struct {
	accounts     []AccountOption
	listCalls    atomic.Int32
	resolveCalls atomic.Int32
	fields       map[string]string // field-name → value (returned by ResolveFields)
	secretFields map[string]bool   // field-name → true means CONCEALED
	listErr      error
	resolveErr   error
}

func (f *fakeFetcher) ListAccounts(tag, accountField, opAccount string) ([]AccountOption, error) {
	f.listCalls.Add(1)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.accounts, nil
}

func (f *fakeFetcher) ResolveFields(itemID string, fieldNames []string, opAccount string) (map[string]ResolvedField, error) {
	f.resolveCalls.Add(1)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	out := make(map[string]ResolvedField, len(fieldNames))
	for _, name := range fieldNames {
		if v, ok := f.fields[name]; ok {
			out[name] = ResolvedField{Value: v, Secret: f.secretFields[name]}
		}
	}
	return out, nil
}

// fakeProfiles is a simple ProfileLookup.
type fakeProfiles map[string]*ProfileConfig

func (f fakeProfiles) Profile(name string) (*ProfileConfig, error) {
	p, ok := f[name]
	if !ok {
		return nil, errors.New("no such profile")
	}
	return p, nil
}

// startTestServer starts a server on a unix socket in tempDir; NOTE this test
// exercises the server end-to-end but bypasses security.VerifyPeer by
// connecting from the same process — since the same executable is talking to
// itself, the check only passes if the test binary's path is included. We
// bypass by leaving allowedCallers empty and short-circuiting via a hook,
// but that hook doesn't exist yet, so we instead connect and expect the
// server to accept us because we mark ourselves as allowed.
//
// The cleanest way: put the test binary's path in AllowedCallers.
func startTestServer(t *testing.T, fetcher Fetcher, profiles ProfileLookup) (string, func()) {
	t.Helper()

	// macOS caps sun_path at 104 bytes, so t.TempDir() (deep nested path) can
	// overflow. Use /tmp/opbroker-test-XXXX for guaranteed-short paths.
	dir, err := os.MkdirTemp("/tmp", "opbroker-test-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	pidFile := filepath.Join(dir, "p")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	s := NewServer(Config{
		SocketPath:     socket,
		PIDFile:        pidFile,
		AllowedCallers: []string{exe},
		TTL:            time.Minute,
		Fetcher:        fetcher,
		Profiles:       profiles,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Start(ctx); err != nil {
			t.Errorf("server.Start: %v", err)
		}
	}()

	// Wait for socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stop := func() {
		s.Stop()
		cancel()
		<-done
	}
	return socket, stop
}

func dial(t *testing.T, socket string) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func TestServer_StatusRoundtrip(t *testing.T) {
	fetcher := &fakeFetcher{}
	socket, stop := startTestServer(t, fetcher, fakeProfiles{})
	defer stop()

	conn := dial(t, socket)
	defer conn.Close()

	if err := WriteMessage(conn, Request{Type: TypeStatus}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp Response
	if err := ReadMessage(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Type != TypeOK {
		t.Fatalf("resp type = %q, want ok; error=%q", resp.Type, resp.Error)
	}
	if resp.Status == nil || resp.Status.PID != os.Getpid() {
		t.Errorf("status = %+v", resp.Status)
	}
}

func TestServer_GetSingleAccount_CachesResolution(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts: []AccountOption{{Account: "only", Title: "t", ItemID: "id1"}},
		fields:   map[string]string{"foo_token": "secret"},
	}
	profiles := fakeProfiles{
		"foo": &ProfileConfig{
			Tag: "FooService/creds", AccountField: "account",
			Env: map[string]string{"FOO_TOKEN": "foo_token"},
		},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	do := func() Response {
		conn := dial(t, socket)
		defer conn.Close()
		if err := WriteMessage(conn, Request{Type: TypeGet, Profile: "foo"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		var r Response
		if err := ReadMessage(conn, &r); err != nil {
			t.Fatalf("read: %v", err)
		}
		return r
	}

	// First call — should populate both caches.
	r := do()
	if r.Type != TypeOK || r.Env["FOO_TOKEN"] != "secret" {
		t.Fatalf("first call: %+v", r)
	}
	if r.Account != "only" {
		t.Errorf("first call Account = %q, want only", r.Account)
	}
	if fetcher.listCalls.Load() != 1 || fetcher.resolveCalls.Load() != 1 {
		t.Errorf("first call counts list=%d resolve=%d", fetcher.listCalls.Load(), fetcher.resolveCalls.Load())
	}

	// Second call — should be pure cache hit.
	r = do()
	if r.Type != TypeOK || r.Env["FOO_TOKEN"] != "secret" {
		t.Fatalf("second call: %+v", r)
	}
	if fetcher.listCalls.Load() != 1 || fetcher.resolveCalls.Load() != 1 {
		t.Errorf("second call caused re-fetch: list=%d resolve=%d", fetcher.listCalls.Load(), fetcher.resolveCalls.Load())
	}
}

func TestServer_GetMultipleAccounts_ReturnsSelect(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts: []AccountOption{
			{Account: "a1", Title: "T1", ItemID: "id1"},
			{Account: "a2", Title: "T2", ItemID: "id2"},
		},
		fields: map[string]string{"foo_token": "secret1"},
	}
	profiles := fakeProfiles{
		"foo": &ProfileConfig{
			Tag: "tag", AccountField: "account",
			Env: map[string]string{"FOO_TOKEN": "foo_token"},
		},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	// No --account → expect select_required.
	conn := dial(t, socket)
	if err := WriteMessage(conn, Request{Type: TypeGet, Profile: "foo"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var r Response
	if err := ReadMessage(conn, &r); err != nil {
		t.Fatalf("read: %v", err)
	}
	conn.Close()
	if r.Type != TypeSelectRequired {
		t.Fatalf("expected select_required, got %q (err=%q)", r.Type, r.Error)
	}
	if len(r.Options) != 2 {
		t.Errorf("options len = %d, want 2", len(r.Options))
	}

	// Now select a1.
	conn = dial(t, socket)
	defer conn.Close()
	if err := WriteMessage(conn, Request{Type: TypeSelect, Profile: "foo", Account: "a1"}); err != nil {
		t.Fatalf("write select: %v", err)
	}
	if err := ReadMessage(conn, &r); err != nil {
		t.Fatalf("read select: %v", err)
	}
	if r.Type != TypeOK || r.Env["FOO_TOKEN"] != "secret1" {
		t.Fatalf("select response: %+v", r)
	}
	if r.Account != "a1" {
		t.Errorf("Account = %q, want a1", r.Account)
	}
}

func TestServer_Refresh(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts: []AccountOption{{Account: "only", ItemID: "id"}},
		fields:   map[string]string{"k": "v"},
	}
	profiles := fakeProfiles{
		"foo": &ProfileConfig{Tag: "t", AccountField: "a", Env: map[string]string{"K": "k"}},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	// Populate cache.
	conn := dial(t, socket)
	_ = WriteMessage(conn, Request{Type: TypeGet, Profile: "foo"})
	var r Response
	_ = ReadMessage(conn, &r)
	conn.Close()
	if fetcher.resolveCalls.Load() != 1 {
		t.Fatalf("setup failed, resolveCalls=%d", fetcher.resolveCalls.Load())
	}

	// Refresh.
	conn = dial(t, socket)
	_ = WriteMessage(conn, Request{Type: TypeRefresh})
	_ = ReadMessage(conn, &r)
	conn.Close()
	if r.Type != TypeOK {
		t.Fatalf("refresh resp: %+v", r)
	}
	if r.Meta["cleared"] == "" {
		t.Errorf("refresh Meta[cleared] empty; want non-empty count")
	}

	// Next get should re-fetch.
	conn = dial(t, socket)
	_ = WriteMessage(conn, Request{Type: TypeGet, Profile: "foo"})
	_ = ReadMessage(conn, &r)
	conn.Close()
	if fetcher.resolveCalls.Load() != 2 {
		t.Errorf("expected re-fetch after refresh, resolveCalls=%d", fetcher.resolveCalls.Load())
	}
}

// TestServer_GetWithAccount_SkipsPicker: when --account is provided, the agent
// must resolve credentials directly and never return select_required, even if
// multiple accounts exist for the tag.
func TestServer_GetWithAccount_SkipsPicker(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts: []AccountOption{
			{Account: "account1", Title: "T1", ItemID: "id1"},
			{Account: "account2", Title: "T2", ItemID: "id2"},
		},
		fields: map[string]string{"foo_token": "test-token-1"},
	}
	profiles := fakeProfiles{
		"foo": &ProfileConfig{
			Tag: "FooService/creds", AccountField: "account",
			Env: map[string]string{"FOO_TOKEN": "foo_token"},
		},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	conn := dial(t, socket)
	defer conn.Close()
	if err := WriteMessage(conn, Request{Type: TypeGet, Profile: "foo", Account: "account1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var r Response
	if err := ReadMessage(conn, &r); err != nil {
		t.Fatalf("read: %v", err)
	}
	if r.Type != TypeOK {
		t.Fatalf("expected ok, got %q (err=%q, opts=%d)", r.Type, r.Error, len(r.Options))
	}
	if r.Env["FOO_TOKEN"] != "test-token-1" {
		t.Errorf("FOO_TOKEN = %q, want test-token-1", r.Env["FOO_TOKEN"])
	}
	if r.Account != "account1" {
		t.Errorf("Account = %q, want account1", r.Account)
	}
}

// TestServer_SecretsPropagate: when a resolved field originated from a
// CONCEALED 1P entry, the Response.Secrets map must reflect that under the
// env/arg name it maps to.
func TestServer_SecretsPropagate(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts:     []AccountOption{{Account: "only", Title: "t", ItemID: "id1"}},
		fields:       map[string]string{"foo_token": "s3cret", "region_name": "us-east-1"},
		secretFields: map[string]bool{"foo_token": true}, // region_name is plain
	}
	profiles := fakeProfiles{
		"foo": &ProfileConfig{
			Tag: "FooService/creds", AccountField: "account",
			Env: map[string]string{
				"FOO_TOKEN":  "foo_token",
				"AWS_REGION": "region_name",
			},
			Args: map[string]string{
				"--token":  "foo_token",
				"--region": "region_name",
			},
		},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	conn := dial(t, socket)
	defer conn.Close()
	if err := WriteMessage(conn, Request{Type: TypeGet, Profile: "foo"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var r Response
	if err := ReadMessage(conn, &r); err != nil {
		t.Fatalf("read: %v", err)
	}
	if r.Type != TypeOK {
		t.Fatalf("resp type = %q, want ok; error=%q", r.Type, r.Error)
	}
	// Env: FOO_TOKEN is secret, AWS_REGION is not.
	if !r.Secrets["FOO_TOKEN"] {
		t.Errorf("Secrets[FOO_TOKEN] = false, want true")
	}
	if r.Secrets["AWS_REGION"] {
		t.Errorf("Secrets[AWS_REGION] = true, want false")
	}
	// Args: --token is secret, --region is not.
	if !r.Secrets["--token"] {
		t.Errorf("Secrets[--token] = false, want true")
	}
	if r.Secrets["--region"] {
		t.Errorf("Secrets[--region] = true, want false")
	}
}

// TestServer_EnvTemplateExpansion: ${account} and ${title} in env values
// expand from the currently-selected item without touching the fetcher, and
// are never marked secret.
func TestServer_EnvTemplateExpansion(t *testing.T) {
	fetcher := &fakeFetcher{
		accounts:     []AccountOption{{Account: "bar-prod", Title: "Bar Prod Creds", ItemID: "id1"}},
		fields:       map[string]string{"bar_token": "s3cret"},
		secretFields: map[string]bool{"bar_token": true},
	}
	profiles := fakeProfiles{
		"bar": &ProfileConfig{
			Tag: "BarService/creds", AccountField: "account",
			Env: map[string]string{
				"BAR_TOKEN":   "bar_token",  // 1P field
				"BAR_ACCOUNT": "${account}", // template
				"BAR_TITLE":   "${title}",   // template
			},
		},
	}
	socket, stop := startTestServer(t, fetcher, profiles)
	defer stop()

	conn := dial(t, socket)
	defer conn.Close()
	if err := WriteMessage(conn, Request{Type: TypeGet, Profile: "bar"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var r Response
	if err := ReadMessage(conn, &r); err != nil {
		t.Fatalf("read: %v", err)
	}
	if r.Type != TypeOK {
		t.Fatalf("resp type = %q, want ok; error=%q", r.Type, r.Error)
	}
	if r.Env["BAR_TOKEN"] != "s3cret" {
		t.Errorf("BAR_TOKEN = %q, want s3cret", r.Env["BAR_TOKEN"])
	}
	if r.Env["BAR_ACCOUNT"] != "bar-prod" {
		t.Errorf("BAR_ACCOUNT = %q, want bar-prod", r.Env["BAR_ACCOUNT"])
	}
	if r.Env["BAR_TITLE"] != "Bar Prod Creds" {
		t.Errorf("BAR_TITLE = %q, want 'Bar Prod Creds'", r.Env["BAR_TITLE"])
	}
	// Only the real 1P-sourced entry is secret; templates are not.
	if !r.Secrets["BAR_TOKEN"] {
		t.Errorf("BAR_TOKEN should be secret")
	}
	if r.Secrets["BAR_ACCOUNT"] || r.Secrets["BAR_TITLE"] {
		t.Errorf("templates should not be secret; got %+v", r.Secrets)
	}
}

func TestServer_UnknownProfile(t *testing.T) {
	socket, stop := startTestServer(t, &fakeFetcher{}, fakeProfiles{})
	defer stop()

	conn := dial(t, socket)
	defer conn.Close()
	_ = WriteMessage(conn, Request{Type: TypeGet, Profile: "missing"})
	var r Response
	_ = ReadMessage(conn, &r)
	if r.Type != TypeError {
		t.Errorf("expected error, got %+v", r)
	}
}
