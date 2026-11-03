package webrtc

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
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

	tpt.OnMessage(func(data []byte) {})
	tpt.OnDisconnect(func() {})

	if tpt.IsConnected() {
		t.Error("transport should not be connected")
	}
}

func TestHandleDisconnectFiresExactlyOnce(t *testing.T) {
	peer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer failed: %v", err)
	}
	defer peer.Close()

	var calls int32
	peer.OnDisconnect(func() { atomic.AddInt32(&calls, 1) })

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

func TestStateTriggersTeardown(t *testing.T) {
	if stateTriggersTeardown(webrtc.PeerConnectionStateDisconnected) {
		t.Error("Disconnected is transient self-heal territory and must NOT tear down")
	}
	if !stateTriggersTeardown(webrtc.PeerConnectionStateFailed) {
		t.Error("Failed is terminal and MUST tear down")
	}
	for _, s := range []webrtc.PeerConnectionState{
		webrtc.PeerConnectionStateNew,
		webrtc.PeerConnectionStateConnecting,
		webrtc.PeerConnectionStateConnected,
		webrtc.PeerConnectionStateClosed,
	} {
		if stateTriggersTeardown(s) {
			t.Errorf("state %s must not trigger teardown", s)
		}
	}
}
