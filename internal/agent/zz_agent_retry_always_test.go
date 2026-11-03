package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestAgentRetryAlwaysAuthErrorRetryable(t *testing.T) {
	provider.SetRetryAlways(true)
	defer provider.SetRetryAlways(false)

	authErr := errors.New("401 unauthorized: invalid api key")
	if !isAgentRetryableLLMError(authErr) {
		t.Fatalf("retry-always: 401 auth error must be agent-retryable")
	}
	if got := agentLLMRetryLimit(); got != 1<<30 {
		t.Fatalf("retry-always: retry limit = %d, want 1<<30", got)
	}

	quotaErr := errors.New("429 you have exceeded your billing quota")
	if !isAgentRetryableLLMError(quotaErr) {
		t.Fatalf("retry-always: quota error must be agent-retryable")
	}
}

func TestAgentRetryAlwaysCancellationStillExits(t *testing.T) {
	provider.SetRetryAlways(true)
	defer provider.SetRetryAlways(false)

	if isAgentRetryableLLMError(context.Canceled) {
		t.Fatalf("retry-always: user cancellation must still exit, not retry")
	}
	if isAgentRetryableLLMError(nil) {
		t.Fatalf("nil error is never retryable")
	}
}

func TestAgentRetryDefaultsUnchanged(t *testing.T) {

	if isAgentRetryableLLMError(errors.New("401 unauthorized")) {
		t.Fatalf("default mode: auth error must NOT be agent-retryable")
	}
	if got := agentLLMRetryLimit(); got != maxAgentLLMRetries {
		t.Fatalf("default mode: retry limit = %d, want %d", got, maxAgentLLMRetries)
	}
}
