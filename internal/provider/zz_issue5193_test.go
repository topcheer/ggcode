package provider

import (
	"context"
	"errors"
	"testing"
)

// #5193: ChatStream reset semantics must match Chat's err==nil reset -
// a stream that opens cleanly and closes cleanly (even with zero output
// events) resets consecutiveFail. Only a DELIVERED error event keeps the
// counter. Scenario from the issue: 2 soft failures + 1 clean-empty
// stream used to leave count=2, so the next soft failure prematurely
// failed over a recovered primary.

func issue5193Provider(emptyOrEvents func() <-chan StreamEvent) *FallbackProvider {
	primary := &streamProducerProvider{
		mockProvider: mockProvider{name: "primary", chatErr: errors.New("soft transient")},
		streamFn: func(ctx context.Context) (<-chan StreamEvent, error) {
			return emptyOrEvents(), nil
		},
	}
	fallback := &mockProvider{name: "fallback"}
	return NewFallbackProvider(primary, fallback, "primary -> fallback")
}

func issue5193TwoSoftFailures(t *testing.T, fp *FallbackProvider) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, err := fp.Chat(context.Background(), nil, nil); err == nil {
			t.Fatalf("seed chat %d: expected soft failure", i)
		}
	}
	if fp.HasFailedOver() {
		t.Fatal("seed must not fail over (2 < threshold)")
	}
}

func TestIssue5193_EmptyStreamResetsCounter(t *testing.T) {
	ch := make(chan StreamEvent)
	close(ch) // clean close, zero output events
	fp := issue5193Provider(func() <-chan StreamEvent { return ch })

	issue5193TwoSoftFailures(t, fp)

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range stream { // drain; zero events expected
	}

	// Probe: another soft failure. Expected to fail; the question is
	// whether the counter was reset (no failover) or not (failover).
	_, _ = fp.Chat(context.Background(), nil, nil)
	if fp.HasFailedOver() {
		t.Fatal("clean-empty stream must reset consecutiveFail (premature failover)")
	}
}

func TestIssue5193_ErrorStreamKeepsCounter(t *testing.T) {
	ch := make(chan StreamEvent, 2)
	ch <- StreamEvent{Type: StreamEventError, Error: errors.New("stream boom")}
	close(ch)
	fp := issue5193Provider(func() <-chan StreamEvent { return ch })

	issue5193TwoSoftFailures(t, fp)

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range stream {
	}

	_, _ = fp.Chat(context.Background(), nil, nil)
	if !fp.HasFailedOver() {
		t.Fatal("delivered error event must keep the fail counter")
	}
}

func TestIssue5193_OutputStreamStillResets(t *testing.T) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamEventText, Text: "hello"}
	close(ch)
	fp := issue5193Provider(func() <-chan StreamEvent { return ch })

	issue5193TwoSoftFailures(t, fp)

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range stream {
	}

	_, _ = fp.Chat(context.Background(), nil, nil)
	if fp.HasFailedOver() {
		t.Fatal("clean stream with output must still reset (regression guard)")
	}
}
