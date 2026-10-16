package main

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4117 ad-hoc provider mode: flag/env resolution, protocol inference, config
// synthesis, and the partial-pair error contract.

func TestAdhocActiveRequiresBothBaseURLAndModel(t *testing.T) {
	t.Setenv(adhocEnvBaseURL, "")
	t.Setenv(adhocEnvModel, "")
	t.Setenv(adhocEnvAPIKey, "")
	f := adhocLLMFlags{}
	if f.adhocActive() {
		t.Fatal("empty flags + empty env must not activate")
	}
	f.BaseURL = "https://api.example.com/v1"
	if f.adhocActive() {
		t.Fatal("URL without model must not activate")
	}
	f.Model = "gpt-test"
	if !f.adhocActive() {
		t.Fatal("URL + model via flags must activate")
	}
}

func TestAdhocActiveViaEnv(t *testing.T) {
	t.Setenv(adhocEnvBaseURL, "https://api.example.com/v1")
	t.Setenv(adhocEnvModel, "m1")
	f := adhocLLMFlags{}
	if !f.adhocActive() {
		t.Fatal("URL + model via env must activate")
	}
}

func TestAdhocFlagWinsOverEnv(t *testing.T) {
	t.Setenv(adhocEnvBaseURL, "https://env.example.com")
	t.Setenv(adhocEnvModel, "env-model")
	f := adhocLLMFlags{BaseURL: "https://flag.example.com", Model: "flag-model"}
	cfg, err := f.synthesizeAdhocConfig()
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	ep := cfg.Vendors[adhocVendorName].Endpoints[adhocEndpointName]
	if ep.BaseURL != "https://flag.example.com" {
		t.Errorf("flag base URL must win, got %q", ep.BaseURL)
	}
	if ep.SelectedModel != "flag-model" {
		t.Errorf("flag model must win, got %q", ep.SelectedModel)
	}
}

func TestAdhocPartialPairIsError(t *testing.T) {
	t.Setenv(adhocEnvBaseURL, "")
	t.Setenv(adhocEnvModel, "")
	f := adhocLLMFlags{BaseURL: "https://only-url.example.com"}
	_, err := f.synthesizeAdhocConfig()
	if err == nil {
		t.Fatal("partial pair must error, not silently synthesize")
	}
	if !strings.Contains(err.Error(), "BOTH") {
		t.Errorf("error must explain the both-required contract: %v", err)
	}
}

func TestAdhocProtocolInference(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com/v1/messages":      "anthropic",
		"https://generativelanguage.googleapis.com/": "gemini",
		"https://api.openai.com/v1":                  "openai",
		"https://my-gateway.local/v1":                "openai", // unknown -> openai-compatible default
	}
	for url, want := range cases {
		if got := adhocInferProtocol(url); got != want {
			t.Errorf("adhocInferProtocol(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestAdhocProtocolOverrideBeatsInference(t *testing.T) {
	t.Setenv(adhocEnvProtocol, "")
	f := adhocLLMFlags{BaseURL: "https://api.anthropic.com", Model: "m", Protocol: "openai"}
	cfg, err := f.synthesizeAdhocConfig()
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	ep := cfg.Vendors[adhocVendorName].Endpoints[adhocEndpointName]
	if ep.Protocol != "openai" {
		t.Errorf("explicit protocol override must beat URL inference, got %q", ep.Protocol)
	}
}

func TestAdhocContextWindowAndMaxTokensFromEnv(t *testing.T) {
	t.Setenv(adhocEnvContextWindow, "200000")
	t.Setenv(adhocEnvMaxTokens, "32768")
	t.Setenv(adhocEnvBaseURL, "https://api.example.com")
	t.Setenv(adhocEnvModel, "m")
	f := adhocLLMFlags{}
	cfg, err := f.synthesizeAdhocConfig()
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	ep := cfg.Vendors[adhocVendorName].Endpoints[adhocEndpointName]
	if ep.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", ep.ContextWindow)
	}
	if ep.MaxTokens != 32768 {
		t.Errorf("MaxTokens = %d, want 32768", ep.MaxTokens)
	}
}

func TestAdhocConfigIsResolvable(t *testing.T) {
	// The synthesized config must survive the real resolution pipeline the
	// pipe entry point uses (ResolveProvider -> ResolveCurrentSelection).
	t.Setenv(adhocEnvBaseURL, "")
	t.Setenv(adhocEnvModel, "")
	f := adhocLLMFlags{BaseURL: "https://api.example.com/v1", Model: "gpt-test", APIKey: "sk-test"}
	cfg, err := f.synthesizeAdhocConfig()
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if cfg.Vendor != adhocVendorName || cfg.Endpoint != adhocEndpointName {
		t.Fatalf("selection fields wrong: vendor=%q endpoint=%q", cfg.Vendor, cfg.Endpoint)
	}
	resolved, err := cfg.ResolveEndpointSelection(cfg.Vendor, cfg.Endpoint, cfg.Model)
	if err != nil {
		t.Fatalf("ResolveEndpointSelection on synthesized config: %v", err)
	}
	if resolved.BaseURL != f.BaseURL || resolved.Model != f.Model {
		t.Errorf("resolved endpoint mismatch: %+v", resolved)
	}
}

// --llm-retry-always (#ad-hoc): bool flag/env resolution and the process-wide
// retry-always activation contract (ad-hoc mode only; never leaks into normal
// file-config runs).

func TestAdhocBoolFlagAndEnv(t *testing.T) {
	t.Setenv(adhocEnvRetryAlways, "")
	if adhocBool(false, adhocEnvRetryAlways) {
		t.Fatal("false flag + empty env must be false")
	}
	if !adhocBool(true, adhocEnvRetryAlways) {
		t.Fatal("true flag must win regardless of env")
	}
	for _, v := range []string{"1", "true", "TRUE", "Yes", "on"} {
		t.Setenv(adhocEnvRetryAlways, v)
		if !adhocBool(false, adhocEnvRetryAlways) {
			t.Fatalf("env %q must read as true", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "garbage"} {
		t.Setenv(adhocEnvRetryAlways, v)
		if adhocBool(false, adhocEnvRetryAlways) {
			t.Fatalf("env %q must read as false", v)
		}
	}
}

func TestAdhocRetryAlwaysActivatesOnlyInAdhocMode(t *testing.T) {
	provider.SetRetryAlways(false)
	t.Cleanup(func() { provider.SetRetryAlways(false) })

	// Ad-hoc active + retry-always set: process-wide mode flips on.
	t.Setenv(adhocEnvBaseURL, "https://api.example.com/v1")
	t.Setenv(adhocEnvModel, "m")
	t.Setenv(adhocEnvRetryAlways, "true")
	if _, err := loadConfigForRun("", &adhocLLMFlags{}); err != nil {
		t.Fatalf("loadConfigForRun adhoc: %v", err)
	}
	if !provider.RetryAlwaysEnabled() {
		t.Fatal("retry-always must activate in ad-hoc mode")
	}

	// Ad-hoc active WITHOUT retry-always: mode stays off.
	provider.SetRetryAlways(false)
	t.Setenv(adhocEnvRetryAlways, "")
	if _, err := loadConfigForRun("", &adhocLLMFlags{}); err != nil {
		t.Fatalf("loadConfigForRun adhoc (no retry-always): %v", err)
	}
	if provider.RetryAlwaysEnabled() {
		t.Fatal("retry-always must stay off when not requested")
	}

	// Retry-always requested on a plain -p pipe run (no ad-hoc provider
	// pair): the escape hatch MUST activate. Batch callers pass only the
	// flag against the config-file provider; gating it behind
	// adhocActive() silently disabled retries for exactly those callers
	// (their 401s terminated on the first attempt). Config-file load may
	// fail on a nonexistent path - irrelevant to the activation check.
	t.Setenv(adhocEnvBaseURL, "")
	t.Setenv(adhocEnvModel, "")
	t.Setenv(adhocEnvRetryAlways, "true")
	provider.SetRetryAlways(false)
	if _, err := loadConfigForRun("nonexistent-config-file-for-test.yaml", &adhocLLMFlags{}); err == nil {
		t.Log("config file load unexpectedly succeeded; activation check still applies")
	}
	if !provider.RetryAlwaysEnabled() {
		t.Fatal("retry-always must activate on plain pipe runs via env, even without the ad-hoc provider pair")
	}

	// Not requested anywhere: mode stays off (no leak from stale state).
	provider.SetRetryAlways(false)
	t.Setenv(adhocEnvRetryAlways, "")
	if _, err := loadConfigForRun("nonexistent-config-file-for-test.yaml", &adhocLLMFlags{}); err == nil {
		t.Log("config file load unexpectedly succeeded; leak check still applies")
	}
	if provider.RetryAlwaysEnabled() {
		t.Fatal("retry-always must stay off when not requested")
	}
}
