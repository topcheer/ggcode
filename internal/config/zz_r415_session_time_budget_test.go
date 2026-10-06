package config

// r415 probe: session_time_budget validation branch mirrors
// session_token_budget's (negative rejected, zero and positive pass).
// DefaultConfig provides the valid vendor/endpoint prerequisite chain.

import (
	"strings"
	"testing"
	"time"
)

func r415TimeBudgetConfig(budget time.Duration) *Config {
	cfg := DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o-mini"
	cfg.Vendors["openai"].Endpoints["api"] = EndpointConfig{
		DisplayName:   "OpenAI API",
		Protocol:      "openai",
		BaseURL:       "https://api.openai.com/v1",
		DefaultModel:  "gpt-4o-mini",
		SelectedModel: "gpt-4o-mini",
		ContextWindow: 64000,
		MaxTokens:     4096,
	}
	cfg.SessionTimeBudget = budget
	return cfg
}

func TestR415_SessionTimeBudgetValidation(t *testing.T) {
	for _, budget := range []time.Duration{0, 10 * time.Minute} {
		if err := r415TimeBudgetConfig(budget).Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil for budget %v", err, budget)
		}
	}
	err := r415TimeBudgetConfig(-time.Minute).Validate()
	if err == nil || !strings.Contains(err.Error(), "session_time_budget") {
		t.Fatalf("negative budget: err = %v, want session_time_budget error", err)
	}
}
