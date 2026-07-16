package agent

import (
	"bytes"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	req := Request{Type: TypeGet, Profile: "foo", Account: "acct1"}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, req); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	var got Request
	if err := ReadMessage(&buf, &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got != req {
		t.Errorf("got %+v, want %+v", got, req)
	}
}

func TestReadTruncated(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0}) // partial header
	var got Request
	if err := ReadMessage(&buf, &got); err == nil {
		t.Fatal("expected error on truncated read")
	}
}

func TestReadOversized(t *testing.T) {
	var buf bytes.Buffer
	// Write header claiming 2MiB — should be rejected.
	buf.Write([]byte{0x00, 0x20, 0x00, 0x00})
	var got Request
	if err := ReadMessage(&buf, &got); err == nil {
		t.Fatal("expected error on oversized message")
	}
}
