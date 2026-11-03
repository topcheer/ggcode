package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestBreakerOpen(t *testing.T, cooldown time.Duration) *serverBreaker {
	t.Helper()
	b := newServerBreaker("srv-probe")
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))
	return b
}

func TestIssue3039_B1_BlockedToolReleasesProbe(t *testing.T) {
	b := newTestBreakerOpen(t, 50*time.Millisecond)
	blocked := &mcpTool{srvName: "s", toolName: "write_thing", breaker: b, blocked: true}

	time.Sleep(100 * time.Millisecond)
	res, err := blocked.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("blocked tool must return an error result")
	}

	if blockedNow, msg := b.gate(); blockedNow {
		t.Fatalf("#3039-B1: probe leaked - next call blocked: %s", msg)
	}
}

func TestIssue3039_B1_InvalidArgumentsReleaseProbe(t *testing.T) {
	b := newTestBreakerOpen(t, 50*time.Millisecond)
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: nil}
	time.Sleep(100 * time.Millisecond)
	res, err := m.Execute(context.Background(), json.RawMessage(`{not-json`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "parsing tool arguments") {
		t.Fatalf("expected parse error result, got %+v", res)
	}
	if blockedNow, msg := b.gate(); blockedNow {
		t.Fatalf("#3039-B1: probe leaked via parse early-return: %s", msg)
	}
}

type cancelingCaller struct{}

func (cancelingCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return nil, context.Canceled
}

func TestIssue3039_B2_CanceledProbeDoesNotHeal(t *testing.T) {
	b := newTestBreakerOpen(t, 200*time.Millisecond)
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: cancelingCaller{}}
	time.Sleep(210 * time.Millisecond)
	res, err := m.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("canceled transport must surface an error result")
	}
	snap := b.snapshot()
	if snap.State == "closed" {
		t.Fatal("#3039-B2: a canceled probe healed the breaker - dead server pronounced recovered")
	}

	time.Sleep(210 * time.Millisecond)
	if blockedNow, msg := b.gate(); blockedNow {
		t.Fatalf("#3039-B2: canceled probe leaked probing=true: %s", msg)
	}
}

func TestIssue3039_B2_CanceledClosedCallKeepsFailureCount(t *testing.T) {
	b := newServerBreaker("srv-count")
	b.recordFailure(errors.New("connection refused"))
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: cancelingCaller{}}
	if _, err := m.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))

	if snap := b.snapshot(); snap.State != "open" {
		t.Fatalf("#3039-B2: cancel reset the failure count; expected open, got %v", snap.State)
	}
}

func TestIssue3039_AbandonProbeNoOpWhenClosed(t *testing.T) {
	b := newServerBreaker("srv-count")
	b.abandonProbe()
	if snap := b.snapshot(); snap.State != "closed" {
		t.Fatalf("abandonProbe in closed state must not change state, got %v", snap.State)
	}
}

func init() { breakerCooldown = 80 * time.Millisecond }
