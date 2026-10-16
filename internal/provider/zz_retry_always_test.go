package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
)

// --llm-retry-always (ad-hoc pipe mode): when SetRetryAlways(true) is active
// every error class becomes retryable, including the permanent ones the
// classifier normally rejects (401/404/422/400, quota, context overflow).
// Caller-context cancellation stays non-retryable so Ctrl-C still works, and
// the attempt/backoff caps are lifted (callPolicy.attempts, retryBudget).

func TestRetryAlwaysPermanentErrorsBecomeRetryable(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	cases := []error{
		&openai.APIError{HTTPStatusCode: 401, Message: "unauthorized"},
		&openai.APIError{HTTPStatusCode: 404, Message: "model not found"},
		&openai.APIError{HTTPStatusCode: 422, Message: "unprocessable"},
		errors.New("status code: 400, message: bad request"),
		errors.New("requested 40123 tokens, maximum is 131072"), // context overflow
		errors.New("quota exceeded: billing cycle"),
	}
	ctx := context.Background()
	for _, err := range cases {
		if !isRetryableForContext(ctx, err) {
			t.Fatalf("retry-always must retry %q", err)
		}
	}
}

func TestRetryAlwaysCancellationStillStops(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if isRetryableForContext(ctx, context.Canceled) {
		t.Fatal("canceled ctx must never retry, even with retry-always")
	}
	deadlineCtx, dCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer dCancel()
	<-deadlineCtx.Done()
	if isRetryableForContext(deadlineCtx, context.DeadlineExceeded) {
		t.Fatal("deadline-exceeded ctx must never retry, even with retry-always")
	}
}

func TestRetryAlwaysBudgetUnlimited(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	// retryWithBackoffCtx marks the budget unlimited under retry-always:
	// deadline checks are skipped entirely and the budget can never report
	// exhaustion. Probe that mechanism directly.
	b := &retryBudget{unlimited: true}
	if err := b.sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("zero-deadline budget must not exhaust, got %v", err)
	}
	if b.done {
		t.Fatal("zero-deadline budget must never be marked done")
	}
}

func TestRetryAlwaysAttemptsUnlimited(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	if got := (callPolicy{maxRetries: 3}).attempts(); got != retryAlwaysAttempts {
		t.Fatalf("retry-always must ignore the configured cap of 3, got %d", got)
	}
}

func TestRetryAlwaysDisabledKeepsClassifier(t *testing.T) {
	if RetryAlwaysEnabled() {
		t.Fatal("retry-always must default to off")
	}
	err := &openai.APIError{HTTPStatusCode: 401, Message: "unauthorized"}
	if isRetryableForContext(context.Background(), err) {
		t.Fatal("401 must stay non-retryable when retry-always is off")
	}
	if got := (callPolicy{maxRetries: 3}).attempts(); got != 3 {
		t.Fatalf("configured cap must apply when retry-always is off, got %d", got)
	}
}
