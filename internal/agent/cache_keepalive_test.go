package agent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCacheKeepalive_BasicLifecycle(t *testing.T) {
	k := newCacheKeepaliveState()

	// Initially not active
	if k.isActive() {
		t.Fatal("keepalive should not be active initially")
	}

	// Stop when not started should be a no-op
	k.stopIdle()
	if k.isActive() {
		t.Fatal("keepalive should not be active after stopIdle on unstarted state")
	}
}

func TestCacheKeepalive_NilProviderNoOp(t *testing.T) {
	k := newCacheKeepaliveState()
	// startIdle with nil provider should be a no-op
	k.startIdle(nil, nil, nil)
	if k.isActive() {
		t.Fatal("keepalive should not start with nil provider")
	}
}

func TestCacheKeepalive_NonAnthropicProviderNoOp(t *testing.T) {
	k := newCacheKeepaliveState()

	p := &mockKeepaliveProvider{name: "openai"}
	k.startIdle(p, nil, nil)
	if k.isActive() {
		t.Fatal("keepalive should not start for non-Anthropic provider")
	}
}

func TestCacheKeepalive_AnthropicProviderStarts(t *testing.T) {
	k := newCacheKeepaliveState()
	p := &mockKeepaliveProvider{name: "anthropic"}

	k.startIdle(p, nil, nil)
	if !k.isActive() {
		t.Fatal("keepalive should be active for Anthropic provider")
	}

	k.stopIdle()
	if k.isActive() {
		t.Fatal("keepalive should not be active after stopIdle")
	}
	if k.touchCount() != 0 {
		t.Fatal("touch count should be reset to 0 after stopIdle")
	}
}

func TestCacheKeepalive_MaxTouchesPreventsScheduling(t *testing.T) {
	k := newCacheKeepaliveState()
	p := &mockKeepaliveProvider{name: "anthropic"}

	k.startIdle(p, nil, nil)
	if !k.isActive() {
		t.Fatal("keepalive should be active for Anthropic provider")
	}

	// Manually set touches to max to test the cap
	k.mu.Lock()
	k.touches = cacheKeepaliveMaxTouches
	k.mu.Unlock()

	if k.touchCount() != cacheKeepaliveMaxTouches {
		t.Fatalf("expected %d touches, got %d", cacheKeepaliveMaxTouches, k.touchCount())
	}

	k.stopIdle()
}

func TestCacheKeepalive_ConstantsCorrect(t *testing.T) {
	if cacheKeepaliveInterval != 270*time.Second {
		t.Fatalf("expected 270s interval, got %v", cacheKeepaliveInterval)
	}
	if cacheKeepaliveMaxTouches != 12 {
		t.Fatalf("expected 12 max touches, got %d", cacheKeepaliveMaxTouches)
	}
}

// mockKeepaliveProvider is a minimal provider for testing cache keepalive.
type mockKeepaliveProvider struct {
	name string
}

func (m *mockKeepaliveProvider) Name() string { return m.name }
func (m *mockKeepaliveProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}
func (m *mockKeepaliveProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockKeepaliveProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}
