package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestVendorSpecificAuthHeaders_XiaomiMiMoAddsAPIKey(t *testing.T) {
	headers := vendorSpecificAuthHeaders("https://token-plan-cn.xiaomimimo.com/anthropic", "tp-test")
	if got := headers.Get("api-key"); got != "tp-test" {
		t.Fatalf("expected api-key header for xiaomimimo host, got %q", got)
	}
}

func TestVendorSpecificAuthHeaders_OtherHostsStayEmpty(t *testing.T) {
	headers := vendorSpecificAuthHeaders("https://api.openai.com/v1", "sk-test")
	if got := headers.Get("api-key"); got != "" {
		t.Fatalf("expected no api-key header for non-xiaomimimo host, got %q", got)
	}
}

func TestIsOpenRouterEndpoint(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{baseURL: "https://openrouter.ai/api/v1", want: true},
		{baseURL: "https://openrouter.ai", want: true},
		{baseURL: "https://api.openrouter.ai/v1", want: true},
		{baseURL: "https://api.openai.com/v1", want: false},
		{baseURL: "https://api.anthropic.com", want: false},
		{baseURL: "", want: false},
		{baseURL: "not a url", want: false},
		// Ensure lookalike domain is not matched.
		{baseURL: "https://openrouter.ai.evil.com/v1", want: false},
	}
	for _, tc := range tests {
		if got := isOpenRouterEndpoint(tc.baseURL); got != tc.want {
			t.Errorf("isOpenRouterEndpoint(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestIsXiaomiMiMoBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{baseURL: "https://token-plan-cn.xiaomimimo.com/v1", want: true},
		{baseURL: "https://platform.xiaomimimo.com/docs", want: true},
		{baseURL: "https://api.openai.com/v1", want: false},
		{baseURL: "not a url", want: false},
	}
	for _, tc := range tests {
		if got := isXiaomiMiMoBaseURL(tc.baseURL); got != tc.want {
			t.Fatalf("isXiaomiMiMoBaseURL(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestIsOpenCodeBaseURL(t *testing.T) {
	yes := []string{"https://opencode.ai/zen/v1", "https://opencode.ai/inference/openai/v1"}
	no := []string{"https://example.com/zen/v1", "", "https://githubcopilot.com"}
	for _, u := range yes {
		if !isOpenCodeBaseURL(u) {
			t.Errorf("isOpenCodeBaseURL(%q) = false, want true", u)
		}
	}
	for _, u := range no {
		if isOpenCodeBaseURL(u) {
			t.Errorf("isOpenCodeBaseURL(%q) = true, want false", u)
		}
	}
}

// A key that is NOT the stored OAuth token must never gain the org header,
// even on opencode hosts (plain API keys keep legacy zen behavior). Uses a
// sentinel key that no real store can hold.
func TestVendorSpecificAuthHeaders_OpenCodeWrongKeyNoOrgHeader(t *testing.T) {
	h := vendorSpecificAuthHeaders("https://opencode.ai/inference/openai/v1", "definitely-not-the-stored-token-xyz")
	if got := h.Get("x-opencode-org-id"); got != "" {
		t.Fatalf("x-opencode-org-id = %q for non-OAuth key, want empty", got)
	}
}

// The OpenCode gateway free-tier gate keys on the CLI's signature tool names
// being present in the tools array (probed live: bash+read -> 200, either
// missing -> 403 FreeTierError). Ghost injection must fire on opencode hosts
// only when real tools exist, never duplicate names, and never touch other
// hosts.
func TestApplyGatewayGhostTools(t *testing.T) {
	mk := func(base string) *OpenAIProvider {
		p := NewOpenAIProviderWithBaseURL("k", "m", 8, base)
		return p
	}
	sample := []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "run_command"}}}

	req := openai.ChatCompletionRequest{Tools: sample}
	mk("https://opencode.ai/inference/openai/v1").applyGatewayGhostTools(&req)
	names := map[string]bool{}
	for _, tl := range req.Tools {
		names[tl.Function.Name] = true
	}
	if !names["bash"] || !names["read"] || !names["run_command"] {
		t.Fatalf("opencode host: want bash+read+run_command, got %v", names)
	}

	req2 := openai.ChatCompletionRequest{Tools: sample}
	mk("https://opencode.ai/inference/openai/v1").applyGatewayGhostTools(&req2)
	mk("https://opencode.ai/inference/openai/v1").applyGatewayGhostTools(&req2)
	bashCount := 0
	for _, tl := range req2.Tools {
		if tl.Function.Name == "bash" {
			bashCount++
		}
	}
	if bashCount != 1 {
		t.Fatalf("double apply duplicated ghost (bash x%d)", bashCount)
	}

	req3 := openai.ChatCompletionRequest{Tools: sample}
	mk("https://api.openai.com/v1").applyGatewayGhostTools(&req3)
	if len(req3.Tools) != 1 {
		t.Fatalf("non-opencode host mutated tools: %d", len(req3.Tools))
	}

	req4 := openai.ChatCompletionRequest{}
	mk("https://opencode.ai/inference/openai/v1").applyGatewayGhostTools(&req4)
	if len(req4.Tools) != 0 {
		t.Fatalf("no real tools: ghosts must not be the only tools (%d)", len(req4.Tools))
	}
}
