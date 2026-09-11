package webrtc

import (
	"sync/atomic"
	"testing"
	"time"
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

// #1827 case 2: four distinct trigger points (PeerConnectionState
// Disconnected/Failed, DataChannel OnClose/OnError) can all observe the
// same link failure within milliseconds; handleDisconnect must fire the
// callback exactly once per Peer lifecycle - every pre-fix invocation
// spawned its own goroutine, driving duplicate retry/Start cycles.
func TestHandleDisconnectFiresExactlyOnce(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	var calls int32
	peer.OnDisconnect(func() { atomic.AddInt32(&calls, 1) })

	// Simulate the double-fire: state-change callback + DataChannel close
	// arriving back to back for the same link failure.
	peer.handleDisconnect()
	peer.handleDisconnect()
	peer.handleDisconnect()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&calls) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("disconnect callback fired %d times, want exactly 1", atomic.LoadInt32(&calls))
}
