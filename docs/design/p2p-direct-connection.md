# P2P Direct Connection Design

## Overview

Replace the current relay-mediated WebSocket transport between Host (Go) and
Mobile (Flutter) with a WebRTC P2P data channel. The relay server is retained
for signaling and as a TURN fallback (~30% of connections that cannot establish
direct P2P due to symmetric NAT or CGNAT).

## Current Architecture

```
Mobile (Flutter) ──WSS──→ Relay Server ←──WSS── Host (Go)
                         (gateway.ggcode.dev)
```

- All application data flows through the relay.
- Protocol: `GatewayMessage` JSON over encrypted WebSocket.
- Encryption: AES-GCM with session key established via relay-mediated key exchange.
- Pairing: Host requests a room from relay → generates QR code → Mobile scans.

## Target Architecture

```
                    ┌──────────────────────┐
                    │   Relay Server       │
                    │ (signaling + TURN)   │
                    └──┬───────────────┬───┘
          SDP/ICE sig  │               │  TURN (fallback ~30%)
                ┌──────┴──┐       ┌────┴──────┐
                │  Host   │       │           │
                │  (Go)   │───────┤ P2P DC    │
                │ pion    │ P2P   │ (~70%)    │
                └─────────┘       └───────────┘
                                      │
                               ┌──────┴──────┐
                               │ Mobile (FL) │
                               │flutter_webrtc│
                               └─────────────┘
```

## Design Principles

1. **Protocol-preserving**: The existing `GatewayMessage` JSON protocol,
   `session_id`/`event_id` ordering, replay, and broker semantics are unchanged.
   Only the byte transport changes (WebSocket → WebRTC DataChannel).

2. **Incremental upgrade**: The connection starts as relay WebSocket, then
   upgrades to P2P when possible. If P2P fails, it stays on the relay
   (or falls back to it). This guarantees zero regression.

3. **User transparency**: The user scans the same QR code. The P2P upgrade
   happens automatically. The only visible difference is a connection-quality
   indicator (P2P / Relayed / Connecting).

## Detailed Design

### 1. Transport Abstraction

Introduce a `Transport` interface in the `tunnel` package that both WebSocket
and WebRTC DataChannel implement:

```go
// internal/tunnel/transport.go
package tunnel

// Transport is a bidirectional byte pipe for tunnel messages.
// Implementations: WebSocketRelayTransport, WebRTCTransport.
type Transport interface {
    // Send writes a raw message (JSON GatewayMessage bytes) to the peer.
    Send(data []byte) error
    // OnMessage sets the handler for incoming messages.
    OnMessage(handler func(data []byte))
    // OnDisconnect sets the handler for connection loss.
    OnDisconnect(handler func())
    // Close terminates the transport.
    Close() error
    // IsConnected returns whether the transport is currently active.
    IsConnected() bool
}
```

The existing `RelayClient` becomes the WebSocket transport implementation.
`Broker` is refactored to use `Transport` instead of directly referencing
`*Session` / `*RelayClient`.

### 2. WebRTC Layer (Host — Go)

Package: `internal/webrtc/`

**Files:**
- `peer.go` — `PeerConnection` wrapper, DataChannel management
- `signal.go` — SDP offer/answer marshaling, ICE candidate exchange
- `transport.go` — implements `tunnel.Transport` via DataChannel
- `config.go` — STUN/TURN server configuration

**PeerConnection lifecycle:**
```go
type Peer struct {
    pc       *webrtc.PeerConnection
    dc       *webrtc.DataChannel
    onMessage func([]byte)
    onDisconnect func()
    once     sync.Once
}

// CreateOffer creates a PeerConnection (host side) and returns the SDP offer.
func (p *Peer) CreateOffer() (sdp string, err error)

// CreateAnswer accepts an SDP offer and returns the answer (mobile side).
func (p *Peer) CreateAnswer(offer string) (sdp string, err error)

// SetRemoteDescription sets the remote SDP (for the answerer).
func (p *Peer) SetRemoteDescription(sdp string) error

// AddICECandidate adds a remote ICE candidate.
func (p *Peer) AddICECandidate(candidate string) error

// OnICECandidate registers a callback for local ICE candidates.
func (p *Peer) OnICECandidate(fn func(candidate string))
```

**DataChannel configuration:**
- `Negotiated: false` (host creates, mobile waits for `ondatachannel`)
- `Ordered: true` (matches WebSocket semantics)
- Protocol label: `"ggcode-tunnel"`
- Max retransmit: 0 (reliable mode, SCTP handles retransmission internally)

### 3. Signaling Protocol

WebRTC signaling (SDP + ICE candidates) is exchanged **over the existing relay
WebSocket connection** before the upgrade. This reuses the room/pairing
infrastructure with zero changes to the relay server.

**New GatewayMessage types** (added to `protocol.go`):

```go
// WebRTC signaling message types
const (
    EventRTCOffer    = "rtc_offer"      // Host → Mobile: SDP offer
    EventRTCAnswer   = "rtc_answer"     // Mobile → Host: SDP answer
    EventRTCCandidate = "rtc_candidate" // Bidirectional: ICE candidate
    EventRTCConnected = "rtc_connected" // Bidirectional: P2P established
    EventRTCFailed    = "rtc_failed"    // Bidirectional: P2P failed
)
```

**Signaling flow:**
```
1. Host connects to relay (existing flow, WSS)
2. Mobile connects to relay (existing flow, WSS)
3. Key exchange + authentication (existing flow)
4. Host creates PeerConnection, generates SDP offer
5. Host sends rtc_offer via relay WebSocket
6. Mobile receives rtc_offer, creates PeerConnection, generates SDP answer
7. Mobile sends rtc_answer via relay WebSocket
8. Both sides exchange rtc_candidate messages (trickle ICE)
9. ICE completes → DataChannel opens → rtc_connected
10. Switch: all future GatewayMessages go over DataChannel
11. Keep relay WebSocket as signaling-only heartbeat (for reconnect detection)
```

### 4. Connection Upgrade Manager

New component: `internal/tunnel/upgrade.go`

```go
type UpgradeManager struct {
    relay    Transport       // current relay transport
    p2p      Transport       // WebRTC transport (nil until established)
    broker   *Broker         // the active broker
    mu       sync.Mutex
    state    UpgradeState
    onSwitch func(Transport) // notify broker of transport switch
}

type UpgradeState int
const (
    UpgradeIdle UpgradeState = iota  // relay only
    UpgradeNegotiating                // signaling in progress
    UpgradeActive                     // P2P active
    UpgradeFailed                     // P2P failed, staying on relay
)
```

**Upgrade logic:**
- Triggered automatically after relay connection is established and authenticated.
- Timeout: 10 seconds for ICE to complete. If timeout → `UpgradeFailed`,
  stays on relay.
- On P2P connect: `broker.SwitchTransport(p2pTransport)`.
  Messages start flowing over DataChannel. Relay stays open as signaling channel.
- On P2P disconnect: `broker.SwitchTransport(relayTransport)`.
  Messages flow back over relay. Re-attempt P2P upgrade after 30s.

### 5. Broker Transport Switching

`Broker` is modified to support live transport switching:

```go
type Broker struct {
    // ... existing fields ...
    transport Transport
    transportMu sync.RWMutex
}

// SwitchTransport atomically swaps the active transport.
// In-flight messages in the old transport's buffer are preserved.
func (b *Broker) SwitchTransport(t Transport) {
    b.transportMu.Lock()
    old := b.transport
    b.transport = t
    b.transportMu.Unlock()
    if old != nil {
        old.Close()
    }
}

func (b *Broker) senderLoop() {
    for {
        b.outMu.Lock()
        for len(b.outbound) == 0 && !b.isDone() {
            b.outCond.Wait()
        }
        batch := b.outbound
        b.outbound = nil
        b.outMu.Unlock()

        b.transportMu.RLock()
        t := b.transport
        b.transportMu.RUnlock()

        for _, msg := range batch {
            data, _ := json.Marshal(msg)
            if err := t.Send(data); err != nil {
                // transport error → buffer for retry
                b.requeue(msg)
            }
        }
    }
}
```

### 6. STUN/TURN Configuration

```go
var defaultICEServers = []webrtc.ICEServer{
    {URLs: []string{"stun:stun.l.google.com:19302"}},
    {URLs: []string{"stun:stun1.l.google.com:19302"}},
    {
        URLs:       []string{"turn:turn.ggcode.dev:3478"},
        Username:   "ggcode",
        Credential: "<rotating-secret>",
    },
}
```

TURN server: self-hosted `coturn` Docker container on the relay host.
Credential rotation via the relay's share session (auth_ticket includes
TURN credentials).

### 7. Mobile Integration (Flutter)

New package: `mobile/flutter/lib/webrtc/`

**Files:**
- `peer_connection.dart` — flutter_webrtc wrapper
- `p2p_transport.dart` — implements same send/receive interface as ConnectionService
- `signaling.dart` — handles rtc_offer/rtc_answer/rtc_candidate messages

**Connection flow:**
```dart
// After relay WebSocket connects and authenticates:
// 1. Listen for rtc_offer from host
// 2. On rtc_offer: create RTCPeerConnection, setRemoteDescription
// 3. Create answer, send rtc_answer back via relay WebSocket
// 4. Exchange ICE candidates via rtc_candidate messages
// 5. OnDataChannel open: switch message routing from WebSocket to DataChannel
// 6. On DataChannel close: switch back to WebSocket
```

**UI feedback:**
```
┌─────────────────────────────┐
│  ● P2P Direct    ← green    │  P2P active, lowest latency
│  ● Relayed       ← yellow   │  TURN fallback, slightly higher latency
│  ● Connecting... ← gray     │  Negotiating
└─────────────────────────────┘
```

### 8. Security

- **DTLS-SRTP**: WebRTC mandates DTLS for DataChannel encryption. This
  replaces the current AES-GCM layer for P2P messages.
- **Relay messages**: Still encrypted via existing AES-GCM (for signaling).
- **TURN credentials**: Short-lived, rotated per share session.
- **Fingerprint verification**: SDP fingerprints are verified against the
  authenticated peer (the relay already authenticates both parties).

### 9. Reconnection and Edge Cases

| Scenario | Behavior |
|---|---|
| P2P established, then drops | Switch back to relay, re-attempt P2P after 30s |
| Relay drops while P2P active | Keep P2P, attempt relay reconnect in background |
| Both drop | Full reconnect cycle (relay first, then P2P upgrade) |
| ICE timeout (10s) | Stay on relay, mark `UpgradeFailed` |
| Mobile background (iOS) | DataChannel may be killed by OS. On foreground: reconnect relay + re-upgrade |
| Mobile network change (WiFi→4G) | P2P drops → relay reconnect → re-upgrade |

### 10. Relay Server Changes

**Minimal.** The relay server requires **zero protocol changes**. It only needs
to forward `rtc_offer`, `rtc_answer`, and `rtc_candidate` messages between the
two WebSocket clients in the same room — which it already does for any
GatewayMessage.

**Optional TURN deployment:** Add a `coturn` container alongside the relay.

### 11. Rollout Strategy

**Phase 1 (this implementation):**
- Add WebRTC transport layer to Host
- Add Transport abstraction to Broker
- Add upgrade manager
- Mobile-side flutter_webrtc integration
- Feature-flagged via config: `p2p.enabled = true|false`
- Default: OFF in production until tested

**Phase 2 (post-verification):**
- Enable P2P upgrade for all connections
- Deploy TURN server
- Monitor P2P success rate

### 12. File Manifest

**Host (Go):**
- `internal/tunnel/transport.go` — Transport interface
- `internal/tunnel/upgrade.go` — UpgradeManager
- `internal/tunnel/protocol.go` — add RTC signaling types
- `internal/tunnel/broker.go` — use Transport, add SwitchTransport
- `internal/webrtc/peer.go` — PeerConnection wrapper
- `internal/webrtc/signal.go` — SDP/ICE helpers
- `internal/webrtc/transport.go` — DataChannel → tunnel.Transport
- `internal/webrtc/config.go` — ICE servers

**Mobile (Flutter):**
- `mobile/flutter/lib/webrtc/peer_connection.dart`
- `mobile/flutter/lib/webrtc/p2p_transport.dart`
- `mobile/flutter/lib/webrtc/signaling.dart`
- `mobile/flutter/lib/core/connection_service.dart` — add RTC message routing

**Shared (config):**
- `internal/config/config.go` — add `p2p.enabled` setting

### 13. Dependencies

**Host:**
- `github.com/pion/webrtc/v4` — pure Go WebRTC implementation

**Mobile:**
- `flutter_webrtc: ^0.12.0` — Flutter WebRTC plugin

### 14. Testing Plan

1. **Unit tests**: PeerConnection creation, SDP marshaling, Transport interface
2. **Integration test**: Host ↔ Host (loopback) DataChannel round-trip
3. **NAT traversal test**: Two machines behind different NATs
4. **Fallback test**: Force ICE failure → verify relay stays active
5. **Reconnect test**: Kill DataChannel → verify relay reconnection + re-upgrade
6. **Mobile background test**: App to background → foreground → verify reconnect
