package provider

import (
	"context"
	"errors"
	"testing"
)

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

func TestRetryAlwaysStillRetriesGatewayErrors(t *testing.T) {
	SetRetryAlways(true)
	t.Cleanup(func() { SetRetryAlways(false) })

	gwErr := errors.New("unexpected 404 from upstream gateway: model not found")
	if !isRetryableForContext(context.Background(), gwErr) {
		t.Fatal("retry-always must keep retrying misleading gateway permanent codes")
	}
}
