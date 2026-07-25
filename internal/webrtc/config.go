package webrtc

import (
	"os"
	"strings"

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

	// Optional self-hosted TURN server (coturn).
	// Env: GGCODE_TURN_URL=turn:turn.ggcode.dev:3478
	//      GGCODE_TURN_USER=ggcode
	//      GGCODE_TURN_PASS=<secret>
	if turnURL := strings.TrimSpace(os.Getenv("GGCODE_TURN_URL")); turnURL != "" {
		servers = append(servers, webrtc.ICEServer{
			URLs:       []string{turnURL},
			Username:   os.Getenv("GGCODE_TURN_USER"),
			Credential: os.Getenv("GGCODE_TURN_PASS"),
		})
	}

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
