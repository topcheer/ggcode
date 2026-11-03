package webrtc

import (
	"github.com/topcheer/ggcode/internal/tunnel"
)

var _ tunnel.Transport = (*DataChannelTransport)(nil)

type DataChannelTransport struct {
	peer *Peer
}

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
