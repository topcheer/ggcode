package im

import (
	"strings"
	"testing"
)

// r202 pin: behavior-preserving decomposition of nostrAdapter.connectRelay
// into relayProxyHost / dialAndRegisterRelay / subscribeDMs /
// serveRelayEvents. These tests lock the proxy-seam contract and the gate
// order (proxy resolution happens BEFORE the closed-adapter gate and the
// dial), so any drift in error strings or gate sequencing fails here.

func TestNostrRelayProxyHostNoProxy(t *testing.T) {
	a := &nostrAdapter{name: "pin", proxy: ""}
	host, err := a.relayProxyHost("wss://relay.r202-pin.invalid")
	if err != nil {
		t.Fatalf("no proxy must not error, got: %v", err)
	}
	if host != "" {
		t.Fatalf("no proxy must return empty host, got %q", host)
	}
}

func TestNostrRelayProxyHostInvalidURL(t *testing.T) {
	a := &nostrAdapter{name: "pin", proxy: "http://127.0.0.1:1"}
	_, err := a.relayProxyHost("://missing-scheme")
	if err == nil {
		t.Fatalf("invalid relay URL must error")
	}
	if !strings.Contains(err.Error(), "invalid relay URL") {
		t.Fatalf("error must name the invalid URL, got: %v", err)
	}
	if !strings.Contains(err.Error(), "connect ://missing-scheme:") {
		t.Fatalf("error must keep the connect prefix, got: %v", err)
	}
}

func TestNostrRelayProxyHostRegistersHost(t *testing.T) {
	a := &nostrAdapter{name: "pin", proxy: "http://127.0.0.1:1"}
	const url = "wss://relay.r202-pin.invalid"
	host, err := a.relayProxyHost(url)
	if err != nil {
		t.Fatalf("valid relay URL must not error, got: %v", err)
	}
	if host != "relay.r202-pin.invalid" {
		t.Fatalf("must return the relay host, got %q", host)
	}
	defer UnregisterHostProxy(host) // keep the global registry clean
}

// connectRelay gate order: the proxy seam runs first, so an invalid relay
// URL errors out before the closed-adapter gate and before any dial.
func TestNostrConnectRelayInvalidURLBeforeDial(t *testing.T) {
	a := &nostrAdapter{name: "pin", proxy: "http://127.0.0.1:1"}
	err := a.connectRelay(t.Context(), "://missing-scheme")
	if err == nil {
		t.Fatalf("invalid relay URL must fail connectRelay")
	}
	if !strings.Contains(err.Error(), "invalid relay URL") {
		t.Fatalf("error must name the invalid URL, got: %v", err)
	}
	a.mu.Lock()
	conns, connected := len(a.relayConns), a.connected
	a.mu.Unlock()
	if conns != 0 || connected != 0 {
		t.Fatalf("failed connect must not mutate adapter state: conns=%d connected=%d", conns, connected)
	}
}
