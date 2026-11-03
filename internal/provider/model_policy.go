package provider

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// callPolicy carries the resolved call tuning for one provider instance.
type callPolicy struct {
	requestTimeout time.Duration // deadline for a single LLM call; 0 = none
	maxRetries     int           // retry budget override; 0 = provider default
}

// callPolicySetter is implemented by providers that honor callPolicy. It
// exists so NewProvider can wire the policy uniformly across protocols.
type callPolicySetter interface {
	setCallPolicy(cp callPolicy)
}

// callPolicyFromResolved extracts the policy from a resolved endpoint config.
func callPolicyFromResolved(resolved *config.ResolvedEndpoint) callPolicy {
	if resolved == nil {
		return callPolicy{}
	}
	return callPolicy{
		requestTimeout: resolved.RequestTimeout,
		maxRetries:     resolved.MaxRetries,
	}
}

// attempts returns the retry budget for this provider: the configured
// override when positive, otherwise the provider-wide default.
func (c callPolicy) attempts() int {
	if retryAlwaysEnabled.Load() {

		return retryAlwaysAttempts
	}
	if c.maxRetries > 0 {
		return c.maxRetries
	}
	return providerRetryAttempts
}

// timeoutRetryInPlace reports whether timeout-class failures should retry
// in place without caps (#4729). Only the DEFAULT path (no configured
// request_timeout) qualifies: an explicitly configured deadline is a
// fail-fast contract (#2573, zz_issue2573_test.go) - a slow relay must hand
// over to the fallback chain quickly instead of being retried forever.
func (c callPolicy) timeoutRetryInPlace() bool {
	return c.requestTimeout <= 0
}

// withTimeout returns ctx wrapped with the configured per-call deadline, plus
// a cancel func the caller MUST defer (it is a no-op when no deadline is
// configured). For streaming calls, invoke this INSIDE the reader goroutine
// so the deadline covers the full stream lifetime and the cancel fires when
// the goroutine ends - ChatStream returns the channel immediately, so a
// caller-side defer would kill the stream early.
func (c callPolicy) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.requestTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.requestTimeout)
}

// setCallPolicy installs the resolved sa-78 call policy. Implemented per
// provider type here (one method per struct) so NewProvider can apply the
// policy uniformly via the callPolicySetter interface.
func (p *OpenAIProvider) setCallPolicy(cp callPolicy)          { p.policy = cp }
func (p *AnthropicProvider) setCallPolicy(cp callPolicy)       { p.policy = cp }
func (p *GeminiProvider) setCallPolicy(cp callPolicy)          { p.policy = cp }
func (p *OpenAIResponsesProvider) setCallPolicy(cp callPolicy) { p.policy = cp }
