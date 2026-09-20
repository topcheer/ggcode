package config

import "testing"

// OAuth tokens are only valid on the inference gateway; zen URLs must be
// rewritten per protocol, non-zen URLs untouched.
func TestOpenCodeInferenceBaseURL(t *testing.T) {
	cases := []struct {
		name, in, proto, want string
	}{
		{"zen openai", "https://opencode.ai/zen/v1", "openai", "https://opencode.ai/inference/openai/v1"},
		{"zen anthropic", "https://opencode.ai/zen/v1", "anthropic", "https://opencode.ai/inference/anthropic/v1"},
		{"already inference", "https://opencode.ai/inference/openai/v1", "openai", "https://opencode.ai/inference/openai/v1"},
		{"custom proxy untouched", "https://my-proxy.example.com/zen/v1", "openai", "https://my-proxy.example.com/zen/v1"},
		{"other opencode path untouched", "https://opencode.ai/custom/v1", "openai", "https://opencode.ai/custom/v1"},
		{"empty", "", "openai", ""},
	}
	for _, c := range cases {
		if got := openCodeInferenceBaseURL(c.in, c.proto); got != c.want {
			t.Errorf("%s: openCodeInferenceBaseURL(%q,%q) = %q, want %q", c.name, c.in, c.proto, got, c.want)
		}
	}
}
