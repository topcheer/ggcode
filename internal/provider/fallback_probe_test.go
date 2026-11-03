package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestFallback_ProactivePrimaryRecovery(t *testing.T) {

	primary := &failingNProvider{name: "primary", err: errors.New("insufficient_quota: quota exceeded")}
	primary.failN.Store(2)
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")
	fp.probeInterval = 10 * time.Millisecond

	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil || resp == nil {
		t.Fatalf("triggering call must succeed via the fallback retry: %v", err)
	}
	if fp.activeIdx.Load() != 1 {
		t.Fatal("quota failure must activate failover")
	}

	deadline := time.Now().Add(3 * time.Second)
	for fp.activeIdx.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fp.HasFailedOver() {
		t.Fatal("prober must switch back to the recovered primary")
	}

	resp, err = fp.Chat(context.Background(), nil, nil)
	if err != nil || resp == nil {
		t.Fatalf("expected primary to serve traffic after recovery: %v", err)
	}
	if got := primary.calls.Load(); got < 3 {
		t.Fatalf("expected >=3 primary calls (initial + probes), got %d", got)
	}
}

func TestFallback_ProberStopsWhenNotFailedOver(t *testing.T) {
	primary := &mockProvider{name: "primary"}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")
	fp.probeInterval = 10 * time.Millisecond

	fp.mu.Lock()
	fp.activeIdx.Store(1)
	fp.startRecoveryProberLocked()
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
