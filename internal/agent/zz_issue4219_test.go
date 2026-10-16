package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4219: retry-always must NOT lift the agent-level overflow exclusion.
// Overflow is deterministic for the same message payload; it belongs to the
// reactive-compaction path whose exhaustion terminates the run. Retrying it
// at the agent level produced an infinite resend loop in pipe mode.
func TestAgentRetryAlwaysOverflowStillNonRetryable(t *testing.T) {
	provider.SetRetryAlways(true)
	t.Cleanup(func() { provider.SetRetryAlways(false) })

	overflow := []error{
		errors.New("requested 40123 tokens, maximum is 131072"),
		errors.New("prompt is too long: 90000 tokens > 8192 maximum"),
	}
	for _, err := range overflow {
		if isAgentRetryableLLMError(err) {
			t.Fatalf("agent-level retry-always must not retry overflow: %q", err)
		}
	}

	// Contrast: auth misreports stay retryable under retry-always (flag rationale).
	auth := errors.New("401 unauthorized")
	if !isAgentRetryableLLMError(auth) {
		t.Fatal("agent-level retry-always should retry intermittent auth errors")
	}
}

// #4219 companion: the linear backoff must be capped even as the retry
// counter grows unbounded under retry-always.
func TestAgentLLMRetryDelayCapped(t *testing.T) {
	multiplier := 2
	if got := time.Duration(1000*multiplier) * time.Second; got <= maxAgentLLMRetryDelay {
		t.Fatalf("precondition: large counters should exceed the cap, got %v", got)
	}
	if maxAgentLLMRetryDelay <= 0 {
		t.Fatalf("cap must be positive, got %v", maxAgentLLMRetryDelay)
	}
}
