package provider

import (
	"context"
	"testing"
	"time"
)

// zz_issue4295: gemini.go / openai.go sendEvent had the same
// deadline-vs-terminal-error race #4293 fixed in openai_responses.go: when the
// per-call deadline fires at the same instant the terminal StreamEventError
// is sent, the old single select had BOTH cases ready and picked ctx.Done
// 50/50 - dropping the very error the deadline produced. The non-blocking
// fast path must make the buffered-send case deterministic, while a FULL
// buffer + cancelled ctx must still return false (leak guard preserved).
func TestIssue4295TerminalEventSurvivesExpiredDeadline(t *testing.T) {
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel() // ctx already expired; cancel for lint hygiene

	cases := []struct {
		name string
		send func(ctx context.Context, ch chan StreamEvent) bool
	}{
		{"gemini", func(ctx context.Context, ch chan StreamEvent) bool {
			return (&GeminiProvider{}).sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: errDeadlineHit})
		}},
		{"openai", func(ctx context.Context, ch chan StreamEvent) bool {
			return (&OpenAIProvider{}).sendEvent(ctx, ch, StreamEvent{Type: StreamEventError, Error: errDeadlineHit})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Terminal error event on a buffered channel with an EMPTY buffer
			// (consumer still draining): must be delivered even though ctx is
			// already expired. Loop a few times to catch any residual
			// nondeterminism, not just the 50/50 coin.
			for i := 0; i < 20; i++ {
				ch := make(chan StreamEvent, 64)
				if !tc.send(deadlineCtx, ch) {
					t.Fatalf("%s iter %d: terminal error event dropped on expired deadline with empty buffer", tc.name, i)
				}
				select {
				case ev := <-ch:
					if ev.Type != StreamEventError {
						t.Fatalf("%s: got event type %v, want StreamEventError", tc.name, ev.Type)
					}
				default:
					t.Fatalf("%s: sendEvent returned true but no event arrived", tc.name)
				}
			}
		})
	}
}

func TestIssue4295FullBufferCancelledCtxStillGuards(t *testing.T) {
	// Full buffer + cancelled ctx: the racing select must return false so the
	// producer never parks forever on a channel a cancelling consumer stopped
	// reading (#3068/#3071 leak guard is preserved by the fallback select).
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	fullCh := make(chan StreamEvent, 1)
	fullCh <- StreamEvent{Type: StreamEventText, Text: "fill"}

	if (&GeminiProvider{}).sendEvent(cancelledCtx, fullCh, StreamEvent{Type: StreamEventError, Error: errDeadlineHit}) {
		t.Fatal("gemini: full buffer + cancelled ctx must return false (leak guard)")
	}
	if (&OpenAIProvider{}).sendEvent(cancelledCtx, fullCh, StreamEvent{Type: StreamEventError, Error: errDeadlineHit}) {
		t.Fatal("openai: full buffer + cancelled ctx must return false (leak guard)")
	}
}

var errDeadlineHit = &staticTestError{"context deadline exceeded"}

type staticTestError struct{ msg string }

func (e *staticTestError) Error() string { return e.msg }
