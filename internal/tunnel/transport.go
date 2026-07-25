package tunnel

// Transport is a bidirectional byte pipe for tunnel messages.
// Both WebSocket relay connections and WebRTC data channels implement this.
//
// All Transport implementations must be safe for concurrent Send calls.
// OnMessage and OnDisconnect callbacks are invoked from the transport's
// internal goroutine — handlers must not block.
type Transport interface {
	// Send writes a raw message (JSON GatewayMessage bytes) to the peer.
	// Returns an error if the transport is closed or the write fails.
	Send(data []byte) error

	// OnMessage sets the handler for incoming raw messages.
	// Only one handler may be registered; calling this replaces the previous one.
	OnMessage(handler func(data []byte))

	// OnDisconnect sets the handler for connection loss.
	// Called exactly once when the transport detects a disconnection.
	OnDisconnect(handler func())

	// Close terminates the transport and releases all resources.
	// It is safe to call multiple times.
	Close() error

	// IsConnected returns whether the transport is currently active and
	// capable of sending data. A transport that is reconnecting returns false.
	IsConnected() bool
}

// TransportKind identifies the transport implementation in use.
type TransportKind string

const (
	// TransportRelay is the WebSocket-via-relay transport (always available).
	TransportRelay TransportKind = "relay"
	// TransportP2P is the WebRTC DataChannel transport (direct P2P).
	TransportP2P TransportKind = "p2p"
)
