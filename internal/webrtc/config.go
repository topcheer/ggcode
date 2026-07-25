package webrtc

import (
	"github.com/pion/webrtc/v4"
)

// DefaultICEServers returns the STUN/TURN servers used for NAT traversal.
// Includes both international and China-accessible servers for reliability.
func DefaultICEServers() []webrtc.ICEServer {
	servers := []webrtc.ICEServer{
		// China-accessible STUN servers (priority for mobile users in CN).
		{URLs: []string{"stun:stun.miwifi.com:3478"}},        // Xiaomi
		{URLs: []string{"stun:stun.qq.com:3478"}},            // Tencent
		{URLs: []string{"stun:stun.chat.bilibili.com:3478"}}, // Bilibili
		// International STUN (works outside CN or with VPN).
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		// Self-hosted TURN server (coturn on hostyuntk3).
		// Handles symmetric NAT / CGNAT where STUN fails.
		{
			URLs:       []string{"turn:turn.allpayone.net:3478"},
			Username:   "admin",
			Credential: "allwap123",
		},
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
