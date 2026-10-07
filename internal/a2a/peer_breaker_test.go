package a2a

// sa-88 / r33-C: per-peer A2A outbound circuit breaker tests.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestBreaker() *peerBreaker {
	return &peerBreaker{}
}

func TestPeerBreaker_ClosedBelowThreshold(t *testing.T) {
	b := newTestBreaker()
	for i := 1; i < peerBreakerThreshold; i++ {
		if !b.allow() {
			t.Fatalf("call %d must be allowed while below threshold", i)
		}
		b.record(true)
	}
	if b.stateSnapshot() != peerBreakerClosed {
		t.Fatalf("expected CLOSED after %d/%d failures, got %v", peerBreakerThreshold-1, peerBreakerThreshold, b.stateSnapshot())
	}
}

func TestPeerBreaker_OpensAtThreshold(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < peerBreakerThreshold; i++ {
		b.record(true)
	}
	if b.stateSnapshot() != peerBreakerOpen {
		t.Fatalf("expected OPEN after %d consecutive infra failures", peerBreakerThreshold)
	}
	if b.allow() {
		t.Fatal("OPEN circuit within cooldown must fast-fail")
	}
}

func TestPeerBreaker_SuccessResetsFailures(t *testing.T) {
	b := newTestBreaker()
	b.record(true)
	b.record(true)
	b.record(false) // success resets
	b.record(true)
	b.record(true)
	if b.stateSnapshot() != peerBreakerClosed {
		t.Fatal("2 failures after a success must not open (non-consecutive)")
	}
}

func TestPeerBreaker_CooldownThenHalfOpenProbe(t *testing.T) {
	old := peerBreakerCooldown
	peerBreakerCooldown = 10 * time.Millisecond
	defer func() { peerBreakerCooldown = old }()

	b := newTestBreaker()
	for i := 0; i < peerBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond)
	if !b.allow() {
		t.Fatal("expired OPEN must admit exactly one probe call")
	}
	if b.stateSnapshot() != peerBreakerHalfOpen {
		t.Fatal("expired OPEN admission must transition to HALF_OPEN")
	}
	if b.allow() {
		t.Fatal("second call during HALF_OPEN probe must be barred")
	}
	// Probe succeeds: circuit closes.
	b.record(false)
	if b.stateSnapshot() != peerBreakerClosed {
		t.Fatal("successful probe must close the circuit")
	}
	// Probe failing instead re-opens with a fresh cooldown.
	for i := 0; i < peerBreakerThreshold; i++ {
		b.record(true)
	}
	time.Sleep(15 * time.Millisecond)
	if !b.allow() {
		t.Fatal("second cycle: expired OPEN must admit probe")
	}
	b.record(true)
	if b.stateSnapshot() != peerBreakerOpen {
		t.Fatal("failed probe must re-open the circuit")
	}
}

func TestPeerBreaker_StragglerSuccessClosesOpen(t *testing.T) {
	b := newTestBreaker()
	for i := 0; i < peerBreakerThreshold; i++ {
		b.record(true)
	}
	// A call that started before the circuit opened finishes late and
	// succeeds: closes the circuit early.
	b.record(false)
	if b.stateSnapshot() != peerBreakerClosed {
		t.Fatal("straggler success must close an OPEN circuit")
	}
}

func TestPeerBreaker_RegistrySharedByBaseURL(t *testing.T) {
	resetPeerBreakers()
	a := peerBreakerFor("http://peer.example/api")
	b1 := peerBreakerFor("http://peer.example/api")
	if a != b1 {
		t.Fatal("same base URL must share one breaker")
	}
	if peerBreakerFor("http://other.example/api") == a {
		t.Fatal("different base URL must get a distinct breaker")
	}
	if peerBreakerFor("") != nil {
		t.Fatal("empty base URL must return nil breaker")
	}
	resetPeerBreakers()
}

func TestClassifyRPCError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		trip bool
	}{
		{"nil", nil, false},
		{"ctx canceled", context.Canceled, false},
		{"ctx deadline", context.DeadlineExceeded, false},
		{"jsonrpc app error", &JSONRPCError{Code: -32000, Message: "task not found"}, false},
		{"local marshal", fmt.Errorf("a2a send: marshal params: boom"), false},
		{"local decode", fmt.Errorf("a2a send: decode: bad json"), false},
		{"result unmarshal", fmt.Errorf("a2a send: unmarshal result: bad json"), false},
		{"transport", errors.New("connection refused"), true},
		{"http status", fmt.Errorf("a2a send: HTTP 503: unavailable"), true},
	}
	for _, tc := range cases {
		if got := classifyRPCError(tc.err); got != tc.trip {
			t.Errorf("%s: classifyRPCError=%v want %v", tc.name, got, tc.trip)
		}
	}
}

// TestRPC_FastFailOnOpenCircuit verifies the client-level integration:
// after the peer's circuit opens, rpc returns peerCircuitOpenError
// WITHOUT issuing an HTTP request (fast-fail), and a recovered peer
// closes the circuit via the half-open probe.
func TestRPC_FastFailOnOpenCircuit(t *testing.T) {
	resetPeerBreakers()
	defer resetPeerBreakers()
	old := peerBreakerCooldown
	peerBreakerCooldown = 20 * time.Millisecond
	defer func() { peerBreakerCooldown = old }()

	healthy := false
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !healthy {
			w.WriteHeader(http.StatusInternalServerError) // infra failure
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, httpClient: srv.Client()}
	var err error
	for i := 0; i < peerBreakerThreshold; i++ {
		err = c.rpc(context.Background(), "message/send", map[string]any{}, nil)
	}
	if err == nil {
		t.Fatal("expected error from failing peer")
	}
	hitsAfterFail := hits

	// Circuit is now OPEN: next rpc fast-fails without touching the server.
	rpcErr := c.rpc(context.Background(), "message/send", map[string]any{}, nil)
	var open *peerCircuitOpenError
	if !errors.As(rpcErr, &open) {
		t.Fatalf("expected peerCircuitOpenError, got %v", rpcErr)
	}
	if open.Peer != srv.URL || open.Method != "message/send" {
		t.Fatalf("circuit-open error must name peer and method, got %+v", open)
	}
	if hits != hitsAfterFail {
		t.Fatal("fast-fail must not issue an HTTP request")
	}

	// Peer recovers; cooldown expires: the next call is the half-open
	// probe, its success closes the circuit, and the call after that
	// flows normally.
	healthy = true
	time.Sleep(25 * time.Millisecond)
	if err := c.rpc(context.Background(), "message/send", map[string]any{}, nil); err != nil {
		t.Fatalf("half-open probe against recovered peer must succeed, got %v", err)
	}
	if peerBreakerFor(srv.URL).stateSnapshot() != peerBreakerClosed {
		t.Fatal("successful probe must close the circuit")
	}
	if err := c.rpc(context.Background(), "message/send", map[string]any{}, nil); err != nil {
		t.Fatalf("post-recovery call must succeed, got %v", err)
	}
}
