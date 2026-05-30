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
	_, err := NewRelayClient("wss://relay.example.com/", "0123456789abcdef0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "legacy relay clients are unsupported") {
		t.Fatalf("NewRelayClient() error = %v, want legacy unsupported error", err)
	}
}

func TestNewRelayClientWithDescriptorRejectsRemoteInsecureRelayURL(t *testing.T) {
	_, err := NewRelayClientWithDescriptor("ws://relay.example.com", testShareDescriptor(t), "server", RelayClientMetadata{})
	if err == nil || !strings.Contains(err.Error(), "insecure relay URL") {
		t.Fatalf("NewRelayClientWithDescriptor() error = %v, want insecure relay URL error", err)
	}
}

func TestRelayClientConnectURL(t *testing.T) {
	rc, err := NewRelayClientWithDescriptor("wss://relay.example.com", testShareDescriptor(t), "server", RelayClientMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	url := rc.ConnectURL()
	if !strings.Contains(url, "role=client") {
		t.Error("ConnectURL should contain role=client")
	}
	if !strings.Contains(url, "proto=3") || !strings.Contains(url, "room_id=") || !strings.Contains(url, "auth_ticket=") {
		t.Error("ConnectURL should contain v3 room parameters")
	}
	if !strings.HasPrefix(url, "wss://") {
		t.Error("ConnectURL should start with wss://")
	}
}

func TestRelayClientSendEncryptsMessage(t *testing.T) {
	rc := testRelayClient(t, "wss://test.local")
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
	rc := testRelayClient(t, "wss://test.local")
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

func TestRelayClientDestroyRoomSendsStopSharing(t *testing.T) {
	rc := testRelayClient(t, "wss://test.local")
	defer rc.Close()

	if err := rc.DestroyRoom(); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-rc.sendCh:
		var relayMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &relayMsg); err != nil {
			t.Fatal(err)
		}
		if relayMsg.Type != "stop_sharing" {
			t.Fatalf("expected stop_sharing, got %q", relayMsg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for control message on sendCh")
	}
}

func TestRelayClientOnAck(t *testing.T) {
	rc := testRelayClient(t, "wss://test.local")
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
	rc := testRelayClient(t, "wss://test.local")
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

func TestRelayClientHandleKeyOfferRespondsWithWrappedKey(t *testing.T) {
	serverPub, serverPriv, err := generateShareKeyExchangeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	clientPub, clientPriv, err := generateShareKeyExchangeKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := NewRelayClientWithDescriptor("wss://relay.example.com", ShareDescriptor{
		ProtocolVersion:  ShareProtocolV3,
		ShareMode:        ShareModeV3,
		RoomID:           "room-1",
		AuthTicket:       "server-auth",
		RenewToken:       "server-renew",
		CryptoKey:        "room-secret",
		ServerPublicKey:  serverPub,
		ServerPrivateKey: serverPriv,
	}, "server", RelayClientMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	payload := json.RawMessage(`{"client_public_key":"` + clientPub + `"}`)
	if err := rc.handleKeyOffer("client-1", payload); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-rc.sendCh:
		var relayMsg struct {
			Type     string         `json:"type"`
			ClientID string         `json:"client_id"`
			Data     relayKeyAccept `json:"data"`
		}
		if err := json.Unmarshal(raw, &relayMsg); err != nil {
			t.Fatal(err)
		}
		if relayMsg.Type != "key_accept" || relayMsg.ClientID != "client-1" {
			t.Fatalf("unexpected relay msg: %+v", relayMsg)
		}
		roomKey, err := unwrapShareRoomKey(relayMsg.Data.Nonce, relayMsg.Data.Ciphertext, "room-1", "client-1", clientPriv, serverPub)
		if err != nil {
			t.Fatal(err)
		}
		if roomKey != "room-secret" {
			t.Fatalf("wrapped room key = %q, want room-secret", roomKey)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for key_accept")
	}
}
