// Package agent implements the opbroker credential-caching agent.
//
// The agent listens on a Unix domain socket and serves credential fetch
// requests from the local opbroker binary. Requests are JSON messages with
// a 4-byte big-endian length prefix.
package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Request/response types.
const (
	TypeGet     = "get"
	TypeSelect  = "select"
	TypeRefresh = "refresh"
	TypeStop    = "stop"
	TypeStatus  = "status"

	TypeOK             = "ok"
	TypeSelectRequired = "select_required"
	TypeError          = "error"
)

// Request is a client-to-agent message.
type Request struct {
	Type    string         `json:"type"`
	Profile string         `json:"profile,omitempty"`
	Account string         `json:"account,omitempty"`
	Config  *ProfileConfig `json:"config,omitempty"`
}

// ProfileConfig is an inline profile description used when the client
// invokes with flag-only overrides. Mirrors config.Profile on the wire — the
// two shapes are kept in sync manually via cmd/opbroker/glue.go.
type ProfileConfig struct {
	Tag          string            `json:"tag"`
	AccountField string            `json:"account_field"`
	Env          map[string]string `json:"env,omitempty"`
	OpAccount    string            `json:"op_account,omitempty"`
	Args         map[string]string `json:"args,omitempty"`
	ArgStyle     string            `json:"arg_style,omitempty"`
	ArgPlacement string            `json:"arg_placement,omitempty"`
	AccountArg   string            `json:"account_arg,omitempty"`
}

// Response is an agent-to-client message.
type Response struct {
	Type    string            `json:"type"`
	Env     map[string]string `json:"env,omitempty"`     // credential env vars (name → value)
	Args    map[string]string `json:"args,omitempty"`    // resolved args (flag → value)
	Secrets map[string]bool   `json:"secrets,omitempty"` // keys from Env or Args whose source was a CONCEALED 1P field
	Account string            `json:"account,omitempty"` // resolved account name
	Options []AccountOption   `json:"options,omitempty"` // populated on select_required
	Meta    map[string]string `json:"meta,omitempty"`    // scalar responses (e.g. refresh: cleared count)
	Error   string            `json:"error,omitempty"`
	Status  *StatusInfo       `json:"status,omitempty"`
}

// ResolvedField is a single field value returned by Fetcher.ResolveFields.
// Secret is true if the source 1P field was CONCEALED (password/token/etc.);
// callers can use this to mask values in debug output without ever seeing the
// plaintext leak into a display path.
type ResolvedField struct {
	Value  string
	Secret bool
}

// AccountOption is a single account row in a select response.
type AccountOption struct {
	Account string `json:"account"`
	Title   string `json:"title"`
	ItemID  string `json:"item_id"`
}

// StatusInfo describes agent state.
type StatusInfo struct {
	PID              int           `json:"pid"`
	Uptime           time.Duration `json:"uptime"`
	AccountListCount int           `json:"account_list_count"`
	ResolvedCount    int           `json:"resolved_count"`
	SelectionCount   int           `json:"selection_count"`
	TTL              time.Duration `json:"ttl"`
	SocketPath       string        `json:"socket_path"`
}

// maxMessageSize caps a single message to guard against corrupt/malicious peers.
const maxMessageSize = 1 << 20 // 1 MiB

// WriteMessage encodes v as JSON with a 4-byte big-endian length prefix.
func WriteMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// ReadMessage reads a length-prefixed JSON message into v.
func ReadMessage(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return errors.New("empty message")
	}
	if n > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}
