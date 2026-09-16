package mcp

// sa-21: per-server MCP circuit breaker tests.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsInfraErrorClassification(t *testing.T) {
	infra := []string{
		"mcp[srv]: tools/call x: dial tcp 127.0.0.1:9999: connect: connection refused",
		"mcp[srv]: connection closed",
		"mcp[srv]: tools/call x: EOF",
		"mcp[srv]: tools/call x: read tcp ...: i/o timeout",
		"mcp[srv]: tools/call x: context deadline exceeded",
		"mcp[srv]: read goroutine did not return after abort: context canceled",
		"mcp[srv]: caller not connected (server may have crashed or not started)",
	}
	for _, m := range infra {
		if !isInfraError(errors.New(m)) {
			t.Errorf("expected INFRA: %q", m)
		}
	}
	semantic := []string{
		"mcp[srv]: tools/call query: Tool not found: query",
		"mcp[srv]: tools/call x: invalid arguments: missing param p",
		"mcp[srv]: context cancelled: context canceled", // user pressed Esc
		"mcp[srv]: tools/call x: permission denied for path /etc",
	}
	for _, m := range semantic {
		if isInfraError(errors.New(m)) {
			t.Errorf("expected SEMANTIC: %q", m)
		}
	}
	if isInfraError(nil) {
		t.Error("nil error must not be infra")
	}
}

func TestBreakerOpensAfterThresholdAndFastFails(t *testing.T) {
	b := newServerBreaker("srv")
	for i := 0; i < b.threshold; i++ {
		blocked, _ := b.gate()
		if blocked {
			t.Fatalf("iteration %d: circuit must be closed", i)
		}
		b.recordFailure(errors.New("connection refused"))
	}
	blocked, msg := b.gate()
	if !blocked {
		t.Fatal("circuit must be OPEN after threshold failures")
	}
	if !strings.Contains(msg, `"srv"`) || !strings.Contains(msg, "Do NOT retry") {
		t.Errorf("fast-fail message missing actionable guidance: %q", msg)
	}
}

func TestBreakerHalfOpenProbeThenClose(t *testing.T) {
	b := newServerBreaker("srv")
	b.cooldown = 10 * time.Millisecond
	for i := 0; i < b.threshold; i++ {
		b.recordFailure(errors.New("i/o timeout"))
	}
	if blocked, _ := b.gate(); !blocked {
		t.Fatal("expected OPEN")
	}
	time.Sleep(15 * time.Millisecond) // cooldown elapses
	if blocked, _ := b.gate(); blocked {
		t.Fatal("after cooldown the call must become the half-open probe")
	}
	// Concurrent callers while the probe is in flight are fast-failed.
	if blocked, msg := b.gate(); !blocked || !strings.Contains(msg, "probe") {
		t.Errorf("expected probe-in-flight fast-fail, got blocked=%v msg=%q", blocked, msg)
	}
	b.recordSuccess()
	if b.snapshot().State != "closed" {
		t.Fatalf("success after probe must close, got %s", b.snapshot().State)
	}
	if b.snapshot().Failure != 0 {
		t.Errorf("failure count must reset, got %d", b.snapshot().Failure)
	}
}

func TestBreakerProbeFailureReopens(t *testing.T) {
	b := newServerBreaker("srv")
	b.cooldown = 5 * time.Millisecond
	for i := 0; i < b.threshold; i++ {
		b.recordFailure(errors.New("connection reset"))
	}
	time.Sleep(10 * time.Millisecond)
	if blocked, _ := b.gate(); blocked {
		t.Fatal("probe must be allowed after cooldown")
	}
	b.recordFailure(errors.New("connection reset"))
	blocked, _ := b.gate()
	if !blocked {
		t.Fatal("failed probe must reopen the circuit")
	}
	// Fresh cooldown: the reopened circuit only unlocks after a FRESH
	// cooldown, not the original one (10ms elapsed here < 50ms fresh).
	b.cooldown = 50 * time.Millisecond
	time.Sleep(10 * time.Millisecond)
	blocked, msg := b.gate()
	if !blocked || !strings.Contains(msg, "OPEN") {
		t.Fatalf("reopened circuit must block until a fresh cooldown; got blocked=%v msg=%q", blocked, msg)
	}
}

func TestBreakerSuccessResetsCounter(t *testing.T) {
	b := newServerBreaker("srv")
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))
	b.recordSuccess()
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))
	if blocked, _ := b.gate(); blocked {
		t.Fatal("success must reset the consecutive-failure counter")
	}
}

func TestMCPTripWireAcrossSiblingTools(t *testing.T) {
	// Two tools of the SAME server share one breaker: trips on tool A must
	// fast-fail tool B without any transport attempt.
	var calls atomic.Int32
	failing := &countingCaller{calls: &calls, err: errors.New("connection refused")}
	ok := &countingCaller{calls: &calls}
	br := newServerBreaker("srv")

	ta := &mcpTool{name: "mcp__srv__a", caller: failing, toolName: "a", srvName: "srv", breaker: br}
	tb := &mcpTool{name: "mcp__srv__b", caller: ok, toolName: "b", srvName: "srv", breaker: br}

	for i := 0; i < br.threshold; i++ {
		res, _ := ta.Execute(context.Background(), json.RawMessage(`{}`))
		if !res.IsError {
			t.Fatalf("call %d must fail", i)
		}
	}
	if got := calls.Load(); got != int32(br.threshold) {
		t.Fatalf("expected exactly %d transport calls, got %d", br.threshold, got)
	}
	res, _ := tb.Execute(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("sibling tool must be fast-failed by the open breaker")
	}
	if calls.Load() != int32(br.threshold) {
		t.Fatalf("sibling fast-fail must NOT attempt transport; calls=%d", calls.Load())
	}
	if !strings.Contains(res.Content, "circuit breaker is OPEN") {
		t.Errorf("sibling fast-fail must carry breaker message, got: %q", res.Content)
	}
}

func TestMCPSemanticErrorsNeverTripBreaker(t *testing.T) {
	var calls atomic.Int32
	failing := &countingCaller{calls: &calls, err: errors.New("mcp[srv]: tools/call x: Tool not found: x")}
	br := newServerBreaker("srv")
	mt := &mcpTool{name: "mcp__srv__x", caller: failing, toolName: "x", srvName: "srv", breaker: br}
	for i := 0; i < br.threshold*2; i++ {
		if res, _ := mt.Execute(context.Background(), json.RawMessage(`{}`)); !res.IsError {
			t.Fatal("expected error result")
		}
	}
	if got := calls.Load(); got != int32(br.threshold*2) {
		t.Fatalf("semantic errors must always reach transport; calls=%d", got)
	}
	if br.snapshot().State != "closed" {
		t.Fatalf("semantic failures must not open the breaker, got %s", br.snapshot().State)
	}
}

func TestAdapterSharesBreakerAcrossRegisteredTools(t *testing.T) {
	adapter := NewAdapter("srv", nil, []ToolDefinition{
		{Name: "a"}, {Name: "b"},
	})
	if adapter.breaker == nil {
		t.Fatal("NewAdapter must create the per-server breaker")
	}
	adapter.breaker.threshold = 1
	adapter.breaker.recordFailure(errNotConnected{server: "srv"})
	blocked, msg := adapter.breaker.gate()
	if !blocked || !strings.Contains(msg, "OPEN") {
		t.Fatalf("expected open circuit, blocked=%v msg=%q", blocked, msg)
	}
}

func TestBreakerConcurrentGateDuringOpen(t *testing.T) {
	b := newServerBreaker("srv")
	for i := 0; i < b.threshold; i++ {
		b.recordFailure(errors.New("connection refused"))
	}
	var wg sync.WaitGroup
	var fastFailed atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if blocked, _ := b.gate(); blocked {
				fastFailed.Add(1)
			}
		}()
	}
	wg.Wait()
	if fastFailed.Load() != 50 {
		t.Fatalf("all 50 concurrent gates must fast-fail while OPEN, got %d", fastFailed.Load())
	}
	if b.snapshot().State != "open" {
		t.Fatalf("state must remain open, got %s", b.snapshot().State)
	}
}

func TestBreakerCloneSharesBreaker(t *testing.T) {
	br := newServerBreaker("srv")
	mt := &mcpTool{name: "mcp__srv__a", caller: nil, toolName: "a", srvName: "srv", breaker: br}
	c, ok := mt.Clone().(*mcpTool)
	if !ok {
		t.Fatal("Clone must return *mcpTool")
	}
	if c.breaker != br {
		t.Fatal("Clone must share the per-server breaker pointer")
	}
	// nil-caller is infra and feeds the shared breaker.
	for i := 0; i < br.threshold; i++ {
		c.Execute(context.Background(), json.RawMessage(`{}`))
	}
	if br.snapshot().State != "open" {
		t.Fatalf("nil-caller failures must trip the breaker, got %s", br.snapshot().State)
	}
	res, _ := mt.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(res.Content, "circuit breaker is OPEN") {
		t.Errorf("original tool must also fast-fail, got: %q", res.Content)
	}
}

// countingCaller is a toolCaller that counts transport attempts.
type countingCaller struct {
	calls  *atomic.Int32
	err    error
	result *CallToolResult
	onCall func()
}

func (c *countingCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	c.calls.Add(1)
	if c.onCall != nil {
		c.onCall()
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.result != nil {
		return c.result, nil
	}
	return &CallToolResult{}, nil
}
