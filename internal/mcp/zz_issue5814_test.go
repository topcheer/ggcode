package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// ---- stdio helper server (spawned as a re-run of this test binary) ----
// Modes (env GGCODE_MCP5814_MODE):
//
//	alive     - answers everything, including ping
//	noping    - answers ping with JSON-RPC -32601 (ping unsupported)
//	stallping - answers discovery normally, never answers ping (half-dead pipe)

func TestIssue5814StdioHelper(t *testing.T) {
	if os.Getenv("GGCODE_MCP5814_HELPER") != "1" {
		return
	}
	mode := os.Getenv("GGCODE_MCP5814_MODE")
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": true}},
					"serverInfo":      map[string]interface{}{"name": "helper5814", "version": "1.0.0"},
				},
			})
		case "ping":
			if mode == "stallping" {
				select {} // half-dead: process alive, pipe never answers
			}
			if mode == "noping" {
				_ = enc.Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
				})
				continue
			}
			_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{}})
		case "tools/list":
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"tools": []interface{}{map[string]interface{}{
						"name":        "echo",
						"description": "echo tool",
						"inputSchema": map[string]interface{}{"type": "object"},
					}},
				},
			})
		case "prompts/list":
			_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{"prompts": []interface{}{}}})
		case "resources/list":
			_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{"resources": []interface{}{}}})
		case "resources/templates/list":
			_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{"resourceTemplates": []interface{}{}}})
		default:
			_ = enc.Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
		}
	}
}

func issue5814StartClient(t *testing.T, mode string) *Client {
	t.Helper()
	client := NewClient("helper5814", os.Args[0], []string{"-test.run=TestIssue5814StdioHelper"})
	client.skipOSVCheck = true
	client.env = map[string]string{
		"GGCODE_MCP5814_HELPER": "1",
		"GGCODE_MCP5814_MODE":   mode,
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize helper: %v", err)
	}
	return client
}

func TestIssue5814ProbePingCapableServer(t *testing.T) {
	client := issue5814StartClient(t, "alive")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		t.Fatalf("probe against ping-capable server must succeed: %v", err)
	}
	if client.LastHealthyAt().IsZero() {
		t.Fatal("probe success must refresh the liveness lease")
	}
	if got := client.probePref.Load(); got != probePrefPing {
		t.Fatalf("probe must cache the ping preference, got %d", got)
	}
	// A second probe must go straight through ping.
	if err := client.Probe(ctx); err != nil {
		t.Fatalf("second probe failed: %v", err)
	}
}

func TestIssue5814ProbeFallsBackWhenPingUnsupported(t *testing.T) {
	client := issue5814StartClient(t, "noping")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		t.Fatalf("probe against ping-unsupported server must succeed via round-trip evidence: %v", err)
	}
	if got := client.probePref.Load(); got != probePrefTools {
		t.Fatalf("probe must cache the tools/list fallback, got %d", got)
	}
	if err := client.Probe(ctx); err != nil {
		t.Fatalf("second probe (tools/list path) failed: %v", err)
	}
}

func TestIssue5814ProbeDetectsHalfDeadPipe(t *testing.T) {
	client := issue5814StartClient(t, "stallping")
	healthyAt := client.LastHealthyAt()
	if healthyAt.IsZero() {
		t.Fatal("initialize round-trip must have armed the lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := client.Probe(ctx)
	if err == nil {
		t.Fatal("probe against a stalled pipe must fail, not hang to the default request timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("probe must fail fast on a stalled pipe, took %v", elapsed)
	}
	if !client.LastHealthyAt().Equal(healthyAt) {
		t.Fatal("failed probe must not refresh the liveness lease")
	}
}

func TestIssue5814LivenessLeaseSemantics(t *testing.T) {
	client := NewClient("lease", "x", nil)
	if _, _, expired := client.LivenessLeaseState(); expired {
		t.Fatal("no lease configured must never report expired")
	}
	if !client.LastHealthyAt().IsZero() {
		t.Fatal("fresh client must have zero LastHealthyAt")
	}
	client.SetLivenessLease(50 * time.Millisecond)
	if _, _, expired := client.LivenessLeaseState(); expired {
		t.Fatal("no round-trip yet must not report expired (no evidence, no verdict)")
	}
	client.markHealthy()
	if _, maxAge, expired := client.LivenessLeaseState(); expired || maxAge != 50*time.Millisecond {
		t.Fatalf("fresh round-trip within lease must not expire (maxAge=%v expired=%v)", maxAge, expired)
	}
	time.Sleep(60 * time.Millisecond)
	if _, _, expired := client.LivenessLeaseState(); !expired {
		t.Fatal("stale round-trip beyond the lease must report expired")
	}
	client.SetLivenessLease(0)
	if _, _, expired := client.LivenessLeaseState(); expired {
		t.Fatal("lease disabled must never report expired")
	}
}

func TestIssue5814BreakerOnOpenHook(t *testing.T) {
	b := newServerBreaker("srv5814")
	var fired int32
	b.SetOnOpen(func() { atomic.AddInt32(&fired, 1) })
	infra := errors.New("connection closed by peer")
	b.recordFailure(infra)
	b.recordFailure(infra)
	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Fatalf("onOpen must not fire below threshold, fired %d", got)
	}
	b.recordFailure(infra)
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("onOpen must fire exactly once on OPEN, fired %d", got)
	}
	// Reopening through a failed half-open probe must fire again.
	b.mu.Lock()
	b.state = breakerHalfOpen
	b.probing = true
	b.mu.Unlock()
	b.recordFailure(infra)
	if got := atomic.LoadInt32(&fired); got != 2 {
		t.Fatalf("onOpen must fire again on REOPEN, fired %d", got)
	}
	// The breaker must now be OPEN (cooldown gate fast-fails).
	if blocked, _ := b.gate(); !blocked {
		t.Fatal("breaker must be OPEN after reopen")
	}
}
