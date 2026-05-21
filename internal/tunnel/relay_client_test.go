//go:build !integration

package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewRelayClient(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com/", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if rc.relayURL != "wss://relay.example.com" {
		t.Errorf("relayURL = %q, want trailing slash trimmed", rc.relayURL)
	}
	if rc.token != "0123456789abcdef0123456789abcdef" {
		t.Errorf("token mismatch")
	}
	if rc.crypto == nil {
		t.Error("crypto should be initialized")
	}
	if rc.sendCh == nil {
		t.Error("sendCh should be initialized")
	}
	if rc.stopCh == nil {
		t.Error("stopCh should be initialized")
	}
}

func TestRelayClientConnectURL(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	url := rc.ConnectURL()
	if !strings.Contains(url, "role=client") {
		t.Error("ConnectURL should contain role=client")
	}
	if !strings.Contains(url, "token=0123456789abcdef01234567") {
		t.Error("ConnectURL should contain token")
	}
	if !strings.HasPrefix(url, "wss://") {
		t.Error("ConnectURL should start with wss://")
	}
}

func TestRelayClientToken(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if rc.Token() != "0123456789abcdef01234567" {
		t.Errorf("Token() = %q, want %q", rc.Token(), "0123456789abcdef01234567")
	}
}

func TestRelayClientOnMessage(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	var received GatewayMessage
	rc.OnMessage(func(msg GatewayMessage) {
		received = msg
	})
	rc.mu.RLock()
	fn := rc.onMessage
	rc.mu.RUnlock()
	if fn == nil {
		t.Fatal("onMessage should be set")
	}
	fn(GatewayMessage{Type: "test", EventID: "ev-1"})
	if received.Type != "test" {
		t.Error("callback should have been called")
	}
}

func TestRelayClientClose(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if !rc.closed {
		t.Error("closed should be true after Close()")
	}
}

func TestRelayClientSendAfterClose(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	err = rc.Send(GatewayMessage{Type: "test"})
	if err == nil {
		t.Error("Send after Close should return error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error should mention closed: %v", err)
	}
}

func TestRelayClientSendSuccess(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	msg := GatewayMessage{
		Type:      "message",
		SessionID: "sess-1",
		EventID:   "ev-1",
		StreamID:  "msg-1",
		Data:      json.RawMessage(`{"text":"hi"}`),
	}
	err = rc.Send(msg)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-rc.sendCh:
		var parsed map[string]string
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["type"] != "encrypted" {
			t.Errorf("relay message type = %q, want %q", parsed["type"], "encrypted")
		}
		if parsed["session_id"] != "sess-1" || parsed["event_id"] != "ev-1" || parsed["stream_id"] != "msg-1" {
			t.Fatalf("expected envelope metadata, got %+v", parsed)
		}
		if parsed["nonce"] == "" {
			t.Error("nonce should not be empty")
		}
		if parsed["ciphertext"] == "" {
			t.Error("ciphertext should not be empty")
		}
	case <-time.After(time.Second):
		t.Error("timed out waiting for message on send channel")
	}
}

func TestRelayClientSendEncryptsPayload(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	msg := GatewayMessage{Type: "test", SessionID: "sess-1", EventID: "ev-42"}
	err = rc.Send(msg)
	if err != nil {
		t.Fatal(err)
	}

	data := <-rc.sendCh
	var relayMsg struct {
		Type       string `json:"type"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	json.Unmarshal(data, &relayMsg)

	plain, err := rc.crypto.Decrypt(relayMsg.Nonce, relayMsg.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var got GatewayMessage
	json.Unmarshal(plain, &got)
	if got.Type != "test" || got.SessionID != "sess-1" || got.EventID != "ev-42" {
		t.Errorf("decrypted message mismatch: %+v", got)
	}
}

func TestRelayClientSendChannelFull(t *testing.T) {
	rc, err := NewRelayClient("wss://relay.example.com", "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	// Fill the send channel (capacity 256)
	for i := 0; i < 256; i++ {
		rc.sendCh <- []byte("filler")
	}

	// Next send should get "send channel full" error
	err = rc.Send(GatewayMessage{Type: "test"})
	if err == nil {
		t.Error("expected error when channel is full")
	}
	if !strings.Contains(err.Error(), "full") {
		t.Errorf("error should mention full: %v", err)
	}
}
