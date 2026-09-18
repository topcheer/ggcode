package config

import (
	"gopkg.in/yaml.v3"
	"testing"
	"time"
)

// Regression for #1517: AddEndpoint on an EXISTING endpoint wiped every
// field the incoming call did not carry (Models, SelectedModel, APIKey
// reference) - the comment promised "updated".
func TestAddEndpointExistingMergesNotReplaces(t *testing.T) {
	c := &Config{Vendors: map[string]VendorConfig{
		"v": {Endpoints: map[string]EndpointConfig{
			"e": {Protocol: "openai", BaseURL: "https://old", Models: []string{"m1", "m2"}, SelectedModel: "m1", APIKey: "${K}"},
		}},
	}}
	if err := c.AddEndpoint("v", "e", "openai", "https://new", ""); err != nil {
		t.Fatal(err)
	}
	ep := c.Vendors["v"].Endpoints["e"]
	if ep.BaseURL != "https://new" {
		t.Fatalf("base_url must update, got %q", ep.BaseURL)
	}
	if len(ep.Models) != 2 || ep.SelectedModel != "m1" || ep.APIKey != "${K}" {
		t.Fatalf("existing fields must be preserved: models=%v selected=%q key=%q", ep.Models, ep.SelectedModel, ep.APIKey)
	}
	// Fresh endpoint still works.
	if err := c.AddEndpoint("v", "e2", "", "https://x", ""); err != nil {
		t.Fatal(err)
	}
	if c.Vendors["v"].Endpoints["e2"].BaseURL != "https://x" {
		t.Fatal("new endpoint must be created")
	}
}

// Regression for #1868 case 3: a failed delete must be able to roll the
// tombstone back, or the yaml entry that survived on disk stays hidden by
// the tombstone (split state) and re-adding the name revives stale fields.
func TestMCPTombstoneRollback(t *testing.T) {
	c := &Config{}
	c.RecordMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 || c.DeletedMCPServers[0] != "srv" {
		t.Fatalf("tombstone must be recorded, got %v", c.DeletedMCPServers)
	}
	// Idempotent double-record (failure paths may re-record).
	c.RecordMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 {
		t.Fatalf("double record must be idempotent, got %v", c.DeletedMCPServers)
	}
	// Rollback removes exactly the name.
	c.RecordMCPDeleted("other")
	c.ClearMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 || c.DeletedMCPServers[0] != "other" {
		t.Fatalf("rollback must remove only the target, got %v", c.DeletedMCPServers)
	}
	// Clearing a name that is not tombstoned is a no-op.
	c.ClearMCPDeleted("absent")
	if len(c.DeletedMCPServers) != 1 {
		t.Fatalf("clear of absent name must be a no-op, got %v", c.DeletedMCPServers)
	}
}

// sa-60: strict_tools endpoint options must survive endpoint resolution and
// land on ResolvedEndpoint, which is what provider/registry consumes to call
// SetStrictTools. YAML shape alone (strict_tools_test.go) does not cover this
// wiring.
func TestResolveEndpointSelectionStrictTools(t *testing.T) {
	cfg := testConfigWithVendor()
	vc := cfg.Vendors["zai"]
	vc.Endpoints["strict-ep"] = EndpointConfig{
		Protocol:         "openai",
		BaseURL:          "https://example.com",
		APIKey:           "sk-test",
		MaxTokens:        1024,
		StrictTools:      boolPtr(true),
		StrictToolsAllow: []string{"grep"},
	}
	vc.Endpoints["plain-ep"] = EndpointConfig{
		Protocol:  "openai",
		BaseURL:   "https://example.com",
		APIKey:    "sk-test",
		MaxTokens: 1024,
	}
	cfg.Vendors["zai"] = vc

	cfg.Vendor = "zai"
	cfg.Endpoint = "strict-ep"
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.StrictTools {
		t.Fatalf("resolved.StrictTools = false, want true")
	}
	if len(resolved.StrictToolsAllow) != 1 || resolved.StrictToolsAllow[0] != "grep" {
		t.Fatalf("resolved.StrictToolsAllow = %v, want [grep]", resolved.StrictToolsAllow)
	}

	// Endpoint without the knob stays disabled (nil => false, no allowlist).
	cfg.Endpoint = "plain-ep"
	resolved, err = cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.StrictTools {
		t.Fatalf("strict_tools must default to false")
	}
	if len(resolved.StrictToolsAllow) != 0 {
		t.Fatalf("strict_tools_allow must default to empty, got %v", resolved.StrictToolsAllow)
	}
}

// sa-78: LLM call policy resolution - per-model override wins over the
// endpoint-level default; unset values fall back to zero (no deadline /
// provider-wide retry budget).
func TestResolveEndpointSelection_CallPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "acme"
	cfg.Endpoint = "relay"
	cfg.Model = "gpt-5-nano"
	cfg.Vendors["acme"] = VendorConfig{
		APIKey: "sk-test",
		Endpoints: map[string]EndpointConfig{
			"relay": {
				Protocol:       "openai",
				BaseURL:        "https://relay.example.com/v1",
				MaxTokens:      1024,
				RequestTimeout: 2 * time.Minute,
				MaxRetries:     4,
				ModelLimits: map[string]ModelLimitConfig{
					"gpt-5-nano": {RequestTimeout: 30 * time.Second, MaxRetries: 1},
				},
			},
		},
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.RequestTimeout != 30*time.Second {
		t.Errorf("per-model override: RequestTimeout = %v, want 30s", resolved.RequestTimeout)
	}
	if resolved.MaxRetries != 1 {
		t.Errorf("per-model override: MaxRetries = %d, want 1", resolved.MaxRetries)
	}

	// A model without a model_limits entry falls back to endpoint-level.
	cfg.Model = "gpt-5-mini"
	resolved, err = cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() (no model_limits entry) error = %v", err)
	}
	if resolved.RequestTimeout != 2*time.Minute {
		t.Errorf("endpoint fallback: RequestTimeout = %v, want 2m", resolved.RequestTimeout)
	}
	if resolved.MaxRetries != 4 {
		t.Errorf("endpoint fallback: MaxRetries = %d, want 4", resolved.MaxRetries)
	}

	// No policy configured anywhere: zero values (opt-in default behavior).
	relay := cfg.Vendors["acme"].Endpoints["relay"]
	relay.RequestTimeout = 0
	relay.MaxRetries = 0
	cfg.Vendors["acme"].Endpoints["relay"] = relay
	resolved, err = cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() (unset policy) error = %v", err)
	}
	if resolved.RequestTimeout != 0 || resolved.MaxRetries != 0 {
		t.Errorf("unset policy: got RequestTimeout=%v MaxRetries=%d, want 0/0", resolved.RequestTimeout, resolved.MaxRetries)
	}
}

// sa-73: responses_background must survive yaml parsing and endpoint
// resolution so the registry can turn background mode on for OpenAI
// Responses endpoints.
func TestResponsesBackgroundWiring(t *testing.T) {
	yamlSrc := `
vendors:
  openai:
    display_name: OpenAI
    endpoints:
      default:
        protocol: openai-responses
        base_url: https://api.openai.com/v1
        default_model: gpt-5.2-pro
        responses_background: true
      sync:
        protocol: openai-responses
        base_url: https://api.openai.com/v1
        default_model: gpt-5-codex
`
	c := &Config{}
	if err := yaml.Unmarshal([]byte(yamlSrc), c); err != nil {
		t.Fatal(err)
	}
	on, err := c.ResolveEndpoint("openai", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !on.ResponsesBackground {
		t.Fatal("responses_background: true must resolve into the endpoint")
	}
	off, err := c.ResolveEndpoint("openai", "sync")
	if err != nil {
		t.Fatal(err)
	}
	if off.ResponsesBackground {
		t.Fatal("endpoints without the flag must default to synchronous mode")
	}
}
