package config

import (
	"strings"
	"testing"
)

func TestMatchKnownAnthropicEndpointURLScoped(t *testing.T) {
	cfg := func() *Config {
		return &Config{Vendors: map[string]VendorConfig{
			"zai": {Endpoints: map[string]EndpointConfig{
				"cn-coding-anthropic": {Protocol: "anthropic", BaseURL: "https://bigmodel.cn/api/anthropic"},
			}},
			"other": {Endpoints: map[string]EndpointConfig{
				"mirror": {Protocol: "anthropic", BaseURL: "https://mirror.example.com/api"},
			}},
		}}
	}

	v, e := matchKnownAnthropicEndpoint(cfg(), "https://BIGMODEL.cn/api/anthropic", "sk-test", "glm-4.7")
	if v != "zai" || e != "cn-coding-anthropic" {
		t.Fatalf("bigmodel URL matched (%s, %s), want (zai, cn-coding-anthropic)", v, e)
	}

	if v, e := matchKnownAnthropicEndpoint(cfg(), "https://api.anthropic.com", "sk-test", "m1"); v != "" || e != "" {
		t.Fatalf("unmatched URL returned (%s, %s), want empty", v, e)
	}

	old := knownAnthropicHostPatterns
	knownAnthropicHostPatterns = append(knownAnthropicHostPatterns,
		struct {
			substring  string
			vendorID   string
			endpointID string
		}{"mirror.example", "other", "mirror"})
	defer func() { knownAnthropicHostPatterns = old }()

	v, e = matchKnownAnthropicEndpoint(cfg(), "https://mirror.example.com/api", "sk-2", "m2")
	if v != "other" || e != "mirror" {
		t.Fatalf("second-row URL matched (%s, %s), want (other, mirror)", v, e)
	}

	if v, e := matchKnownAnthropicEndpoint(cfg(), "https://bigmodel.cn/x", "sk-3", "m3"); v != "zai" || e != "cn-coding-anthropic" {
		t.Fatalf("first-row URL matched (%s, %s), want (zai, cn-coding-anthropic)", v, e)
	}
	_ = strings.ToLower
}
