package webrtc

import (
	"testing"
)

func TestNewPeer(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	if peer.IsReady() {
		t.Error("peer should not be ready before ICE completes")
	}
}

func TestCreateOffer(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	offer, err := peer.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer failed: %v", err)
	}
	if offer == "" {
		t.Error("offer should not be empty")
	}
}

func TestEncodeDecodeSDP(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	offer, err := peer.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer failed: %v", err)
	}

	// Round-trip: encode then decode should produce a valid SDP.
	desc, err := decodeSDP(offer)
	if err != nil {
		t.Fatalf("decodeSDP failed: %v", err)
	}
	if desc.Type.String() != "offer" {
		t.Errorf("expected SDP type offer, got %s", desc.Type.String())
	}
}

func TestHostPeerFactory(t *testing.T) {
	factory := HostPeerFactory()

	transport, readyCh, startNeg, cleanup, err := factory()
	if err != nil {
		t.Fatalf("factory failed: %v", err)
	}
	defer cleanup()

	if transport == nil {
		t.Fatal("transport should not be nil")
	}
	if readyCh == nil {
		t.Fatal("readyCh should not be nil")
	}
	if startNeg == nil {
		t.Fatal("startNegotiation should not be nil")
	}

	// Transport should report not connected before ICE.
	if transport.IsConnected() {
		t.Error("transport should not be connected before ICE")
	}
}

func TestDataChannelTransportInterface(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	tpt := NewDataChannelTransport(peer)

	// Verify it implements the Transport interface by calling methods.
	tpt.OnMessage(func(data []byte) {})
	tpt.OnDisconnect(func() {})

	if tpt.IsConnected() {
		t.Error("transport should not be connected")
	}
}
