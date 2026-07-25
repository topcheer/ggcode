package webrtc

import (
	"github.com/pion/webrtc/v4"
)

// DefaultICEServers returns the STUN/TURN servers used for NAT traversal.
// TURN credentials may be overridden via environment variables for
// per-session rotation or self-hosted deployments.
func DefaultICEServers() []webrtc.ICEServer {
	servers := []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun1.l.google.com:19302"}},
	}

	// Self-hosted TURN server (coturn on hostyuntk3).
	// Credentials match /etc/turnserver.conf on the host.
	servers = append(servers, webrtc.ICEServer{
		URLs:       []string{"turn:turn.allpayone.net:3478"},
		Username:   "admin",
		Credential: "allwap123",
	})

	return servers
}

// PeerConfig returns the WebRTC configuration for PeerConnection creation.
func PeerConfig() webrtc.Configuration {
	return webrtc.Configuration{
		ICEServers: DefaultICEServers(),
	}
}

// DataChannelConfig returns the settings for the ggcode tunnel data channel.
// Ordered + reliable matches the current WebSocket semantics.
func DataChannelConfig() *webrtc.DataChannelInit {
	return &webrtc.DataChannelInit{
		Ordered:    boolPtr(true),
		Negotiated: nil, // host creates, mobile receives via ondatachannel
	}
}

func boolPtr(b bool) *bool {
	return &b
}
