package config

import (
	"strings"
	"testing"
)

// TestMatchKnownAnthropicEndpointURLScoped verifies #1515 case C: the
// fallback endpoint matcher must scope its table rows by baseURL, not
// return the first row unconditionally. The entry gate
// (isBootstrapKnownHost) only proves the URL contains SOME pattern's
// substring; the fallback must pick the row whose substring it actually
// contains.
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

	// URL contains bigmodel -> zai endpoint selected and carries the key.
	v, e := matchKnownAnthropicEndpoint(cfg(), "https://BIGMODEL.cn/api/anthropic", "sk-test", "glm-4.7")
	if v != "zai" || e != "cn-coding-anthropic" {
		t.Fatalf("bigmodel URL matched (%s, %s), want (zai, cn-coding-anthropic)", v, e)
	}

	// URL that matches NO pattern row must not match anything - even
	// though the table's first row exists. (Old code returned the first
	// row unconditionally.)
	if v, e := matchKnownAnthropicEndpoint(cfg(), "https://api.anthropic.com", "sk-test", "m1"); v != "" || e != "" {
		t.Fatalf("unmatched URL returned (%s, %s), want empty", v, e)
	}

	// Two-row table: the row matching the URL wins, not the first row.
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
	// First row still wins for its own URL.
	if v, e := matchKnownAnthropicEndpoint(cfg(), "https://bigmodel.cn/x", "sk-3", "m3"); v != "zai" || e != "cn-coding-anthropic" {
		t.Fatalf("first-row URL matched (%s, %s), want (zai, cn-coding-anthropic)", v, e)
	}
	_ = strings.ToLower
}
