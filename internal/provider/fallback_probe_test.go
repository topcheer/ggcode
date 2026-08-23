package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// failingNProvider succeeds only after its error has been returned N times -
// simulates a primary whose quota window resets / outage ends.
type failingNProvider struct {
	name  string
	err   error
	failN atomic.Int32
	calls atomic.Int32
}

func (m *failingNProvider) Name() string { return m.name }
func (m *failingNProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	if int(m.calls.Add(1)) <= int(m.failN.Load()) {
		return nil, m.err
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *failingNProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *failingNProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}

// TestFallback_ProactivePrimaryRecovery verifies that while the fallback is
// active, a background prober checks the primary and switches back as soon
// as it responds successfully - without waiting for the fallback to fail.
func TestFallback_ProactivePrimaryRecovery(t *testing.T) {
	// Primary fails twice (quota), then recovers: 1 call from the initial
	// failed attempt + 1 failed probe, subsequent probes succeed.
	primary := &failingNProvider{name: "primary", err: errors.New("insufficient_quota: quota exceeded")}
	primary.failN.Store(2)
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")
	fp.probeInterval = 10 * time.Millisecond

	// Trigger failover via a real Chat call on the primary. The call itself
	// SUCCEEDS - the fallback serves the retry - but failover activates.
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil || resp == nil {
		t.Fatalf("triggering call must succeed via the fallback retry: %v", err)
	}
	if !fp.HasFailedOver() {
		t.Fatal("quota failure must activate failover")
	}

	// Wait for the prober to notice the recovered primary and switch back.
	deadline := time.Now().Add(3 * time.Second)
	for fp.HasFailedOver() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fp.HasFailedOver() {
		t.Fatal("prober must switch back to a recovered primary")
	}

	// Subsequent traffic flows through the primary again.
	resp, err = fp.Chat(context.Background(), nil, nil)
	if err != nil || resp == nil {
		t.Fatalf("expected primary to serve traffic after recovery: %v", err)
	}
	if got := primary.calls.Load(); got < 3 {
		t.Fatalf("expected >=3 primary calls (initial + probes), got %d", got)
	}
}

// TestFallback_ProberStopsWhenNotFailedOver ensures no probe traffic runs in
// the normal (not failed-over) state - the prober is lazy, started only on
// failover activation and retired on every switch-back path.
func TestFallback_ProberStopsWhenNotFailedOver(t *testing.T) {
	primary := &mockProvider{name: "primary"}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")
	fp.probeInterval = 10 * time.Millisecond

	// Manual Reset() after activation must also retire the prober.
	fp.mu.Lock()
	fp.failedOver.Store(true)
	fp.startPrimaryProberLocked()
	fp.mu.Unlock()
	fp.Reset()
	if fp.HasFailedOver() {
		t.Fatal("Reset must clear failover state")
	}

	before := primary.chatCalls.Load()
	time.Sleep(80 * time.Millisecond)
	if after := primary.chatCalls.Load(); after != before {
		t.Fatalf("no probe calls expected after Reset, got %d extra", after-before)
	}
}
