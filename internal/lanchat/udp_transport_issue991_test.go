package lanchat

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// gzipBomb builds a small datagram whose decompressed size far exceeds
// maxUDPDecompressed (5MB of zeros compresses to a few KB).
func gzipBomb(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	payload := make([]byte, maxUDPDecompressed+1024*1024) // 5MB
	if _, err := gz.Write(payload); err != nil {
		t.Fatalf("write bomb: %v", err)
	}
	gz.Close()
	return buf.Bytes()
}

// TestIssue991GzipBombDroppedBeforeAllocation pins #991 problem 1: a
// high-ratio gzip datagram from an unauthenticated LAN host must be dropped
// by the decompression cap before allocating unbounded memory, with no
// dispatch to the hub and no panic.
func TestIssue991GzipBombDroppedBeforeAllocation(t *testing.T) {
	hub, port := udpTestHub(t)
	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()

	got := make(chan string, 1)
	hub.SetCallbacks(func(m Message) { got <- m.ID }, nil, nil, nil, nil, nil)
	udp.Start()

	bomb := gzipBomb(t)
	if len(bomb) > 60*1024 {
		t.Logf("note: bomb datagram is %d bytes", len(bomb))
	}
	addr, ok := udp.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("transport addr is %T", addr)
	}
	sender, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial transport: %v", err)
	}
	defer sender.Close()
	if _, err := sender.Write(bomb); err != nil {
		t.Fatalf("write bomb: %v", err)
	}

	select {
	case id := <-got:
		t.Fatalf("oversized gzip payload dispatched message %q", id)
	case <-time.After(300 * time.Millisecond):
		// dropped as required; transport still alive
	}
	// Transport must still be alive (no panic) — a well-formed message now
	// goes through and is dispatched.
	env := udpEnvelope{Type: "message", APIKey: hub.APIKey(), FromNode: "peer-991"}
	payload, _ := json.Marshal(Message{ID: "m-after-bomb", FromNodeID: "peer-991", Content: "ok"})
	env.Payload = payload
	data, _ := json.Marshal(env)
	if _, err := sender.Write(data); err != nil {
		t.Fatalf("write follow-up: %v", err)
	}
	select {
	case id := <-got:
		if id != "m-after-bomb" {
			t.Fatalf("unexpected message %q after bomb", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transport dead after gzip bomb (normal message not dispatched)")
	}
}

// TestIssue991NormalGzipStillPasses verifies the 4MB cap does not break the
// legitimate compressed-message path: a small gzipped envelope is decompressed
// and dispatched normally.
func TestIssue991NormalGzipStillPasses(t *testing.T) {
	hub, port := udpTestHub(t)
	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()

	got := make(chan string, 1)
	hub.SetCallbacks(func(m Message) { got <- m.ID }, nil, nil, nil, nil, nil)
	udp.Start()

	env := udpEnvelope{Type: "message", APIKey: hub.APIKey(), FromNode: "peer-991"}
	payload, _ := json.Marshal(Message{ID: "m-gzip", FromNodeID: "peer-991", Content: "compressed hello"})
	env.Payload = payload
	plain, _ := json.Marshal(env)
	data := compressIfNeeded(plain)

	addr, ok := udp.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("transport addr is %T", addr)
	}
	sender, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial transport: %v", err)
	}
	defer sender.Close()
	if _, err := sender.Write(data); err != nil {
		t.Fatalf("write gzip message: %v", err)
	}
	select {
	case id := <-got:
		if id != "m-gzip" {
			t.Fatalf("unexpected message %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("normal gzipped message was not dispatched (cap too strict?)")
	}
}

// TestIssue991SendUnicastErrorOnRetryExhaustion pins #991 problem 2: when a
// fragment exhausts all ACK retries (target port closed / no listener), the
// transport must return a non-nil error so the hub-side UDP multicast
// fallback fires instead of a false "delivered" nil.
//
// Retry-duration control: udpAckWaitFn is a package-level hook (the #991
// analogue of PeerRetryDelayFn) — the test shrinks the per-attempt ACK wait
// to 20ms so the full 3-attempt exhaustion finishes in ~60ms instead of 6s.
func TestIssue991SendUnicastErrorOnRetryExhaustion(t *testing.T) {
	hub, port := udpTestHub(t)
	udp, err := NewUDPTransport(port, "", hub, hub.NodeID(), hub.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	defer udp.Stop()

	orig := udpAckWaitFn
	udpAckWaitFn = func() time.Duration { return 20 * time.Millisecond }
	defer func() { udpAckWaitFn = orig }()

	// Fresh port with no listener: writes succeed (UDP) but no ACK ever returns.
	dead, err := net.ResolveUDPAddr("udp4", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("resolve dead addr: %v", err)
	}

	env := udpEnvelope{Type: "message", APIKey: hub.APIKey(), FromNode: hub.NodeID()}
	payload, _ := json.Marshal(Message{ID: "m-991", FromNodeID: hub.NodeID(), Content: "anyone?"})
	env.Payload = payload

	done := make(chan error, 1)
	go func() {
		done <- udp.SendUnicast(context.Background(), dead, env)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SendUnicast returned nil after exhausting all ACK retries (silent loss, multicast fallback skipped)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendUnicast did not return within 2s with shrunk retry timeout")
	}
}

// TestIssue991SendUnicastSuccessReturnsNil pins the regression contract: a
// fully ACKed unicast send still returns nil. Two transports on loopback; A
// sends to B's port, B ACKs, A must see success on first attempt.
func TestIssue991SendUnicastSuccessReturnsNil(t *testing.T) {
	hubA, portA := udpTestHub(t)
	udpA, err := NewUDPTransport(portA, "", hubA, hubA.NodeID(), hubA.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport A: %v", err)
	}
	defer udpA.Stop()

	hubB, portB := udpTestHub(t)
	udpB, err := NewUDPTransport(portB, "", hubB, hubB.NodeID(), hubB.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport B: %v", err)
	}
	defer udpB.Stop()

	orig := udpAckWaitFn
	udpAckWaitFn = func() time.Duration { return 500 * time.Millisecond }
	defer func() { udpAckWaitFn = orig }()

	hubA.SetCallbacks(func(Message) {}, nil, nil, nil, nil, nil)
	hubB.SetCallbacks(func(Message) {}, nil, nil, nil, nil, nil)
	udpA.Start()
	udpB.Start()

	env := udpEnvelope{Type: "message", APIKey: hubA.APIKey(), FromNode: hubA.NodeID()}
	payload, _ := json.Marshal(Message{ID: "m-ok", FromNodeID: hubA.NodeID(), Content: "hello"})
	env.Payload = payload

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	baddr, ok := udpB.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("B addr is %T", udpB.conn.LocalAddr())
	}
	// The listener reports 0.0.0.0 — rewrite to loopback for a routable
	// destination.
	baddr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: baddr.Port}
	if err := udpA.SendUnicast(ctx, baddr, env); err != nil {
		t.Fatalf("ACKed unicast send returned error: %v", err)
	}
}

// TestIssue991ACKPreregistrationNoLoss pins the #991 race fix: the ACK
// waiter is registered BEFORE WriteToUDP, so even when the peer's ACK comes
// back within the same scheduling slice (LAN RTT < 1ms), the first attempt
// ACKs instead of timing out. A loopback send with a generous per-attempt
// timeout must succeed on the very first try; with the old
// write-then-register order this intermittently burned retries.
func TestIssue991ACKPreregistrationNoLoss(t *testing.T) {
	hubA, portA := udpTestHub(t)
	udpA, err := NewUDPTransport(portA, "", hubA, hubA.NodeID(), hubA.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport A: %v", err)
	}
	defer udpA.Stop()

	hubB, portB := udpTestHub(t)
	udpB, err := NewUDPTransport(portB, "", hubB, hubB.NodeID(), hubB.APIKey())
	if err != nil {
		t.Fatalf("NewUDPTransport B: %v", err)
	}
	defer udpB.Stop()

	orig := udpAckWaitFn
	udpAckWaitFn = func() time.Duration { return time.Second }
	defer func() { udpAckWaitFn = orig }()

	hubA.SetCallbacks(func(Message) {}, nil, nil, nil, nil, nil)
	hubB.SetCallbacks(func(Message) {}, nil, nil, nil, nil, nil)
	udpA.Start()
	udpB.Start()

	// Rapid-fire round trips: each must ACK without exhausting retries.
	// Success is measured by total wall time — 20 sends that each need the
	// full 1s timeout would take 20s+; fully ACKed first-attempt sends
	// finish in milliseconds.
	baddr, ok := udpB.conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("B addr is %T", udpB.conn.LocalAddr())
	}
	baddr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: baddr.Port}
	start := time.Now()
	for i := 0; i < 20; i++ {
		env := udpEnvelope{
			Type:       "message",
			APIKey:     hubA.APIKey(),
			FromNode:   hubA.NodeID(),
			FragmentID: fmt.Sprintf("race-%d", i),
		}
		payload, _ := json.Marshal(Message{ID: fmt.Sprintf("m-race-%d", i), FromNodeID: hubA.NodeID(), Content: "ping"})
		env.Payload = payload
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := udpA.SendUnicast(ctx, baddr, env); err != nil {
			cancel()
			t.Fatalf("send %d failed (ACK lost to registration race?): %v", i, err)
		}
		cancel()
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("20 loopback sends took %v — ACKs are being missed and retried", elapsed)
	}
}
