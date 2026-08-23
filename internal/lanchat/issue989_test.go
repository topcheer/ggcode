package lanchat

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// udpTestHub builds a custom-key hub for UDP transport tests. Returns
// port 0 so the transport binds its own ephemeral port (a pre-bound
// placeholder conn would collide with the transport's own listen).
func udpTestHub(t *testing.T) (*Hub, int) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	hub := NewHub("self-node", "tui", "http://127.0.0.1:1", "my-strong-secret", store, WorkspaceMeta{})
	return hub, 0
}

// TestIssue989UDPTransportUsesHubAPIKey pins the #989 fix: the UDP transport
// must be constructed with the hub's EFFECTIVE key (hub.APIKey()), not the
// hardcoded community key. Before the fix, a custom-key node's UDP fallback
// envelopes carried the custom key while the receiver's t.apiKey stayed
// communityKey, so #988 turned the TCP 401 into a silent UDP drop.
func TestIssue989UDPTransportUsesHubAPIKey(t *testing.T) {
	hub, port := udpTestHub(t)

	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()
	if got := udp.apiKey; got != "my-strong-secret" {
		t.Fatalf("UDP transport key = %q, want configured key (constructors must pass hub.APIKey(), #989)", got)
	}
}

// TestIssue989UDPAuthMirrorsHTTPGate pins #989 problem 2: the UDP inbound
// gate must mirror the TCP AuthMiddleware semantics - a custom-key receiver
// rejects community-key envelopes outright (the old unconditional
// "|| communityKey" branch let any LAN host inject a custom-key hub).
func TestIssue989UDPAuthMirrorsHTTPGate(t *testing.T) {
	hub, port := udpTestHub(t)
	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()

	got := make(chan string, 1)
	hub.SetCallbacks(func(m Message) { got <- m.ID }, nil, nil, nil, nil, nil)
	udp.Start()

	env := udpEnvelope{
		Type:     "message",
		APIKey:   communityKey, // well-known key must NOT pass a custom-key gate
		FromNode: "attacker",
	}
	payload, _ := json.Marshal(Message{ID: "m-evil", FromNodeID: "attacker", Content: "inject"})
	env.Payload = payload
	data, _ := json.Marshal(env)
	addr, ok := udp.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("transport addr is %T, want *net.UDPAddr", udp.conn.LocalAddr())
	}
	sender, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial transport: %v", err)
	}
	defer sender.Close()
	if _, err := sender.Write(data); err != nil {
		t.Fatalf("write datagram: %v", err)
	}
	select {
	case id := <-got:
		t.Fatalf("community-key envelope accepted by custom-key UDP gate; message %q dispatched", id)
	case <-time.After(300 * time.Millisecond):
		// dropped as required
	}
}

// TestIssue989CustomKeyDMEndToEnd pins #989 problem 3: a real Hub A ->
// Hub B link where BOTH sides run the same custom key must interoperate on
// the DM main path. The receiver exposes the production HTTP route behind
// AuthMiddleware; the sender runs SendDirect; the message must land.
func TestIssue989CustomKeyDMEndToEnd(t *testing.T) {
	const sharedKey = "team-shared-key-989"
	storeB := NewStore(filepath.Join(t.TempDir(), "storeB"))
	hubB := NewHub("node-b", "tui", "http://127.0.0.1:1", sharedKey, storeB, WorkspaceMeta{})

	got := make(chan Message, 1)
	hubB.SetCallbacks(func(m Message) { got <- m }, nil, nil, nil, nil, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/lanchat/message", AuthMiddleware(sharedKey, hubB.handleReceiveMessage))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	storeA := NewStore(filepath.Join(t.TempDir(), "storeA"))
	hubA := NewHub("node-a", "tui", srv.URL, sharedKey, storeA, WorkspaceMeta{})
	hubA.mu.Lock()
	hubA.peers["node-b"] = &Participant{NodeID: "node-b", Endpoint: srv.URL, HumanNick: "B"}
	hubA.mu.Unlock()

	if err := hubA.SendDirect(context.Background(), "node-b", "human", "hello over custom key", nil); err != nil {
		t.Fatalf("SendDirect with shared custom key failed (interop broken): %v", err)
	}
	select {
	case m := <-got:
		if m.Content != "hello over custom key" {
			t.Fatalf("unexpected content: %q", m.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DM never arrived at receiver within 2s (custom-key interop broken)")
	}
}
