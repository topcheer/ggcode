//go:build !integration

package tunnel

import (
	"testing"
)

func TestNewSession(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	if sess == nil {
		t.Fatal("session should not be nil")
	}
	if sess.relayURL != "wss://relay.example.com" {
		t.Errorf("relayURL = %q, want %q", sess.relayURL, "wss://relay.example.com")
	}
}

func TestNewSessionTrailingSlash(t *testing.T) {
	sess := NewSession("wss://relay.example.com/")
	if sess.relayURL != "wss://relay.example.com" {
		t.Errorf("trailing slash should be trimmed: got %q", sess.relayURL)
	}
}

func TestSessionInfoNilBeforeStart(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	info := sess.Info()
	if info != nil {
		t.Error("Info() should be nil before Start()")
	}
}

func TestSessionOnMessage(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	var called bool
	sess.OnMessage(func(msg GatewayMessage) {
		called = true
	})
	if sess.onMsg == nil {
		t.Error("onMsg should be set")
	}
	_ = called
}

func TestSessionOnConnect(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	sess.OnConnect(func() {})
	if sess.onConn == nil {
		t.Error("onConn should be set")
	}
}

func TestSessionStopNilClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	// Should not panic when client is nil
	sess.Stop()
}

func TestSessionStopWithClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	sess.client = rc
	sess.Stop()
	if !rc.closed {
		t.Error("client should be closed after session Stop")
	}
}

func TestSessionInfoRWMutex(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	// Set info directly
	sess.mu.Lock()
	sess.info = &SessionInfo{Token: "test"}
	sess.mu.Unlock()

	info := sess.Info()
	if info == nil || info.Token != "test" {
		t.Error("Info() should return the set info")
	}
}

func TestDefaultRelayURL(t *testing.T) {
	if DefaultRelayURL != "wss://gateway.ggcode.dev" {
		t.Errorf("DefaultRelayURL = %q, want %q", DefaultRelayURL, "wss://gateway.ggcode.dev")
	}
}

func TestSessionInfoFields(t *testing.T) {
	info := &SessionInfo{
		ConnectURL: "wss://relay.example.com/ws?role=client&token=abc",
		Token:      "abc",
		QRCode:     "qr-string",
		QRCodePNG:  []byte("png-data"),
		QRLines:    []string{"line1", "line2"},
	}
	if info.ConnectURL == "" || info.Token == "" {
		t.Error("fields should be set")
	}
	if len(info.QRLines) != 2 {
		t.Errorf("QRLines len = %d, want 2", len(info.QRLines))
	}
}
