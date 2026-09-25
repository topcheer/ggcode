package lanchat

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestIssue2765SingleDatagramDeliveredToHub pins #2765: a small message sent
// through the production splitFragments single-datagram path must actually
// reach the receiving hub's onMessage callback. Before the fix the wrapped
// base64 payload failed the hub's json.Unmarshal ("message payload parse
// error"), the message was silently dropped, and the unconditional ACK still
// told the sender it was delivered - suppressing the multicast fallback.
func TestIssue2765SingleDatagramDeliveredToHub(t *testing.T) {
	hub, port := udpTestHub(t)
	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()

	got := make(chan Message, 1)
	hub.SetCallbacks(func(m Message) { got <- m }, nil, nil, nil, nil, nil)
	udp.Start()

	// Build a message envelope exactly as SendUnicast does: marshal the
	// envelope, compress, then run the production splitter - small payloads
	// take the single non-fragment datagram path with a base64-wrapped
	// Payload.
	msg := Message{ID: "msg-2765", FromNodeID: "peer-2765", Content: "single datagram body"}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	env := udpEnvelope{
		Type:       "message",
		APIKey:     hub.APIKey(),
		FromNode:   "peer-2765",
		FragmentID: "frag-2765",
		Payload:    payload,
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	frags := splitFragments(compressIfNeeded(envBytes), env.FragmentID, env.APIKey, env.FromNode, env.Type)
	if len(frags) != 1 {
		t.Fatalf("expected single datagram, got %d fragments", len(frags))
	}
	if frags[0].IsFragment {
		t.Fatal("small payload must take the non-fragment single-datagram path")
	}

	// Feed the exact production wire bytes through a real UDP socket.
	addr, ok := udp.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("transport addr is %T", addr)
	}
	sender, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial transport: %v", err)
	}
	defer sender.Close()
	wire, err := json.Marshal(frags[0])
	if err != nil {
		t.Fatalf("marshal wire datagram: %v", err)
	}
	if _, err := sender.Write(wire); err != nil {
		t.Fatalf("write datagram: %v", err)
	}

	select {
	case m := <-got:
		if m.ID != "msg-2765" {
			t.Fatalf("hub received wrong message: %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message never reached the hub callback - silently dropped (#2765: base64-wrapped payload failed the hub's json.Unmarshal while the ACK reported delivery)")
	}
}
