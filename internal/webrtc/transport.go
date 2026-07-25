package webrtc

import (
	"github.com/topcheer/ggcode/internal/tunnel"
)

// Compile-time assertion: DataChannelTransport implements tunnel.Transport.
var _ tunnel.Transport = (*DataChannelTransport)(nil)

// DataChannelTransport adapts a WebRTC DataChannel (via Peer) to the
// tunnel.Transport interface. It allows the Broker to send/receive
// GatewayMessages over a P2P connection instead of a relay WebSocket.
type DataChannelTransport struct {
	peer *Peer
}

// NewDataChannelTransport wraps an existing Peer as a tunnel.Transport.
// The Peer must have its OnMessage/OnDisconnect handlers wired before
// the DataChannel opens (i.e., before ICE completes).
func NewDataChannelTransport(peer *Peer) *DataChannelTransport {
	return &DataChannelTransport{peer: peer}
}

func (t *DataChannelTransport) Send(data []byte) error {
	return t.peer.Send(data)
}

func (t *DataChannelTransport) OnMessage(handler func(data []byte)) {
	t.peer.OnMessage(handler)
}

func (t *DataChannelTransport) OnDisconnect(handler func()) {
	t.peer.OnDisconnect(handler)
}

func (t *DataChannelTransport) Close() error {
	return t.peer.Close()
}

func (t *DataChannelTransport) IsConnected() bool {
	return t.peer.IsReady()
}
