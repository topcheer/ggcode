package provider

import (
	"net/http"
	"testing"
)

// #624: LiteLLM/OpenRouter-style gateways send their own x-ratelimit-*
// headers (often only the requests fields) ALONGSIDE the upstream
// anthropic-ratelimit-* token headers. The OpenAI-group early return
// (`if !info.IsEmpty() { return info }`) used to drop the Anthropic token
// headers, leaving RemainingTokens=-1 so TokenFractionRemaining() read as
// 1.000 (100% illusion) and token-critical warnings never fired — the same
// failure mode #602 R6 fixed for the anthropic/reratelimit prefix pair.
func TestIssue624_OpenAIGroupDoesNotDropAnthropicTokenHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "3")
	h.Set("x-ratelimit-limit-requests", "100")
	h.Set("anthropic-ratelimit-tokens-remaining", "500")
	h.Set("anthropic-ratelimit-tokens-limit", "100000")

	info := parseRateLimitHeaders(h)

	if info.RemainingRequests != 3 || info.LimitRequests != 100 {
		t.Fatalf("OpenAI request fields lost: %+v", info)
	}
	if info.RemainingTokens != 500 {
		t.Fatalf("RemainingTokens = %d, want 500 (Anthropic token headers must merge past the OpenAI-group early return, #624)", info.RemainingTokens)
	}
	if info.LimitTokens != 100000 {
		t.Fatalf("LimitTokens = %d, want 100000", info.LimitTokens)
	}
	frac := info.TokenFractionRemaining()
	if frac < 0.0049 || frac > 0.0051 {
		t.Fatalf("TokenFractionRemaining = %v, want ~0.005 (was 1.000 pre-fix)", frac)
	}
	if !info.IsCritical() {
		t.Fatal("token-exhausted mixed-header response must be IsCritical")
	}
}

// Regression guards: single-group responses must still parse exactly as
// before — absent headers must never clobber fields parsed from the other
// group.
func TestIssue624_SingleGroupStillParses(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "99")
	h.Set("x-ratelimit-limit-requests", "100")
	info := parseRateLimitHeaders(h)
	if info.RemainingRequests != 99 || info.LimitRequests != 100 {
		t.Fatalf("OpenAI-requests-only parse changed: %+v", info)
	}
	if info.RemainingTokens != -1 || info.LimitTokens != -1 {
		t.Fatalf("absent Anthropic headers must stay -1: %+v", info)
	}

	h2 := http.Header{}
	h2.Set("x-ratelimit-remaining-tokens", "42")
	h2.Set("x-ratelimit-limit-tokens", "1000")
	info2 := parseRateLimitHeaders(h2)
	if info2.RemainingTokens != 42 || info2.LimitTokens != 1000 {
		t.Fatalf("OpenAI-tokens-only parse changed: %+v", info2)
	}
	if info2.RemainingRequests != -1 {
		t.Fatalf("absent request headers must stay -1: %+v", info2)
	}
}
