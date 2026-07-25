package webrtc

import (
	"fmt"

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
		startNegotiation func(sendSignal func(tunnel.SignalMessage), recv <-chan tunnel.SignalMessage) error,
		cleanup func(),
		err error,
	) {
		peer, err := NewPeer()
		if err != nil {
			return nil, nil, nil, nil, err
		}

		dc := NewDataChannelTransport(peer)

		// readyCh is closed when the DataChannel opens.
		ready := make(chan struct{})
		peer.OnDCOpen(func() {
			close(ready)
		})

		// negDone is closed in cleanup to unblock the signal processor
		// goroutine started by runHostNegotiation. Without this, the
		// goroutine blocks on `for sig := range recv` forever because
		// the recv channel (m.signalCh) is shared across negotiations
		// and never closed. Each Restart() would leak a goroutine that
		// also steals signals from the new negotiation.
		negDone := make(chan struct{})

		startNeg := func(sendSignal func(tunnel.SignalMessage), recv <-chan tunnel.SignalMessage) error {
			return runHostNegotiation(peer, sendSignal, recv, negDone)
		}

		cleanupFn := func() {
			close(negDone) // stop signal processor goroutine
			_ = peer.Close()
		}

		return dc, ready, startNeg, cleanupFn, nil
	}
}

// runHostNegotiation creates an SDP offer, sends it via sendSignal (which
// routes through the broker to the relay), and processes incoming answers
// and ICE candidates from recv (which come from the mobile via the relay).
func runHostNegotiation(peer *Peer, sendSignal func(tunnel.SignalMessage), recv <-chan tunnel.SignalMessage, done <-chan struct{}) error {
	// Wire local ICE candidate forwarding via the relay.
	peer.OnICECandidate(func(candidateStr string) {
		debug.Log("webrtc", "host: gathered ICE candidate: %s", candidateStr)
		sendSignal(tunnel.SignalMessage{
			Type:      "rtc_candidate",
			Candidate: candidateStr,
		})
	})

	// Create offer.
	offerSDP, err := peer.CreateOffer()
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}

	// Send offer once. The runUpgrade caller already waits 1s for mobile
	// to become ready in the relay before calling us. Retrying the offer
	// causes the mobile to create multiple PeerConnections (each ~50MB),
	// leading to OOM and ufrag mismatch errors from stale candidates.
	sendSignal(tunnel.SignalMessage{Type: "rtc_offer", SDP: offerSDP})
	debug.Log("webrtc", "host: sent SDP offer via relay")

	// Process incoming signaling messages from mobile until DC opens,
	// fails, or the negotiation is cancelled (done channel closed).
	safego.Go("webrtc.host.signalProcessor", func() {
		debug.Log("webrtc", "host: signal processor started, waiting for mobile response")
		for {
			select {
			case sig, ok := <-recv:
				if !ok {
					return
				}
				debug.Log("webrtc", "host: received signal from mobile: type=%s", sig.Type)
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
			case <-done:
				debug.Log("webrtc", "host: signal processor stopping (negotiation cancelled)")
				return
			}
		}
	})

	return nil
}
