package webrtc

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tunnel"
)

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

		ready := make(chan struct{})
		peer.OnDCOpen(func() {
			close(ready)
		})

		negDone := make(chan struct{})

		startNeg := func(sendSignal func(tunnel.SignalMessage), recv <-chan tunnel.SignalMessage) error {
			return runHostNegotiation(peer, sendSignal, recv, negDone)
		}

		cleanupFn := func() {
			close(negDone)
			_ = peer.Close()
		}

		return dc, ready, startNeg, cleanupFn, nil
	}
}

func runHostNegotiation(peer *Peer, sendSignal func(tunnel.SignalMessage), recv <-chan tunnel.SignalMessage, done <-chan struct{}) error {

	peer.OnICECandidate(func(candidateStr string) {
		debug.Log("webrtc", "host: gathered ICE candidate: %s", candidateStr)
		sendSignal(tunnel.SignalMessage{
			Type:      "rtc_candidate",
			Candidate: candidateStr,
		})
	})

	offerSDP, err := peer.CreateOffer()
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}

	sendSignal(tunnel.SignalMessage{Type: "rtc_offer", SDP: offerSDP})
	debug.Log("webrtc", "host: sent SDP offer via relay")

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
