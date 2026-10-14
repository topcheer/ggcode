package main

import (
	"strings"
	"testing"
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
