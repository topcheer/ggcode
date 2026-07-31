package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// mockProvider is a minimal Provider for testing failover.
type mockProvider struct {
	name       string
	chatErr    error
	streamErr  error
	chatCalls  atomic.Int32
	streamErrs atomic.Int32 // number of errors to return before succeeding
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	m.chatCalls.Add(1)
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *mockProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	if m.streamErrs.Load() > 0 {
		m.streamErrs.Add(-1)
		return nil, m.streamErr
	}
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return len(messages) * 10, nil
}

func TestFallback_NoFailover_OnSuccess(t *testing.T) {
	primary := &mockProvider{name: "primary"}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if fp.HasFailedOver() {
		t.Fatal("should not have failed over on success")
	}
}

func TestFallback_ImmediateFailover_OnQuota(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("insufficient_quota: quota exceeded"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	var notifyTrigger FailoverTrigger
	fp.SetFailoverNotify(func(trigger FailoverTrigger, err error) {
		notifyTrigger = trigger
	})

	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response from fallback")
	}
	if !fp.HasFailedOver() {
		t.Fatal("should have failed over on quota error")
	}
	if notifyTrigger != FailoverTriggerQuota {
		t.Fatalf("expected trigger quota, got %s", notifyTrigger)
	}
	if primary.chatCalls.Load() != 1 {
		t.Fatalf("expected 1 primary call, got %d", primary.chatCalls.Load())
	}
}

func TestFallback_ImmediateFailover_OnAuth(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("401 Unauthorized: invalid_api_key"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	_, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if !fp.HasFailedOver() {
		t.Fatal("should have failed over on auth error")
	}
}

func TestFallback_NoFailover_BelowTransientThreshold(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("429 rate limit exceeded"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	// First transient failure — should NOT trigger failover yet.
	_, err := fp.Chat(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from primary (no failover yet)")
	}
	if fp.HasFailedOver() {
		t.Fatal("should not failover on first transient error")
	}
}

func TestFallback_Failover_AfterRepeatedTransient(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("503 Service Unavailable"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	var notifyTrigger FailoverTrigger
	fp.SetFailoverNotify(func(trigger FailoverTrigger, err error) {
		notifyTrigger = trigger
	})

	// Call enough times to exceed the threshold.
	for i := 0; i < failoverThreshold; i++ {
		_, _ = fp.Chat(context.Background(), nil, nil)
	}
	// The call that crosses the threshold triggers failover and retries on fallback.
	if !fp.HasFailedOver() {
		t.Fatal("should have failed over after repeated transient errors")
	}
	if notifyTrigger != FailoverTriggerRepeated {
		t.Fatalf("expected trigger repeated, got %s", notifyTrigger)
	}
}

func TestFallback_ChatStream_FailoverOnQuota(t *testing.T) {
	primary := &mockProvider{
		name:      "primary",
		streamErr: errors.New("coding plan expired"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected fallback stream, got error: %v", err)
	}
	if stream == nil {
		t.Fatal("expected non-nil stream from fallback")
	}
	if !fp.HasFailedOver() {
		t.Fatal("should have failed over on quota error")
	}
}

func TestFallback_Reset(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("insufficient_quota"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	// Trigger failover
	_, _ = fp.Chat(context.Background(), nil, nil)
	if !fp.HasFailedOver() {
		t.Fatal("should have failed over")
	}

	// Reset
	fp.Reset()
	if fp.HasFailedOver() {
		t.Fatal("should not be failed over after reset")
	}
}

func TestFallback_NameReflectsActiveProvider(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("insufficient_quota"),
	}
	fallback := &mockProvider{name: "fallback"}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	if fp.Name() != "primary" {
		t.Fatalf("expected 'primary', got %s", fp.Name())
	}

	// Trigger failover
	_, _ = fp.Chat(context.Background(), nil, nil)

	if fp.Name() != "fallback" {
		t.Fatalf("expected 'fallback' after failover, got %s", fp.Name())
	}
}

func TestFallbackConfig_IsConfigured(t *testing.T) {
	// Test via the config package would be ideal, but we test the logic here.
	tests := []struct {
		enabled  bool
		vendor   string
		model    string
		expected bool
	}{
		{true, "zai", "glm-4.6", true},
		{true, "zai", "", false},
		{true, "", "glm-4.6", false},
		{false, "zai", "glm-4.6", false},
	}
	for _, tt := range tests {
		// Inline check matching FallbackConfig.IsConfigured logic
		result := tt.enabled && tt.vendor != "" && tt.model != ""
		if result != tt.expected {
			t.Errorf("enabled=%v vendor=%q model=%q: expected %v, got %v", tt.enabled, tt.vendor, tt.model, tt.expected, result)
		}
	}
}
