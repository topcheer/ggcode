package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type asyncBrokenStreamProvider struct {
	name  string
	calls atomic.Int32
}

func (p *asyncBrokenStreamProvider) Name() string { return p.name }
func (p *asyncBrokenStreamProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return nil, errors.New("not implemented")
}
func (p *asyncBrokenStreamProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	p.calls.Add(1)
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamEventError, Error: errors.New("insufficient_quota: quota exceeded (async #" + p.name + ")")}
	close(ch)
	return ch, nil
}
func (p *asyncBrokenStreamProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 0, nil
}

type okStreamProvider struct {
	name  string
	calls atomic.Int32
}

func (p *okStreamProvider) Name() string { return p.name }
func (p *okStreamProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (p *okStreamProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	p.calls.Add(1)
	ch := make(chan StreamEvent, 2)
	ch <- StreamEvent{Type: StreamEventText, Text: "hello from " + p.name}
	close(ch)
	return ch, nil
}
func (p *okStreamProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 0, nil
}

func collectStream1244(t *testing.T, stream <-chan StreamEvent) (texts []string, lastErr error, systemNotices int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				return texts, lastErr, systemNotices
			}
			switch ev.Type {
			case StreamEventText:
				texts = append(texts, ev.Text)
			case StreamEventError:
				lastErr = ev.Error
			case StreamEventSystem:
				systemNotices++
			}
		case <-deadline:
			t.Fatal("stream did not terminate within 5s (infinite failover recursion?)")
			return
		}
	}
}

func TestFallback_AsyncDoubleFailureChainsToThirdProvider(t *testing.T) {
	a := &asyncBrokenStreamProvider{name: "A"}
	b := &asyncBrokenStreamProvider{name: "B"}
	c := &okStreamProvider{name: "C"}
	fp := NewCascadeProvider([]Provider{a, b, c}, "A -> B -> C")

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream sync start must succeed (async failure comes later), got: %v", err)
	}
	texts, lastErr, notices := collectStream1244(t, stream)
	if lastErr != nil {
		t.Fatalf("chain must recover to C, but consumer saw error: %v", lastErr)
	}
	if len(texts) != 1 || texts[0] != "hello from C" {
		t.Fatalf("expected C's text, got %v", texts)
	}
	if c.calls.Load() != 1 {
		t.Fatalf("C must be attempted exactly once, got %d", c.calls.Load())
	}
	if notices != 2 {
		t.Fatalf("expected 2 failover system notices (A->B, B->C), got %d", notices)
	}
}

func TestFallback_FullyBrokenAsyncChainTerminates(t *testing.T) {
	a := &asyncBrokenStreamProvider{name: "A"}
	b := &asyncBrokenStreamProvider{name: "B"}
	fp := NewCascadeProvider([]Provider{a, b}, "A -> B")

	stream, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream sync start: %v", err)
	}
	texts, lastErr, _ := collectStream1244(t, stream)
	if lastErr == nil {
		t.Fatalf("fully-broken chain must surface the error, got texts %v", texts)
	}

	if a.calls.Load() != 2 || b.calls.Load() != 1 {
		t.Fatalf("expected A=2 (initial + wrap retry), B=1 (budget = one revolution); got A=%d, B=%d", a.calls.Load(), b.calls.Load())
	}
}
