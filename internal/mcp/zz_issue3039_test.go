package mcp

// Regression probes for #3039:
//   - B1: gate() may designate a call as the half-open probe; every early
//     return between gate() and the transport (read-only block, invalid
//     arguments) must release the probe - a leaked probing=true fast-failed
//     every later call until process restart.
//   - B2: a caller-side context.Canceled must neither recordSuccess (which
//     closed the breaker and pronounced a dead server recovered) nor leak
//     the half-open probe.

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

// TestIssue3039_B1_BlockedToolReleasesProbe: a read-only-blocked probe call
// must release the half-open probe - the NEXT call must be allowed as the
// new probe instead of hitting "already in flight".
func TestIssue3039_B1_BlockedToolReleasesProbe(t *testing.T) {
	b := newTestBreakerOpen(t, 50*time.Millisecond)
	blocked := &mcpTool{srvName: "s", toolName: "write_thing", breaker: b, blocked: true}
	// Cooldown must elapse FIRST so this Execute is the half-open probe;
	// during cooldown gate() fast-fails before the blocked branch is reached.
	time.Sleep(100 * time.Millisecond)
	res, err := blocked.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("blocked tool must return an error result")
	}
	// The leaked-probe bug: the very next gate() fast-failed with "a probe
	// call is already in flight" forever (we stay half-open). It must
	// instead allow the next call as the new probe, IMMEDIATELY - the
	// release is synchronous.
	if blockedNow, msg := b.gate(); blockedNow {
		t.Fatalf("#3039-B1: probe leaked - next call blocked: %s", msg)
	}
}

// TestIssue3039_B1_InvalidArgumentsReleaseProbe: same for the arg-parse
// early return.
func TestIssue3039_B1_InvalidArgumentsReleaseProbe(t *testing.T) {
	b := newTestBreakerOpen(t, 50*time.Millisecond)
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: nil}
	time.Sleep(100 * time.Millisecond) // first Execute becomes the probe
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

// fakeCaller cancels the transport call, simulating the user aborting.
type cancelingCaller struct{}

func (cancelingCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	return nil, context.Canceled
}

// TestIssue3039_B2_CanceledProbeDoesNotHeal: a canceled half-open probe must
// keep the breaker open (cooldown restarts) - recordSuccess would have
// declared the dead server recovered.
func TestIssue3039_B2_CanceledProbeDoesNotHeal(t *testing.T) {
	b := newTestBreakerOpen(t, 200*time.Millisecond)
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: cancelingCaller{}}
	time.Sleep(210 * time.Millisecond) // cooldown elapsed -> this call is the probe
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
	// And the probe must be released, not leaked: after the new cooldown the
	// next call is again allowed as a probe.
	time.Sleep(210 * time.Millisecond)
	if blockedNow, msg := b.gate(); blockedNow {
		t.Fatalf("#3039-B2: canceled probe leaked probing=true: %s", msg)
	}
}

// TestIssue3039_B2_CanceledClosedCallKeepsFailureCount: in the closed state
// a cancellation must not reset the consecutive-failure count.
func TestIssue3039_B2_CanceledClosedCallKeepsFailureCount(t *testing.T) {
	b := newServerBreaker("srv-count")
	b.recordFailure(errors.New("connection refused"))
	m := &mcpTool{srvName: "s", toolName: "thing", breaker: b, caller: cancelingCaller{}}
	if _, err := m.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	b.recordFailure(errors.New("connection refused"))
	b.recordFailure(errors.New("connection refused"))
	// If the cancel had reset the count, the breaker would still be closed
	// here; it must have opened after the 3rd total failure.
	if snap := b.snapshot(); snap.State != "open" {
		t.Fatalf("#3039-B2: cancel reset the failure count; expected open, got %v", snap.State)
	}
}

// Compile-time shape check for the release helper.
func TestIssue3039_AbandonProbeNoOpWhenClosed(t *testing.T) {
	b := newServerBreaker("srv-count")
	b.abandonProbe() // closed state: must be a safe no-op
	if snap := b.snapshot(); snap.State != "closed" {
		t.Fatalf("abandonProbe in closed state must not change state, got %v", snap.State)
	}
}

func init() { breakerCooldown = 80 * time.Millisecond }
