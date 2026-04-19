package im

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- sendWithTimeout tests ---

func TestSendWithTimeout_Success(t *testing.T) {
	sink := &stubSink{name: "test"}
	binding := ChannelBinding{Adapter: "test", ChannelID: "ch1"}
	event := OutboundEvent{Kind: OutboundEventText, Text: "hello"}

	err := sendWithTimeout(context.Background(), sink, binding, event)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
}

func TestSendWithTimeout_RetriesOnce(t *testing.T) {
	var calls atomic.Int32
	sink := &retrySink{fn: func() error {
		n := calls.Add(1)
		if n == 1 {
			return errors.New("transient")
		}
		return nil
	}}
	binding := ChannelBinding{Adapter: "test", ChannelID: "ch1"}
	event := OutboundEvent{Kind: OutboundEventText, Text: "hello"}

	err := sendWithTimeout(context.Background(), sink, binding, event)
	if err != nil {
		t.Fatalf("expected nil error after retry, got %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls (1 initial + 1 retry), got %d", calls.Load())
	}
}

func TestSendWithTimeout_BothAttemptsFail(t *testing.T) {
	sink := &stubSink{name: "test", err: errors.New("permanent")}
	binding := ChannelBinding{Adapter: "test", ChannelID: "ch1"}
	event := OutboundEvent{Kind: OutboundEventText, Text: "hello"}

	err := sendWithTimeout(context.Background(), sink, binding, event)
	if err == nil {
		t.Fatal("expected error when both attempts fail")
	}
}

func TestSendWithTimeout_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	sink := &blockSink{unblock: make(chan struct{})}
	binding := ChannelBinding{Adapter: "test", ChannelID: "ch1"}
	event := OutboundEvent{Kind: OutboundEventText, Text: "hello"}

	err := sendWithTimeout(ctx, sink, binding, event)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- fanOutSend tests ---

func TestFanOutSend_ParallelDelivery(t *testing.T) {
	var (
		mu       sync.Mutex
		order    []string
		unblockA = make(chan struct{})
		unblockB = make(chan struct{})
	)

	sinkA := &callbackSink{name: "a", fn: func() error {
		<-unblockA
		mu.Lock()
		order = append(order, "a")
		mu.Unlock()
		return nil
	}}
	sinkB := &callbackSink{name: "b", fn: func() error {
		<-unblockB
		mu.Lock()
		order = append(order, "b")
		mu.Unlock()
		return nil
	}}

	targets := []emitTarget{
		{binding: ChannelBinding{Adapter: "a", ChannelID: "ch1"}, sink: sinkA},
		{binding: ChannelBinding{Adapter: "b", ChannelID: "ch2"}, sink: sinkB},
	}

	done := make(chan error, 1)
	go func() {
		done <- fanOutSend(context.Background(), targets, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	}()

	// Unblock B first — if parallel, B can finish before A
	unblockB <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	unblockA <- struct{}{}

	if err := <-done; err != nil {
		t.Fatalf("fanOutSend returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// B should appear before A since we unblocked B first
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("expected parallel execution [b, a], got %v", order)
	}
}

func TestFanOutSend_SetsCreatedAt(t *testing.T) {
	sink := &stubSink{name: "test"}
	targets := []emitTarget{
		{binding: ChannelBinding{Adapter: "test", ChannelID: "ch1"}, sink: sink},
	}
	event := OutboundEvent{Kind: OutboundEventText, Text: "hi"}

	if err := fanOutSend(context.Background(), targets, event); err != nil {
		t.Fatal(err)
	}
	if sink.events[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestFanOutSend_FirstErrorReturned(t *testing.T) {
	sinkA := &stubSink{name: "a", err: errors.New("fail-a")}
	sinkB := &stubSink{name: "b"}

	targets := []emitTarget{
		{binding: ChannelBinding{Adapter: "a", ChannelID: "ch1"}, sink: sinkA},
		{binding: ChannelBinding{Adapter: "b", ChannelID: "ch2"}, sink: sinkB},
	}

	err := fanOutSend(context.Background(), targets, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err == nil {
		t.Fatal("expected error from failed sink")
	}
	if err == nil || !strings.Contains(err.Error(), "fail-a") {
		t.Fatalf("expected fail-a error, got %v", err)
	}
}

func TestFanOutSend_EmptyTargets(t *testing.T) {
	err := fanOutSend(context.Background(), nil, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err != nil {
		t.Fatalf("expected nil for empty targets, got %v", err)
	}
}

func TestFanOutSend_TimeoutOnSlowSink(t *testing.T) {
	// Use a short-lived context to keep the test fast.
	// The real defaultSendTimeout is 10s, but we verify the timeout mechanism works.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sink := &blockSink{unblock: make(chan struct{})}
	targets := []emitTarget{
		{binding: ChannelBinding{Adapter: "slow", ChannelID: "ch1"}, sink: sink},
	}

	err := fanOutSend(ctx, targets, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// --- non-blocking enqueue tests ---

func TestEnqueue_NonBlocking(t *testing.T) {
	mgr, sink := newManagerWithBinding(t)

	s := newIMEmitterState()

	// Block the consumer goroutine so the channel fills up.
	// We make sink.Send block until we unblock it.
	unblock := make(chan struct{})
	sink.sendBlock = unblock

	// Send enough events to fill the channel (capacity 256).
	// The consumer goroutine will be stuck on sink.Send for the first event,
	// so subsequent events pile up in the channel.
	for i := 0; i < 256; i++ {
		event := OutboundEvent{Kind: OutboundEventText, Text: "fill"}
		s.enqueue(mgr, event, "")
	}

	// The 257th event should be dropped (non-blocking), not hang the test.
	done := make(chan struct{})
	go func() {
		s.enqueue(mgr, OutboundEvent{Kind: OutboundEventStatus, Status: "overflow"}, "")
		close(done)
	}()

	select {
	case <-done:
		// correct: enqueue returned immediately (dropped)
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked when channel was full — non-blocking write broken")
	}

	// Unblock the consumer so the goroutine can drain
	close(unblock)
}

func TestEnqueue_NormalDelivery(t *testing.T) {
	mgr, sink := newManagerWithBinding(t)

	s := newIMEmitterState()

	event := OutboundEvent{Kind: OutboundEventText, Text: "normal"}
	s.enqueue(mgr, event, "")

	// Wait for the consumer goroutine to process
	deadline := time.After(2 * time.Second)
	for {
		sink.mu.Lock()
		n := len(sink.events)
		sink.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for event delivery")
		case <-time.After(50 * time.Millisecond):
		}
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.events[0].Text != "normal" {
		t.Fatalf("expected 'normal', got %q", sink.events[0].Text)
	}
}

// newManagerWithBinding creates a Manager with a binding and a blockingStubSink registered.
func newManagerWithBinding(t *testing.T) (*Manager, *blockingStubSink) {
	t.Helper()
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{Workspace: "/ws1"})
	mgr.BindChannel(ChannelBinding{Workspace: "/ws1", Adapter: "qq", TargetID: "t1", ChannelID: "c1"})
	sink := &blockingStubSink{name: "qq"}
	mgr.RegisterSink(sink)
	return mgr, sink
}

// --- test helpers ---

// retrySink calls fn on each Send, for controlling retry behavior.
type retrySink struct {
	name string
	fn   func() error
}

func (s *retrySink) Name() string { return s.name }
func (s *retrySink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return s.fn()
}

// callbackSink calls fn on Send, for controlling timing/ordering.
type callbackSink struct {
	name string
	fn   func() error
}

func (s *callbackSink) Name() string { return s.name }
func (s *callbackSink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return s.fn()
}

// blockSink blocks on Send until unblock channel is closed or receives.
type blockSink struct {
	unblock chan struct{}
}

func (s *blockSink) Name() string { return "block" }
func (s *blockSink) Send(ctx context.Context, _ ChannelBinding, _ OutboundEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.unblock:
		return nil
	}
}

// blockingStubSink extends stubSink with an optional sendBlock channel.
// When sendBlock is non-nil, Send blocks on it before recording the event.
type blockingStubSink struct {
	name      string
	mu        sync.Mutex
	events    []OutboundEvent
	sendBlock chan struct{} // if non-nil, blocks on each Send until closed/signaled
}

func (s *blockingStubSink) Name() string { return s.name }
func (s *blockingStubSink) Send(_ context.Context, _ ChannelBinding, event OutboundEvent) error {
	if s.sendBlock != nil {
		<-s.sendBlock
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}
