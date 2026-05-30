//go:build !integration

package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSessionInfoReturnsClone(t *testing.T) {
	sess := NewSession("wss://relay.example.com")
	sess.mu.Lock()
	sess.info = &SessionInfo{
		Token:     "test",
		QRCodePNG: []byte("png"),
		QRLines:   []string{"one", "two"},
	}
	sess.mu.Unlock()

	info := sess.Info()
	info.Token = "changed"
	info.QRCodePNG[0] = 'x'
	info.QRLines[0] = "changed"

	again := sess.Info()
	if again.Token != "test" {
		t.Fatalf("Info() should return cloned token, got %q", again.Token)
	}
	if string(again.QRCodePNG) != "png" {
		t.Fatalf("Info() should clone QRCodePNG, got %q", string(again.QRCodePNG))
	}
	if again.QRLines[0] != "one" {
		t.Fatalf("Info() should clone QRLines, got %#v", again.QRLines)
	}
}

func TestSessionRefreshInvite(t *testing.T) {
	authExp := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	renewExp := authExp.Add(24 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != shareSessionRefreshPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, shareSessionRefreshPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(relayRefreshedShareSessionResponse{
			ProtocolVersion:  ShareProtocolV3,
			ShareMode:        ShareModeV3,
			RoomID:           "room-test",
			ClientAuthTicket: "client-auth-2",
			ServerRenewToken: "server-renew-2",
			AuthExpiresAt:    authExp.Format(time.RFC3339),
			RenewExpiresAt:   renewExp.Format(time.RFC3339),
			Notice:           "refreshed",
		})
	}))
	defer srv.Close()

	relayURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	sess := NewSession(relayURL)
	rc, err := NewRelayClientWithDescriptor(relayURL, testShareDescriptor(t), "server", RelayClientMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	sess.client = rc
	sess.mu.Lock()
	sess.info = &SessionInfo{
		ConnectURL: "stale",
		RoomID:     "room-test",
	}
	sess.mu.Unlock()

	info, err := sess.RefreshInvite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.RoomID != "room-test" || !strings.Contains(info.ConnectURL, "client-auth-2") {
		t.Fatalf("unexpected refreshed info: %+v", info)
	}
	if info.CompatibilityNotice != "refreshed" {
		t.Fatalf("unexpected refreshed notice: %+v", info)
	}
	if current := sess.Info(); current == nil || current.ConnectURL != info.ConnectURL {
		t.Fatalf("session info not updated: current=%+v refreshed=%+v", current, info)
	}
	if rc.currentShareDescriptor().RenewToken != "server-renew-2" {
		t.Fatalf("relay client renew token not updated: %+v", rc.currentShareDescriptor())
	}
}
