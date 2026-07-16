package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gummigudm/opbroker/internal/security"
)

// Fetcher resolves account lists and field values from the source of truth
// (typically the `op` CLI). It is abstracted so tests can substitute a fake.
type Fetcher interface {
	ListAccounts(tag, accountField, opAccount string) ([]AccountOption, error)
	// ResolveFields returns each requested field name mapped to its resolved
	// value plus a Secret flag indicating whether the source was a CONCEALED
	// 1P field. Called once per selected item so env + args resolution share
	// a single item fetch. Caller must pass a de-duplicated slice.
	ResolveFields(itemID string, fieldNames []string, opAccount string) (map[string]ResolvedField, error)
}

// ProfileLookup resolves a profile name to its config. The agent uses this
// so it doesn't need to re-read files on every request.
type ProfileLookup interface {
	Profile(name string) (*ProfileConfig, error)
}

// Server is the opbroker agent.
type Server struct {
	socketPath     string
	pidFile        string
	allowedCallers []string
	cache          *Cache
	fetcher        Fetcher
	profiles       ProfileLookup
	logger         *log.Logger

	startedAt time.Time
	stopCh    chan struct{}
	stopped   atomic.Bool
	wg        sync.WaitGroup
	listener  net.Listener
}

// Config configures a Server.
type Config struct {
	SocketPath     string
	PIDFile        string
	AllowedCallers []string
	TTL            time.Duration
	Fetcher        Fetcher
	Profiles       ProfileLookup
	Logger         *log.Logger
}

// NewServer builds a Server. The socket is not created until Start.
func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "opbroker-agent: ", log.LstdFlags)
	}
	return &Server{
		socketPath:     cfg.SocketPath,
		pidFile:        cfg.PIDFile,
		allowedCallers: cfg.AllowedCallers,
		cache:          NewCache(cfg.TTL),
		fetcher:        cfg.Fetcher,
		profiles:       cfg.Profiles,
		logger:         logger,
		stopCh:         make(chan struct{}),
	}
}

// Start begins accepting connections. It blocks until Stop is called or the
// context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if err := s.prepare(); err != nil {
		return err
	}
	s.startedAt = time.Now()

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = l

	if err := s.writePIDFile(); err != nil {
		_ = l.Close()
		return err
	}

	s.logger.Printf("listening on %s (pid=%d, ttl=%s)", s.socketPath, os.Getpid(), s.cache.TTL())

	// Ctx cancellation → close listener.
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.stopCh:
		}
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if s.stopped.Load() {
				break
			}
			if errors.Is(err, net.ErrClosed) {
				break
			}
			s.logger.Printf("accept: %v", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}

	s.wg.Wait()
	s.cleanup()
	return nil
}

// Stop signals the accept loop to exit and closes the listener.
func (s *Server) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}
	close(s.stopCh)
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *Server) prepare() error {
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	// If a stale socket or PID file exists (from a previous crash, a reboot,
	// or a SIGKILL), verify no live agent owns them and clean up.
	socketPresent := statExists(s.socketPath)
	pidPresent := statExists(s.pidFile)
	if socketPresent || pidPresent {
		if err := s.checkStale(); err != nil {
			return err
		}
		if socketPresent {
			if err := os.Remove(s.socketPath); err != nil {
				return fmt.Errorf("remove stale socket: %w", err)
			}
		}
		if pidPresent {
			if err := os.Remove(s.pidFile); err != nil {
				return fmt.Errorf("remove stale pid file: %w", err)
			}
		}
	}
	return nil
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkStale returns an error if an agent process is already running. It uses
// signal 0 as the standard "process exists" probe: kill(pid, 0) returns nil
// if the process exists and we can signal it, ESRCH if it's dead, EPERM if
// it exists but we don't own it.
func (s *Server) checkStale() error {
	data, err := os.ReadFile(s.pidFile)
	if err != nil {
		// No PID file — treat socket as stale.
		return nil
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return nil
	}
	switch err := syscall.Kill(pid, 0); err {
	case nil:
		return fmt.Errorf("agent already running (pid=%d)", pid)
	case syscall.EPERM:
		// Process exists but is owned by another user — still not stale.
		return fmt.Errorf("agent already running (pid=%d, owned by another user)", pid)
	default:
		// ESRCH or anything else → treat as stale.
		return nil
	}
}

func (s *Server) writePIDFile() error {
	return os.WriteFile(s.pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
}

func (s *Server) cleanup() {
	_ = os.Remove(s.socketPath)
	_ = os.Remove(s.pidFile)
	s.logger.Printf("stopped")
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	// Verify caller.
	if _, err := security.VerifyPeer(conn, s.allowedCallers); err != nil {
		s.logger.Printf("rejected caller: %v", err)
		_ = WriteMessage(conn, Response{Type: TypeError, Error: "unauthorized caller"})
		return
	}

	var req Request
	if err := ReadMessage(conn, &req); err != nil {
		s.logger.Printf("read request: %v", err)
		return
	}

	resp := s.dispatch(&req)
	if err := WriteMessage(conn, resp); err != nil {
		s.logger.Printf("write response: %v", err)
	}

	if req.Type == TypeStop && resp.Type == TypeOK {
		go s.Stop()
	}
}

func (s *Server) dispatch(req *Request) Response {
	switch req.Type {
	case TypeGet, TypeSelect:
		return s.handleGet(req)
	case TypeRefresh:
		n := s.cache.Clear()
		return Response{Type: TypeOK, Meta: map[string]string{"cleared": fmt.Sprintf("%d", n)}}
	case TypeStop:
		return Response{Type: TypeOK}
	case TypeStatus:
		acc, resolved, sel := s.cache.Counts()
		return Response{
			Type: TypeOK,
			Status: &StatusInfo{
				PID:              os.Getpid(),
				Uptime:           time.Since(s.startedAt),
				AccountListCount: acc,
				ResolvedCount:    resolved,
				SelectionCount:   sel,
				TTL:              s.cache.TTL(),
				SocketPath:       s.socketPath,
			},
		}
	default:
		return Response{Type: TypeError, Error: fmt.Sprintf("unknown request type %q", req.Type)}
	}
}

// handleGet resolves credentials + args for a profile, returning either OK
// with populated maps, select_required with the account options, or an error.
func (s *Server) handleGet(req *Request) Response {
	if s.fetcher == nil || s.profiles == nil {
		return Response{Type: TypeError, Error: "agent not ready: no fetcher/profiles configured"}
	}
	prof, err := s.resolveProfile(req)
	if err != nil {
		return Response{Type: TypeError, Error: err.Error()}
	}

	// If a select response already specified the account, treat this as a
	// direct fetch; otherwise consult selection memory.
	account := req.Account
	if account == "" {
		if last, ok := s.cache.LastSelection(req.Profile); ok {
			account = last
		}
	}

	if account != "" {
		result, err := s.resolveByAccount(prof, account)
		if err != nil {
			return Response{Type: TypeError, Error: err.Error()}
		}
		s.cache.RememberSelection(req.Profile, account)
		return okResponse(result)
	}

	// No account known — return the list (from cache or fetched).
	opts, ok := s.cache.GetAccounts(prof.Tag)
	if !ok {
		fetched, err := s.fetcher.ListAccounts(prof.Tag, prof.AccountField, prof.OpAccount)
		if err != nil {
			return Response{Type: TypeError, Error: fmt.Sprintf("list accounts: %v", err)}
		}
		s.cache.SetAccounts(prof.Tag, fetched)
		opts = fetched
	}

	switch len(opts) {
	case 0:
		return Response{Type: TypeError, Error: fmt.Sprintf("no items found for tag %q", prof.Tag)}
	case 1:
		result, err := s.resolveForItem(prof, opts[0])
		if err != nil {
			return Response{Type: TypeError, Error: err.Error()}
		}
		s.cache.RememberSelection(req.Profile, opts[0].Account)
		return okResponse(result)
	default:
		return Response{Type: TypeSelectRequired, Options: opts}
	}
}

// okResponse packages a ResolvedResult into a TypeOK Response.
func okResponse(r *ResolvedResult) Response {
	return Response{Type: TypeOK, Env: r.Env, Args: r.Args, Secrets: r.Secrets, Account: r.Account}
}

func (s *Server) resolveProfile(req *Request) (*ProfileConfig, error) {
	if req.Config != nil {
		return req.Config, nil
	}
	if req.Profile == "" {
		return nil, errors.New("no profile or inline config specified")
	}
	return s.profiles.Profile(req.Profile)
}

// resolveByAccount finds the AccountOption for a given account name (using
// the cached account list, refetching if missing) and returns the resolved
// result for that item.
func (s *Server) resolveByAccount(prof *ProfileConfig, account string) (*ResolvedResult, error) {
	if r, ok := s.cache.GetResolved(prof.Tag, account); ok {
		return r, nil
	}
	opts, ok := s.cache.GetAccounts(prof.Tag)
	if !ok {
		fetched, err := s.fetcher.ListAccounts(prof.Tag, prof.AccountField, prof.OpAccount)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		s.cache.SetAccounts(prof.Tag, fetched)
		opts = fetched
	}
	for _, o := range opts {
		if o.Account == account {
			return s.resolveForItem(prof, o)
		}
	}
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = o.Account
	}
	return nil, fmt.Errorf("account %q not found (available: %s)", account, strings.Join(names, ", "))
}

// resolveForItem fetches all fields referenced by prof.Env and prof.Args in a
// single item fetch, assembles the env + args maps, and caches the result.
// Template values (${account}, ${title}) are resolved from opt.Account /
// opt.Title without touching op. Secrets metadata (CONCEALED sources) is
// carried through so the client can mask values in debug output.
func (s *Server) resolveForItem(prof *ProfileConfig, opt AccountOption) (*ResolvedResult, error) {
	if r, ok := s.cache.GetResolved(prof.Tag, opt.Account); ok {
		return r, nil
	}
	needed := collectFieldNames(prof.Env, prof.Args)
	fields, err := s.fetcher.ResolveFields(opt.ItemID, needed, prof.OpAccount)
	if err != nil {
		return nil, fmt.Errorf("resolve fields: %w", err)
	}

	envValues, envSecrets := assembleEnv(prof.Env, fields)
	argValues, argSecrets := assembleArgs(prof.Args, fields, opt.Account, opt.Title)
	secrets := mergeSecrets(envSecrets, argSecrets)

	result := &ResolvedResult{
		Env:     envValues,
		Args:    argValues,
		Secrets: secrets,
		Account: opt.Account,
	}
	s.cache.SetResolved(prof.Tag, opt.Account, result)
	return result, nil
}

// mergeSecrets combines env-side and arg-side secret sets into one. Env var
// names and arg flag names live in different namespaces (env is uppercase,
// args start with '-'), so key collisions are effectively impossible.
func mergeSecrets(a, b map[string]bool) map[string]bool {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]bool, len(a)+len(b))
	for k, v := range a {
		if v {
			out[k] = true
		}
	}
	for k, v := range b {
		if v {
			out[k] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectFieldNames returns the union of field names referenced by env and
// args, minus the reserved templates. Sorted so callers see deterministic
// output.
func collectFieldNames(env, args map[string]string) []string {
	set := map[string]struct{}{}
	for _, v := range env {
		set[v] = struct{}{}
	}
	for _, v := range args {
		if isArgTemplate(v) {
			continue
		}
		set[v] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assembleEnv projects the profile's env map onto resolved field values,
// returning the value map and a set of env var names whose source was
// CONCEALED in 1Password.
func assembleEnv(env map[string]string, resolved map[string]ResolvedField) (map[string]string, map[string]bool) {
	if len(env) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(env))
	secrets := map[string]bool{}
	for envName, fieldName := range env {
		rf := resolved[fieldName]
		values[envName] = rf.Value
		if rf.Secret {
			secrets[envName] = true
		}
	}
	if len(secrets) == 0 {
		secrets = nil
	}
	return values, secrets
}

// assembleArgs projects the profile's args map to resolved values, expanding
// ${account} and ${title} templates from the currently-selected item.
// Templates are never marked as secrets; only non-template values sourced
// from CONCEALED 1P fields flip their flag on in the returned secrets map.
func assembleArgs(args map[string]string, resolved map[string]ResolvedField, account, title string) (map[string]string, map[string]bool) {
	if len(args) == 0 {
		return nil, nil
	}
	values := make(map[string]string, len(args))
	secrets := map[string]bool{}
	for flag, source := range args {
		switch source {
		case argTemplateAccount:
			values[flag] = account
		case argTemplateTitle:
			values[flag] = title
		default:
			rf := resolved[source]
			values[flag] = rf.Value
			if rf.Secret {
				secrets[flag] = true
			}
		}
	}
	if len(secrets) == 0 {
		secrets = nil
	}
	return values, secrets
}

// Reserved template values in args map. Duplicated here (rather than imported
// from config) to keep the agent package self-contained.
const (
	argTemplateAccount = "${account}"
	argTemplateTitle   = "${title}"
)

func isArgTemplate(v string) bool {
	return v == argTemplateAccount || v == argTemplateTitle
}
