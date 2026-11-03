package provider

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

type callPolicy struct {
	requestTimeout time.Duration
	maxRetries     int
}

type callPolicySetter interface {
	setCallPolicy(cp callPolicy)
}

func callPolicyFromResolved(resolved *config.ResolvedEndpoint) callPolicy {
	if resolved == nil {
		return callPolicy{}
	}
	return callPolicy{
		requestTimeout: resolved.RequestTimeout,
		maxRetries:     resolved.MaxRetries,
	}
}

func (c callPolicy) attempts() int {
	if retryAlwaysEnabled.Load() {

		return retryAlwaysAttempts
	}
	if c.maxRetries > 0 {
		return c.maxRetries
	}
	return providerRetryAttempts
}

func (c callPolicy) timeoutRetryInPlace() bool {
	return c.requestTimeout <= 0
}

func (c callPolicy) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.requestTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.requestTimeout)
}

func (p *OpenAIProvider) setCallPolicy(cp callPolicy)          { p.policy = cp }
func (p *AnthropicProvider) setCallPolicy(cp callPolicy)       { p.policy = cp }
func (p *GeminiProvider) setCallPolicy(cp callPolicy)          { p.policy = cp }
func (p *OpenAIResponsesProvider) setCallPolicy(cp callPolicy) { p.policy = cp }
