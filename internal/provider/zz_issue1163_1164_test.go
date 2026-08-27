package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Test providers dedicated to issues #1163/#1164. Kept separate from
// mockProvider so these scenarios can express async-before-output streams.

type zz116xProvider struct {
	name        string
	syncErr     error // returned synchronously by ChatStream/Chat
	asyncErr    error // emitted as an error event before any output
	text        string
	streamCalls int32 // guarded by the provider being consumed serially; read via atomic-free helper after drain
}

func (p *zz116xProvider) Name() string { return p.name }
func (p *zz116xProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return len(messages) * 10, nil
}
func (p *zz116xProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	if p.syncErr != nil {
		return nil, p.syncErr
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (p *zz116xProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	p.streamCalls++
	if p.syncErr != nil {
		return nil, p.syncErr
	}
	ch := make(chan StreamEvent, 8)
	if p.asyncErr != nil {
		ch <- StreamEvent{Type: StreamEventError, Error: p.asyncErr}
	} else if p.text != "" {
		ch <- StreamEvent{Type: StreamEventText, Text: p.text}
	}
	ch <- StreamEvent{Type: StreamEventDone}
	close(ch)
	return ch, nil
}

var (
	errQuotaA = errors.New("insufficient_quota: quota exceeded on primary A")
	errQuotaB = errors.New("insufficient_quota: quota exceeded on B")
	errAuthB  = errors.New("401 Unauthorized: invalid_api_key on B")
)

// Issue #1164: a sync-path failover retry whose replacement stream later fails
// asynchronously must still be watched so the chain advances again. Without
// the wrapper, the retried stream's async error passes straight to the caller.
func TestIssue1164_SyncFailoverRetryStreamIsWatched(t *testing.T) {
	a := &zz116xProvider{name: "a", syncErr: errQuotaA}
	b := &zz116xProvider{name: "b", asyncErr: errQuotaB}
	c := &zz116xProvider{name: "c", text: "from-c"}
	fp := NewCascadeProvider([]Provider{a, b, c}, "a->b->c")

	out, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected sync error: %v", err)
	}

	gotText := ""
	var gotErrors []error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range out {
			switch ev.Type {
			case StreamEventError:
				if ev.Error != nil {
					gotErrors = append(gotErrors, ev.Error)
				}
			case StreamEventText:
				gotText += ev.Text
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out consuming stream")
	}

	if len(gotErrors) != 0 {
		t.Fatalf("expected chained failover to hide b's async error, got %v", gotErrors)
	}
	if gotText != "from-c" {
		t.Fatalf("expected output from c, got %q", gotText)
	}
	if !fp.HasFailedOver() {
		t.Fatal("expected failover to be recorded")
	}
	if c.streamCalls == 0 {
		t.Fatal("expected c to receive the retried request (chained failover)")
	}
}

// Issue #1163: when an async-error failover's replacement provider fails to
// even start (sync error), both root causes must reach the consumer instead of
// only the original provider's stale error.
func TestIssue1163_AsyncFailoverPreservesFallbackSyncError(t *testing.T) {
	a := &zz116xProvider{name: "a", asyncErr: errQuotaA}
	b := &zz116xProvider{name: "b", syncErr: errAuthB}
	fp := NewFallbackProvider(a, b, "a->b")

	out, err := fp.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected sync error: %v", err)
	}

	var gotErrors []error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range out {
			if ev.Type == StreamEventError && ev.Error != nil {
				gotErrors = append(gotErrors, ev.Error)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out consuming stream")
	}

	if len(gotErrors) == 0 {
		t.Fatal("expected at least one propagated error event")
	}
	last := gotErrors[len(gotErrors)-1]
	if !errors.Is(last, errQuotaA) || !errors.Is(last, errAuthB) {
		t.Fatalf("expected joined root causes (quota + auth), got: %v", last)
	}
}
