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

func udpTestHub(t *testing.T) (*Hub, int) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	hub := NewHub("self-node", "tui", "http://127.0.0.1:1", "my-strong-secret", store, WorkspaceMeta{})
	return hub, 0
}

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
		APIKey:   communityKey,
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

	}
}

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
