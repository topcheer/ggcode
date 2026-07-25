package tunnel

import (
	"encoding/json"
	"testing"
)

func TestEncodeDecodeSignalMessage(t *testing.T) {
	sig := SignalMessage{Type: "rtc_offer", SDP: `{"type":"offer","sdp":"v=0..."}`}

	gm, err := EncodeSignalMessage(sig)
	if err != nil {
		t.Fatalf("EncodeSignalMessage: %v", err)
	}
	if gm.Type != EventRTCOffer {
		t.Errorf("expected type %s, got %s", EventRTCOffer, gm.Type)
	}

	decoded, ok := DecodeSignalMessage(gm)
	if !ok {
		t.Fatal("DecodeSignalMessage returned false")
	}
	if decoded.Type != "rtc_offer" {
		t.Errorf("expected rtc_offer, got %s", decoded.Type)
	}
	if decoded.SDP != sig.SDP {
		t.Error("SDP mismatch")
	}
}

func TestDecodeSignalMessage_InvalidType(t *testing.T) {
	gm := GatewayMessage{Type: "text"}
	_, ok := DecodeSignalMessage(gm)
	if ok {
		t.Error("should return false for non-RTC message")
	}
}

func TestIsRTCSignalMessage(t *testing.T) {
	tests := []struct {
		msgType string
		want    bool
	}{
		{EventRTCOffer, true},
		{EventRTCAnswer, true},
		{EventRTCCandidate, true},
		{EventRTCConnected, true},
		{EventRTCFailed, true},
		{"text", false},
		{"status", false},
	}
	for _, tt := range tests {
		gm := GatewayMessage{Type: tt.msgType}
		if got := IsRTCSignalMessage(gm); got != tt.want {
			t.Errorf("IsRTCSignalMessage(%s) = %v, want %v", tt.msgType, got, tt.want)
		}
	}
}

func TestBrokerP2PTransport(t *testing.T) {
	b := NewBroker(nil)
	defer b.Stop()

	// Initially no P2P transport.
	if b.HasP2PTransport() {
		t.Error("broker should not have P2P transport initially")
	}

	// Set a mock P2P transport.
	mock := &mockTransport{}
	b.SetP2PTransport(mock)

	if !b.HasP2PTransport() {
		t.Error("broker should have P2P transport after SetP2PTransport")
	}

	// Revert to relay.
	b.SetP2PTransport(nil)
	if b.HasP2PTransport() {
		t.Error("broker should not have P2P transport after nil")
	}
}

// mockTransport implements Transport for testing.
type mockTransport struct {
	sent   [][]byte
	onMsg  func([]byte)
	onDisc func()
	closed bool
}

func (m *mockTransport) Send(data []byte) error {
	m.sent = append(m.sent, data)
	return nil
}
func (m *mockTransport) OnMessage(h func([]byte)) { m.onMsg = h }
func (m *mockTransport) OnDisconnect(h func())    { m.onDisc = h }
func (m *mockTransport) Close() error             { m.closed = true; return nil }
func (m *mockTransport) IsConnected() bool        { return !m.closed }

func TestSignalMessageJSON(t *testing.T) {
	sig := SignalMessage{
		Type:      "rtc_candidate",
		Candidate: `{"candidate":"candidate:...","sdpMid":"0"}`,
	}
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back SignalMessage
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Type != sig.Type || back.Candidate != sig.Candidate {
		t.Error("round-trip mismatch")
	}
}
