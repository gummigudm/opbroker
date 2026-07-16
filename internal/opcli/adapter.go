package opcli

import (
	"fmt"

	"github.com/gummigudm/opbroker/internal/agent"
	"github.com/gummigudm/opbroker/internal/config"
)

// Adapter satisfies agent.Fetcher using an opcli.Client.
type Adapter struct {
	Client *Client
}

// NewAdapter constructs an Adapter around c.
func NewAdapter(c *Client) *Adapter { return &Adapter{Client: c} }

// ListAccounts fetches items with the given tag, then for each item reads the
// account_field to build an AccountOption list.
//
// If accountField equals config.AccountFieldTitle ("title"), the item's title
// is used as the account identifier and no field lookup is performed. Any
// other value is looked up as a field id or label on the item; missing fields
// are a hard error so profile misconfiguration is surfaced immediately.
func (a *Adapter) ListAccounts(tag, accountField, opAccount string) ([]agent.AccountOption, error) {
	items, err := a.Client.ListItemsByTag(tag, opAccount)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	out := make([]agent.AccountOption, 0, len(items))
	for _, it := range items {
		// `op item list` returns items without fields — fetch details.
		detail, err := a.Client.GetItem(it.ID, opAccount)
		if err != nil {
			return nil, fmt.Errorf("get item %s: %w", it.ID, err)
		}
		account, err := extractAccountName(detail, accountField)
		if err != nil {
			return nil, err
		}
		out = append(out, agent.AccountOption{
			Account: account,
			Title:   detail.Title,
			ItemID:  detail.ID,
		})
	}
	return out, nil
}

// extractAccountName pulls the account identifier off an item, honoring the
// reserved "title" sentinel.
func extractAccountName(item *Item, accountField string) (string, error) {
	if accountField == config.AccountFieldTitle {
		return item.Title, nil
	}
	v, ok := item.FieldValue(accountField)
	if !ok {
		return "", fmt.Errorf(
			"item %q has no field %q — set `account_field: title` in the profile if the item title identifies the account",
			item.Title, accountField,
		)
	}
	return v, nil
}

// ResolveFields fetches an item and returns each requested field name mapped
// to its resolved value plus a Secret flag (true if the source 1P field was
// CONCEALED). Fields with an embedded value on the item are returned directly;
// fields with only a reference are dereferenced via `op read`. Called once per
// selected item so env + args resolution share a single `op item get` call.
func (a *Adapter) ResolveFields(itemID string, fieldNames []string, opAccount string) (map[string]agent.ResolvedField, error) {
	item, err := a.Client.GetItem(itemID, opAccount)
	if err != nil {
		return nil, fmt.Errorf("get item %s: %w", itemID, err)
	}
	out := make(map[string]agent.ResolvedField, len(fieldNames))
	for _, name := range fieldNames {
		v, secret, err := resolveFieldValue(a.Client, item, name, opAccount)
		if err != nil {
			return nil, err
		}
		out[name] = agent.ResolvedField{Value: v, Secret: secret}
	}
	return out, nil
}

// resolveFieldValue returns the plaintext value for fieldName on item and a
// flag indicating whether the source field was CONCEALED. It prefers the
// embedded `value` on the field record; if the field only has a reference,
// it fetches via `op read`. Returns an error if the item has no such field
// at all.
func resolveFieldValue(client *Client, item *Item, fieldName, opAccount string) (string, bool, error) {
	// Locate the field so we can report its type (CONCEALED vs plain).
	var field *Field
	for i := range item.Fields {
		f := &item.Fields[i]
		if f.ID == fieldName || f.Label == fieldName {
			field = f
			break
		}
	}
	if field == nil {
		return "", false, fmt.Errorf("item %q has no field %q", item.Title, fieldName)
	}
	secret := field.IsSecret()
	// Prefer already-resolved value on the item.
	if field.Value != "" {
		return field.Value, secret, nil
	}
	if field.Ref == "" {
		// No value and no reference — return empty; caller decides whether
		// that's an error.
		return "", secret, nil
	}
	v, err := client.Read(field.Ref, opAccount)
	if err != nil {
		return "", secret, fmt.Errorf("read %s: %w", field.Ref, err)
	}
	return v, secret, nil
}
