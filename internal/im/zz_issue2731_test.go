package im

import (
	"strings"
	"testing"
)

func TestIssue2731ClosedAdapterRefusesNewRelay(t *testing.T) {
	a := &nostrAdapter{name: "probe"}
	a.Close()

	err := a.connectRelay(t.Context(), "wss://relay.invalid.invalid")
	if err == nil {
		t.Fatalf("closed adapter accepted a new relay")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("error should name the closed state, got: %v", err)
	}
	a.mu.Lock()
	conns := len(a.relayConns)
	connected := a.connected
	a.mu.Unlock()
	if conns != 0 || connected != 0 {
		t.Fatalf("closed adapter recorded state: conns=%d connected=%d", conns, connected)
	}
}
