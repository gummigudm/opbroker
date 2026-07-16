// Package opcli wraps the 1Password `op` CLI.
package opcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// Client shells out to the `op` binary. If Binary is empty, "op" on PATH is used.
type Client struct {
	Binary string
}

// New returns a Client using the given `op` binary path (or "op" if empty).
func New(binary string) *Client {
	if binary == "" {
		binary = "op"
	}
	return &Client{Binary: binary}
}

// Item is the subset of `op item` JSON we care about.
type Item struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Vault struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"vault"`
	Fields []Field `json:"fields"`
}

// OpAccount is one entry from `op account list`.
type OpAccount struct {
	URL         string `json:"url"`
	Email       string `json:"email"`
	UserUUID    string `json:"user_uuid"`
	AccountUUID string `json:"account_uuid"`
}

// ListAccounts returns the 1Password accounts configured on this machine.
func (c *Client) ListAccounts() ([]OpAccount, error) {
	out, err := c.exec([]string{"account", "list", "--format=json"})
	if err != nil {
		return nil, err
	}
	var accounts []OpAccount
	if err := json.Unmarshal(out, &accounts); err != nil {
		return nil, fmt.Errorf("parse account list: %w", err)
	}
	return accounts, nil
}

// Field is a single field on an item.
type Field struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"` // "STRING", "CONCEALED", etc. per `op` schema
	Value string `json:"value,omitempty"`
	Ref   string `json:"reference,omitempty"`
}

// IsSecret reports whether the field is a masked/CONCEALED value in 1Password.
func (f Field) IsSecret() bool { return f.Type == "CONCEALED" }

// ListItemsByTag returns all items carrying the given tag.
// If opAccount is non-empty, --account is passed to `op`.
func (c *Client) ListItemsByTag(tag, opAccount string) ([]Item, error) {
	args := []string{"item", "list", "--tags", tag, "--format=json"}
	if opAccount != "" {
		args = append(args, "--account", opAccount)
	}
	out, err := c.exec(args)
	if err != nil {
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse item list: %w", err)
	}
	return items, nil
}

// GetItem returns the full item (including fields) by ID.
func (c *Client) GetItem(itemID, opAccount string) (*Item, error) {
	args := []string{"item", "get", itemID, "--format=json"}
	if opAccount != "" {
		args = append(args, "--account", opAccount)
	}
	out, err := c.exec(args)
	if err != nil {
		return nil, err
	}
	var it Item
	if err := json.Unmarshal(out, &it); err != nil {
		return nil, fmt.Errorf("parse item get: %w", err)
	}
	return &it, nil
}

// FieldValue returns the value of a field with the given label or id.
func (it *Item) FieldValue(nameOrLabel string) (string, bool) {
	for _, f := range it.Fields {
		if f.ID == nameOrLabel || f.Label == nameOrLabel {
			return f.Value, true
		}
	}
	return "", false
}

// Read resolves an op:// reference to its plaintext value.
func (c *Client) Read(ref, opAccount string) (string, error) {
	args := []string{"read", ref}
	if opAccount != "" {
		args = append(args, "--account", opAccount)
	}
	out, err := c.exec(args)
	if err != nil {
		return "", err
	}
	// `op read` outputs the value with a trailing newline.
	return string(bytes.TrimRight(out, "\r\n")), nil
}

// exec runs `op <args>` and returns stdout. Non-zero exit codes wrap stderr
// into the returned error.
func (c *Client) exec(args []string) ([]byte, error) {
	cmd := exec.Command(c.Binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 {
			return nil, fmt.Errorf("op %v: %w", args, err)
		}
		return nil, fmt.Errorf("op %v: %s", args, msg)
	}
	return stdout.Bytes(), nil
}

// ErrNotFound is returned when a requested field isn't present on an item.
var ErrNotFound = errors.New("field not found")
