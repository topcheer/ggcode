package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// countProvider fails permanently (quota) but counts calls.
type countProvider struct {
	name  string
	err   error
	calls atomic.Int32
}

func (m *countProvider) Name() string { return m.name }
func (m *countProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *countProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *countProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}

// recoverableProvider fails while failing is set, succeeds after Clear.
type recoverableProvider struct {
	name    string
	failing atomic.Bool
	calls   atomic.Int32
}

func (m *recoverableProvider) Name() string { return m.name }
func (m *recoverableProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	m.calls.Add(1)
	if m.failing.Load() {
		return nil, errors.New("insufficient_quota: quota exceeded")
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *recoverableProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	if m.failing.Load() {
		return nil, errors.New("insufficient_quota: quota exceeded")
	}
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *recoverableProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 1, nil
}
func (m *recoverableProvider) Clear() { m.failing.Store(false) }

// TestCascade_MultiLevelFailover: primary and fb1 both hard-down -> traffic
// advances through the chain and is served by fb2; the active index is 2.
func TestCascade_MultiLevelFailover(t *testing.T) {
	primary := &countProvider{name: "primary", err: errors.New("insufficient_quota: quota exceeded")}
	fb1 := &countProvider{name: "fb1", err: errors.New("invalid_api_key: bad key")}
	fb2 := &mockProvider{name: "fb2"}
	fp := NewCascadeProvider([]Provider{primary, fb1, fb2}, "p -> fb1 -> fb2")

	// Call 1: primary fails -> retry on fb1 fails -> error returned (one
	// advancement per call). Active is now fb1.
	_, err := fp.Chat(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("both sides failing must surface an error on the first call")
	}
	if got := fp.activeIdx.Load(); got != 1 {
		t.Fatalf("after primary quota failure active must be fb1 (1), got %d", got)
	}

	// Call 2: fb1 fails (auth) -> retry succeeds on fb2.
	resp, err := fp.Chat(context.Background(), nil, nil)
	if err != nil || resp == nil {
		t.Fatalf("fb2 must serve after primary+fb1 both fail: %v", err)
	}
	if got := fp.activeIdx.Load(); got != 2 {
		t.Fatalf("active must be fb2 (2), got %d", got)
	}
}

// TestCascade_WraparoundToPrimary: the LAST level failing hard wraps the
// active back to the primary (#936 generalization).
func TestCascade_WraparoundToPrimary(t *testing.T) {
	primary := &mockProvider{name: "primary"}
	fb1 := &countProvider{name: "fb1", err: errors.New("insufficient_quota: quota exceeded")}
	fp := NewCascadeProvider([]Provider{primary, fb1}, "p -> fb1")
	fp.activeIdx.Store(1) // already failed over to fb1

	_, err := fp.Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("primary must serve after fb1 hard-fails: %v", err)
	}
	if got := fp.activeIdx.Load(); got != 0 {
		t.Fatalf("wrap-around must return active to primary (0), got %d", got)
	}
}

// TestCascade_RecoveryPrefersPrimary: while running on the LAST level, the
// recovery prober must return straight to the PRIMARY when it recovers -
// not linger on intermediate fb1 (user requirement: primary-first recovery).
func TestCascade_RecoveryPrefersPrimary(t *testing.T) {
	primary := &recoverableProvider{name: "primary"}
	primary.failing.Store(true)
	fb1 := &recoverableProvider{name: "fb1"}
	fb1.failing.Store(true) // also down initially -> chain lands on fb2
	fb2 := &mockProvider{name: "fb2"}

	fp := NewCascadeProvider([]Provider{primary, fb1, fb2}, "p -> fb1 -> fb2")
	fp.probeInterval = 10 * time.Millisecond

	// Drive the chain to level 2.
	for i := 0; i < 4 && fp.activeIdx.Load() != 2; i++ {
		fp.Chat(context.Background(), nil, nil)
	}
	if got := fp.activeIdx.Load(); got != 2 {
		t.Fatalf("expected chain to reach fb2 (2), got %d", got)
	}

	// Both higher levels recover simultaneously.
	fb1.Clear()
	primary.Clear()

	// Prober must land on the PRIMARY (0) even though fb1 also recovered.
	deadline := time.Now().Add(3 * time.Second)
	for fp.activeIdx.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fp.activeIdx.Load(); got != 0 {
		t.Fatalf("recovery must prefer the primary (0), still at %d", got)
	}
	if primary.calls.Load() == 0 {
		t.Fatal("prober must have probed the primary")
	}
}

// TestCascade_RecoverySkipsStillDown: when the primary is still down but an
// intermediate level recovers, the prober advances to that level - and
// later upgrades to the primary once it recovers too.
func TestCascade_RecoverySkipsStillDown(t *testing.T) {
	primary := &recoverableProvider{name: "primary"}
	primary.failing.Store(true) // stays down
	fb1 := &recoverableProvider{name: "fb1"}
	fb1.failing.Store(false)
	fb2 := &mockProvider{name: "fb2"}

	fp := NewCascadeProvider([]Provider{primary, fb1, fb2}, "p -> fb1 -> fb2")
	fp.probeInterval = 10 * time.Millisecond
	fp.activeIdx.Store(2)
	fp.mu.Lock()
	fp.startRecoveryProberLocked()
	fp.mu.Unlock()

	// fb1 healthy, primary down -> land on fb1 (1).
	deadline := time.Now().Add(3 * time.Second)
	for fp.activeIdx.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fp.activeIdx.Load(); got != 1 {
		t.Fatalf("expected intermediate recovery to fb1 (1), got %d", got)
	}

	// Primary recovers -> prober (still running on level != 0) upgrades to 0.
	primary.Clear()
	deadline = time.Now().Add(3 * time.Second)
	for fp.activeIdx.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fp.activeIdx.Load(); got != 0 {
		t.Fatalf("expected later upgrade to primary (0), got %d", got)
	}
}
