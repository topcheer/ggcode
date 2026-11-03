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

func gzipBomb(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	payload := make([]byte, maxUDPDecompressed+1024*1024)
	if _, err := gz.Write(payload); err != nil {
		t.Fatalf("write bomb: %v", err)
	}
	gz.Close()
	return buf.Bytes()
}

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

	udp.processDatagram(bomb, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 65001}, "udp-test")

	select {
	case id := <-got:
		t.Fatalf("oversized gzip payload dispatched message %q", id)
	case <-time.After(300 * time.Millisecond):

	}

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

	baddr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: baddr.Port}
	if err := udpA.SendUnicast(ctx, baddr, env); err != nil {
		t.Fatalf("ACKed unicast send returned error: %v", err)
	}
}

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
