//go:build !integration

package tunnel

import (
	"encoding/json"
	"strings"
	"sync"
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

func TestRelayClientSendEncryptsMessage(t *testing.T) {
	rc, _ := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	defer rc.Close()

	msg := GatewayMessage{
		Type: EventText,
		Data: json.RawMessage(`{"id":"1","chunk":"hello"}`),
	}
	if err := rc.Send(msg); err != nil {
		t.Fatal(err)
	}

	// Drain the send channel and verify it's a valid encrypted relay message.
	select {
	case raw := <-rc.sendCh:
		var relayMsg map[string]interface{}
		if err := json.Unmarshal(raw, &relayMsg); err != nil {
			t.Fatal(err)
		}
		if relayMsg["type"] != "encrypted" {
			t.Errorf("expected type=encrypted, got %v", relayMsg["type"])
		}
		if _, ok := relayMsg["nonce"]; !ok {
			t.Error("missing nonce field")
		}
		if _, ok := relayMsg["ciphertext"]; !ok {
			t.Error("missing ciphertext field")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for encrypted message on sendCh")
	}
}

func TestRelayClientSendMessageID(t *testing.T) {
	rc, _ := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	defer rc.Close()

	msg := GatewayMessage{
		Type:      CmdMessage,
		MessageID: "msg-test-789",
		Data:      json.RawMessage(`{"text":"hello"}`),
	}
	if err := rc.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-rc.sendCh:
		var relayMsg map[string]interface{}
		if err := json.Unmarshal(raw, &relayMsg); err != nil {
			t.Fatal(err)
		}
		if relayMsg["message_id"] != "msg-test-789" {
			t.Errorf("expected message_id=msg-test-789, got %v", relayMsg["message_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestRelayClientOnAck(t *testing.T) {
	rc, _ := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	defer rc.Close()

	var acks []struct {
		ackType   string
		messageID string
	}
	ackMu := sync.Mutex{}
	rc.OnAck(func(ackType, messageID string) {
		ackMu.Lock()
		acks = append(acks, struct {
			ackType   string
			messageID string
		}{ackType, messageID})
		ackMu.Unlock()
	})

	// Simulate relay_ack by directly invoking the onAck callback
	// (same path as readPump's relay_ack case).
	rc.mu.RLock()
	fn := rc.onAck
	rc.mu.RUnlock()
	if fn == nil {
		t.Fatal("onAck should be set")
	}
	fn("relay_ack", "msg-456")

	ackMu.Lock()
	defer ackMu.Unlock()
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	if acks[0].ackType != "relay_ack" || acks[0].messageID != "msg-456" {
		t.Errorf("expected relay_ack/msg-456, got %s/%s", acks[0].ackType, acks[0].messageID)
	}
}

func TestRelayClientClosedSend(t *testing.T) {
	rc, _ := NewRelayClient("wss://test.local", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4")
	rc.Close()

	msg := GatewayMessage{Type: "test"}
	if err := rc.Send(msg); err == nil {
		t.Error("expected error sending on closed client")
	}
}

func TestRelayReconnectDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 5 * time.Second},
		{attempt: 2, want: 10 * time.Second},
		{attempt: 3, want: 20 * time.Second},
		{attempt: 4, want: 40 * time.Second},
		{attempt: 8, want: 40 * time.Second},
	}

	for _, tt := range tests {
		if got := relayReconnectDelay(tt.attempt); got != tt.want {
			t.Fatalf("relayReconnectDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}
