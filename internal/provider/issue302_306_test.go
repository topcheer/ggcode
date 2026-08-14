package provider

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
)

// #303: digit coincidences must NOT classify as FailureAuth.
func TestClassifyLLMError_TokenCountNotAuth(t *testing.T) {
	err := fmt.Errorf("request failed: requested 40123 tokens, maximum is 131072")
	if got := ClassifyLLMError(err); got == FailureAuth {
		t.Fatalf("token-count message misclassified as FailureAuth: %v", got)
	}
}

// #303: context-anchored 401 patterns still classify as FailureAuth.
func TestClassifyLLMError_Anchored401StillAuth(t *testing.T) {
	for _, msg := range []string{
		"request failed: status 401 unauthorized body",
		`openai: {"error":{"status":401,"message":"bad key"}}`,
		"statusCode:401",
		"invalid api key provided",
		// #313: go-openai SDK forms previously missed.
		"error, status code: 401, message: You must be a member of an organization to use the API",
		"error, status code: 401, status: , message: bad key",
		"failed with code: 401",
		"relay said 401,\nbody empty",
	} {
		if got := ClassifyLLMError(fmt.Errorf("%s", msg)); got != FailureAuth {
			t.Errorf("expected FailureAuth for %q, got %v", msg, got)
		}
	}
}

// #303: overflow errors must not be misclassified as quota/auth.
func TestClassifyLLMError_OverflowNotAuthQuota(t *testing.T) {
	err := fmt.Errorf("request failed: requested 40123 tokens, maximum is 131072")
	if IsContextOverflowError(err) != true {
		t.Fatalf("expected overflow detection for token-count message")
	}
	got := ClassifyLLMError(err)
	if got == FailureAuth || got == FailureQuota {
		t.Fatalf("overflow misclassified as %v", got)
	}
}

// #304: cancellation must not count toward consecutive-failure failover.
func TestMaybeFailover_CanceledNotCounted(t *testing.T) {
	f := &FallbackProvider{
		consecutiveFail: atomic.Int32{},
		failedOver:      atomic.Bool{},
	}
	// simulate 5 consecutive cancellations
	for i := 0; i < 5; i++ {
		_, retry := f.maybeFailover(context.Canceled, nil)
		if retry {
			t.Fatalf("cancellation offered fallback retry")
		}
		if f.consecutiveFail.Load() != 0 {
			t.Fatalf("cancellation incremented consecutiveFail to %d", f.consecutiveFail.Load())
		}
		if f.failedOver.Load() {
			t.Fatalf("cancellation triggered sticky failover")
		}
	}
}

// #306: string-form 400 must not be retried.
func TestIsRetryable_String400NotRetryable(t *testing.T) {
	for _, msg := range []string{
		"upstream request failed: status code: 400, message: invalid tool_use id: toolu_abc",
		`relay: {"status":400,"error":"bad request"}`,
		"statusCode:400 bad parameters",
	} {
		if isRetryable(fmt.Errorf("%s", msg)) {
			t.Errorf("expected 400 string error to be non-retryable: %q", msg)
		}
	}
}

// #306: 401/403/404 string forms remain non-retryable.
func TestIsRetryable_String4xxNotRetryable(t *testing.T) {
	for _, code := range []string{"401", "403", "404"} {
		if isRetryable(fmt.Errorf("status code: %s", code)) {
			t.Errorf("expected %s string error to be non-retryable", code)
		}
	}
}

// #302-retry parity: cancellation never retryable.
func TestIsRetryable_CanceledNeverRetryable(t *testing.T) {
	if isRetryable(context.Canceled) {
		t.Fatalf("context.Canceled should not be retryable")
	}
	var netErr net.Error = &timeoutErr{}
	if !isRetryable(netErr) {
		t.Fatalf("plain net timeout should remain retryable")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
