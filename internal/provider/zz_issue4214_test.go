package provider

import (
	"context"
	"errors"
	"testing"
)

// #4214: --llm-retry-always must not retry a context overflow. The request
// body is deterministic - resending the same bytes can never fit - so the
// old ordering (retryAlways checked before the overflow classifier) spun
// unattended pipes through 2^30 attempts on a zero-probability retry.
func TestRetryAlwaysDoesNotRetryContextOverflow(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	overflow := errors.New("This model's maximum context length is 8192 tokens, however you requested 100000 tokens")
	if !IsContextOverflowError(overflow) {
		t.Fatal("precondition: fixture must be classified as context overflow")
	}
	if isRetryableForContext(context.Background(), overflow) {
		t.Fatal("retry-always must not retry a deterministic context overflow (#4214)")
	}
}

// Control: retry-always still lifts caps for ordinary permanent-looking
// gateway errors (the flag's actual rationale).
func TestRetryAlwaysStillRetriesGatewayErrors(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	gwErr := errors.New("unexpected 404 from upstream gateway: model not found")
	if !isRetryableForContext(context.Background(), gwErr) {
		t.Fatal("retry-always must keep retrying misleading gateway permanent codes")
	}
}
