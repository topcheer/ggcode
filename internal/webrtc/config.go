package webrtc

import (
	"os"
	"strings"

	"github.com/pion/webrtc/v4"
)

func DefaultICEServers() []webrtc.ICEServer {
	turnUser := strings.TrimSpace(os.Getenv("GGCODE_TURN_USERNAME"))
	turnPass := strings.TrimSpace(os.Getenv("GGCODE_TURN_CREDENTIAL"))
	servers := []webrtc.ICEServer{
		{

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

func PeerConfig() webrtc.Configuration {
	return webrtc.Configuration{
		ICEServers: DefaultICEServers(),
	}
}

func DataChannelConfig() *webrtc.DataChannelInit {
	return &webrtc.DataChannelInit{
		Ordered:    boolPtr(true),
		Negotiated: nil,
	}
}

func boolPtr(b bool) *bool {
	return &b
}
