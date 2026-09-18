package provider

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// sa-78: per-endpoint / per-model LLM call policy.
//
// Every provider today shares one hardcoded retry budget
// (providerRetryAttempts = 20) and no per-call deadline. That is the right
// default for interactive sessions, but wrong for heterogeneous fleets: a
// fast, cheap model behind a flaky relay should fail fast so the fallback
// chain takes over quickly, while a slow reasoning model may legitimately
// stream for many minutes. LiteLLM models this as per-model `timeout` +
// `num_retries`; this is ggcode's equivalent, resolved from config:
//
//	vendors:
//	  acme:
//	    endpoints:
//	      relay:
//	        protocol: openai
//	        request_timeout: 120s   # endpoint-level default
//	        max_retries: 4
//	        model_limits:
//	          gpt-5-nano:
//	            request_timeout: 30s  # per-model override wins
//	            max_retries: 1
//
// Zero values mean "unset" and fall back to pre-existing behavior (no
// deadline, providerRetryAttempts), so the feature is fully opt-in.

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
	if c.maxRetries > 0 {
		return c.maxRetries
	}
	return providerRetryAttempts
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
