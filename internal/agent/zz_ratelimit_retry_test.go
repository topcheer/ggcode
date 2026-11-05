package agent

import (
	"errors"
	"testing"
	"time"
)

// Rate-limit retry policy (user directive 2026-11-05): a rate-limited LLM call
// (transient 429 or a coding-plan window/quota reset) retries in place every
// 5 minutes by default, instead of failing the turn after three short backoffs.
func TestRateLimitRetryPolicyDefaults(t *testing.T) {
	t.Setenv(rateLimitRetryIntervalEnvVar, "")
	t.Setenv(rateLimitRetryMaxEnvVar, "")
	interval, max := rateLimitRetryPolicy()
	if interval != 5*time.Minute {
		t.Errorf("default interval = %v, want 5m", interval)
	}
	if max != 0 {
		t.Errorf("default max = %d, want 0 (unlimited)", max)
	}
}

func TestRateLimitRetryPolicyEnvOverrides(t *testing.T) {
	t.Setenv(rateLimitRetryIntervalEnvVar, "90s")
	t.Setenv(rateLimitRetryMaxEnvVar, "4")
	interval, max := rateLimitRetryPolicy()
	if interval != 90*time.Second {
		t.Errorf("interval = %v, want 90s", interval)
	}
	if max != 4 {
		t.Errorf("max = %d, want 4", max)
	}
	// Bare seconds accepted too.
	t.Setenv(rateLimitRetryIntervalEnvVar, "120")
	if got, _ := rateLimitRetryPolicy(); got != 120*time.Second {
		t.Errorf("bare-seconds interval = %v, want 120s", got)
	}
	// Invalid values fall back to defaults rather than erroring.
	t.Setenv(rateLimitRetryIntervalEnvVar, "bogus")
	if got, _ := rateLimitRetryPolicy(); got != 5*time.Minute {
		t.Errorf("invalid interval = %v, want default 5m", got)
	}
}

func TestIsRateLimitedLLMError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain 429", errors.New("429 Too Many Requests"), true},
		{"rate limit phrase", errors.New("rate limit exceeded"), true},
		{"anthropic window limit", errors.New("5-hour window limit will reset at 18:00"), true},
		{"quota exhausted stays fail-fast", errors.New("insufficient_quota: quota exceeded"), false},
		{"auth is not rate limit", errors.New("invalid api key"), false},
	}
	for _, tc := range cases {
		if got := isRateLimitedLLMError(tc.err); got != tc.want {
			t.Errorf("%s: isRateLimitedLLMError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
