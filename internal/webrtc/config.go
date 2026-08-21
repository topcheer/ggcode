package webrtc

import (
	"os"
	"strings"

	"github.com/pion/webrtc/v4"
)

// DefaultICEServers returns the STUN/TURN servers used for NAT traversal.
//
// NOTE: Chinese ISPs commonly block UDP port 3478 (the standard STUN/TURN port).
// The self-hosted TURN server on hostyuntk3 uses port 8443 to avoid this.
// Public STUN servers on 3478 are omitted because they are unreachable from
// most CN mobile networks (CGNAT + port 3478 blocking).
//
// #924: TURN credentials are injected via GGCODE_TURN_USERNAME /
// GGCODE_TURN_CREDENTIAL (no hardcoded default - the previously embedded
// pair was extractable from any binary and usable against the public
// server). With no credentials configured only the STUN Binding URLs are
// returned; deployments needing TURN relay must set both variables.
func DefaultICEServers() []webrtc.ICEServer {
	turnUser := strings.TrimSpace(os.Getenv("GGCODE_TURN_USERNAME"))
	turnPass := strings.TrimSpace(os.Getenv("GGCODE_TURN_CREDENTIAL"))
	servers := []webrtc.ICEServer{
		{
			// Self-hosted server (coturn on hostyuntk3). Also serves STUN
			// (Binding) on the same port. Port 8443 avoids ISP blocking of
			// 3478. STUN needs no credentials.
			URLs: []string{"stun:turn.allpayone.net:8443"},
		},
	}
	if turnUser != "" && turnPass != "" {
		servers = append(servers, webrtc.ICEServer{
			URLs: []string{
				"turn:turn.allpayone.net:8443?transport=udp",
				"turn:turn.allpayone.net:8443?transport=tcp",
			},
			Username:   turnUser,
			Credential: turnPass,
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
