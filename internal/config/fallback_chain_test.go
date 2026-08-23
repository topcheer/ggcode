package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFallbackChainOrdering: legacy single entry keeps first (highest)
// priority, then fallbacks list entries in order; unconfigured entries
// are skipped.
func TestFallbackChainOrdering(t *testing.T) {
	c := &Config{}
	if got := len(c.FallbackChain()); got != 0 {
		t.Fatalf("empty config must yield empty chain, got %d", got)
	}

	c.Fallback = FallbackConfig{Enabled: true, Vendor: "kimi", Model: "k2"}
	c.Fallbacks = []FallbackConfig{
		{Enabled: true, Vendor: "zhipu", Model: "glm-5"},
		{Enabled: false, Vendor: "disabled", Model: "x"}, // skipped: not enabled
		{Enabled: true, Vendor: "anthropic", Endpoint: "primary", Model: "claude"},
		{Enabled: true, Vendor: "incomplete"}, // skipped: no model
	}
	chain := c.FallbackChain()
	want := []string{"kimi", "zhipu", "anthropic"}
	if len(chain) != len(want) {
		t.Fatalf("chain must have %d entries, got %d: %+v", len(want), len(chain), chain)
	}
	for i, v := range want {
		if chain[i].Vendor != v {
			t.Fatalf("chain[%d].Vendor = %q, want %q", i, chain[i].Vendor, v)
		}
	}
}

// TestFallbacksYAMLRoundTrip: the fallbacks list serializes and parses
// back through YAML (schema wiring).
func TestFallbacksYAMLRoundTrip(t *testing.T) {
	in := `
fallbacks:
  - enabled: true
    vendor: kimi
    model: k2
  - enabled: true
    vendor: anthropic
    endpoint: primary
    model: claude-sonnet
`
	var c Config
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	chain := c.FallbackChain()
	if len(chain) != 2 {
		t.Fatalf("expected 2 configured entries, got %d", len(chain))
	}
	if chain[0].Vendor != "kimi" || chain[1].Vendor != "anthropic" {
		t.Fatalf("order broken: %+v", chain)
	}
}
