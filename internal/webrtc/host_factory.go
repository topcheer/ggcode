package webrtc

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// HostPeerFactory creates a PeerFactory for the host (offerer) side.
// It implements tunnel.PeerFactory without creating a circular import.
func HostPeerFactory() tunnel.PeerFactory {
	return func() (
		transport tunnel.Transport,
		readyCh <-chan struct{},
		startNegotiation func(signalCh chan tunnel.SignalMessage) error,
		cleanup func(),
		err error,
	) {
		peer, err := NewPeer()
		if err != nil {
			return nil, nil, nil, nil, err
		}

		dc := NewDataChannelTransport(peer)

		// readyCh is closed when the DataChannel opens.
		// Peer.dcReadyCh is already a chan struct{} — we expose it read-only.
		ready := make(chan struct{})
		peer.OnDCOpen(func() {
			close(ready)
		})

		startNeg := func(signalCh chan tunnel.SignalMessage) error {
			return runHostNegotiation(peer, signalCh)
		}

		cleanupFn := func() {
			_ = peer.Close()
		}

		return dc, ready, startNeg, cleanupFn, nil
	}
}

// runHostNegotiation creates an SDP offer, sends it via signalCh,
// and processes incoming answers and ICE candidates.
func runHostNegotiation(peer *Peer, signalCh chan tunnel.SignalMessage) error {
	var once sync.Once

	// Wire ICE candidate forwarding.
	peer.OnICECandidate(func(candidateStr string) {
		sig := tunnel.SignalMessage{
			Type:      "rtc_candidate",
			Candidate: candidateStr,
		}
		select {
		case signalCh <- sig:
		default:
		}
	})

	// Create offer.
	offerSDP, err := peer.CreateOffer()
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}

	// Send offer.
	select {
	case signalCh <- tunnel.SignalMessage{Type: "rtc_offer", SDP: offerSDP}:
		debug.Log("webrtc", "host: sent SDP offer")
	default:
		return fmt.Errorf("signal channel full when sending offer")
	}

	// Wait for answer and ICE candidates from mobile.
	// This runs in a background goroutine started by the upgrade manager.
	once.Do(func() {}) // placeholder for future cleanup

	// Process incoming signaling messages until DC opens or fails.
	safego.Go("webrtc.host.signalProcessor", func() {
		for sig := range signalCh {
			switch sig.Type {
			case "rtc_answer":
				if err := peer.SetRemoteAnswer(sig.SDP); err != nil {
					debug.Log("webrtc", "host: set remote answer: %v", err)
				} else {
					debug.Log("webrtc", "host: applied SDP answer")
				}
			case "rtc_candidate":
				if err := peer.AddICECandidate(sig.Candidate); err != nil {
					debug.Log("webrtc", "host: add ICE candidate: %v", err)
				}
			}
		}
	})

	return nil
}

// SerializeSignalMessage converts a tunnel.SignalMessage to JSON bytes
// for transmission over the relay WebSocket.
func SerializeSignalMessage(sig tunnel.SignalMessage) ([]byte, error) {
	return json.Marshal(sig)
}

// DeserializeSignalMessage parses JSON bytes into a tunnel.SignalMessage.
func DeserializeSignalMessage(data []byte) (tunnel.SignalMessage, error) {
	var sig tunnel.SignalMessage
	err := json.Unmarshal(data, &sig)
	return sig, err
}
