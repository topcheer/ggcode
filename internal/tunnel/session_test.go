//go:build !integration

package tunnel

import (
	"encoding/json"
	"testing"
	"time"
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

func TestSessionOnConnected(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	var called bool
	sess.OnConnected(func(info RelayConnectedState) {
		called = info.Role == "server"
	})
	if sess.onConn == nil {
		t.Error("onConn should be set")
	}
	_ = called
}

func TestSessionStopNilClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	// Should not panic when client is nil
	sess.Stop()
}

func TestSessionStopWithClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	rc := testRelayClient(t, "wss://relay.example.com")
	sess.client = rc
	sess.Stop()
	if !rc.closed {
		t.Error("client should be closed after session Stop")
	}
}

func TestSessionStopGracefullyWithClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	rc := testRelayClient(t, "wss://relay.example.com")
	sess.client = rc
	sess.StopGracefully(50 * time.Millisecond)
	if !rc.closed {
		t.Error("client should be closed after session StopGracefully")
	}
}

func TestSessionDestroyGracefullyWithClient(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	rc := testRelayClient(t, "wss://relay.example.com")
	sess.client = rc
	sess.DestroyGracefully(50 * time.Millisecond)
	if !rc.closed {
		t.Error("client should be closed after session DestroyGracefully")
	}
	select {
	case raw := <-rc.sendCh:
		var msg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "stop_sharing" {
			t.Fatalf("expected stop_sharing, got %q", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stop_sharing")
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
