package opcli

import (
	"strings"
	"testing"
)

func TestExtractAccountName_TitleSentinel(t *testing.T) {
	item := &Item{
		ID:    "id1",
		Title: "AWS Prod Account",
		Fields: []Field{
			{ID: "username", Label: "username", Value: "root"},
		},
	}
	got, err := extractAccountName(item, "title")
	if err != nil {
		t.Fatalf("extractAccountName: %v", err)
	}
	if got != "AWS Prod Account" {
		t.Errorf("got %q, want %q", got, "AWS Prod Account")
	}
}

func TestExtractAccountName_FieldLookup(t *testing.T) {
	item := &Item{
		ID:    "id1",
		Title: "Some Title",
		Fields: []Field{
			{ID: "abc", Label: "account", Value: "account1"},
		},
	}
	got, err := extractAccountName(item, "account")
	if err != nil {
		t.Fatalf("extractAccountName: %v", err)
	}
	if got != "account1" {
		t.Errorf("got %q, want account1", got)
	}
}

func TestExtractAccountName_MissingFieldErrors(t *testing.T) {
	item := &Item{Title: "Some Item", Fields: nil}
	_, err := extractAccountName(item, "not_a_field")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "account_field: title") {
		t.Errorf("error should hint at `account_field: title`, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Some Item") {
		t.Errorf("error should name the item, got: %v", err)
	}
}
