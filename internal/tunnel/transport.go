package tunnel

type Transport interface {
	Send(data []byte) error

	OnMessage(handler func(data []byte))

	OnDisconnect(handler func())

	Close() error

	IsConnected() bool
}

type TransportKind string

const (
	TransportRelay TransportKind = "relay"

	TransportP2P TransportKind = "p2p"
)
