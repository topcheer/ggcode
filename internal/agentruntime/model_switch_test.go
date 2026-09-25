package agentruntime

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Regression for #1487: ActivateCurrentSelection rewrote the active selection
// via SetActiveSelection BEFORE validation ran, and a failed switch left cfg
// poisoned at the broken choice - later provider rebuilds then failed against
// it, taking down the previously working provider.
func TestActivateCurrentSelectionRollsBackOnResolveFailure(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "v1",
		Endpoint: "e1",
		Model:    "m1",
		Vendors: map[string]config.VendorConfig{
			"v1": {Endpoints: map[string]config.EndpointConfig{
				"e1": {APIKey: "k1", BaseURL: "https://one.test", SelectedModel: "m1"},
			}},
			// v2/e2 resolves but has no API key -> Resolve fails AFTER
			// SetActiveSelection already rewrote the selection.
			"v2": {Endpoints: map[string]config.EndpointConfig{
				"e2": {BaseURL: "https://two.test", SelectedModel: "m2"},
			}},
		},
	}
	if _, _, err := ActivateCurrentSelection(cfg, "v2", "e2", ""); err == nil {
		t.Fatal("switch to keyless endpoint must fail")
	}
	if cfg.Vendor != "v1" || cfg.Endpoint != "e1" || cfg.Model != "m1" {
		t.Fatalf("selection not rolled back: vendor=%q endpoint=%q model=%q, want v1/e1/m1",
			cfg.Vendor, cfg.Endpoint, cfg.Model)
	}
	if sel := cfg.Vendors["v2"].Endpoints["e2"].SelectedModel; sel != "m2" {
		t.Fatalf("endpoint SelectedModel must be restored to its pre-switch value: %q, want m2", sel)
	}
}

// fakeApplyProvider implements just enough of provider.Provider for
// ApplyProviderToAgent (embedded nil interface keeps it inert).
type fakeApplyProvider struct {
	provider.Provider
}

func (f *fakeApplyProvider) Name() string { return "fake-apply" }
func (f *fakeApplyProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("ok")}}}, nil
}

// TestApplyProviderToAgentGatesMemoryTool pins #2511: the agent-side memory
// executor is enabled iff the resolved endpoint is anthropic-protocol AND
// configured memory_tool: true - the same gate the provider registry uses to
// declare the tool. Any other combination (non-anthropic protocol, or the
// flag unset) must leave the executor disabled so hallucinated name="memory"
// calls fall through to UnknownToolError.
func TestApplyProviderToAgentGatesMemoryTool(t *testing.T) {
	prov := &fakeApplyProvider{}
	base := config.ResolvedEndpoint{Protocol: "anthropic", Model: "m1"}

	cases := []struct {
		name     string
		protocol string
		memTool  bool
		wantOn   bool
	}{
		{"anthropic + memory_tool", "anthropic", true, true},
		{"anthropic without memory_tool", "anthropic", false, false},
		{"openai with memory_tool flag set", "openai", true, false},
		{"openai without memory_tool", "openai", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
			r := base
			r.Protocol = tc.protocol
			r.MemoryTool = tc.memTool
			ApplyProviderToAgent(a, prov, &r, nil)
			if got := a.MemoryToolEnabled(); got != tc.wantOn {
				t.Fatalf("MemoryToolEnabled()=%v, want %v (protocol=%q memory_tool=%v)",
					got, tc.wantOn, tc.protocol, tc.memTool)
			}
		})
	}
}

// TestResolveCurrentSelectionWrapsPureFallbacksArray pins #1674 case 2: a
// config with ONLY the modern fallbacks array (no legacy single entry) must
// still get a FallbackProvider wrapper - the old guard checked
// Fallback.IsConfigured() alone and silently skipped the wrap.
func TestResolveCurrentSelectionWrapsPureFallbacksArray(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "v1",
		Endpoint: "e1",
		Model:    "m1",
		// No legacy cfg.Fallback at all.
		Fallbacks: []config.FallbackConfig{
			{Enabled: true, Vendor: "v2", Endpoint: "e2", Model: "m2"},
		},
		Vendors: map[string]config.VendorConfig{
			"v1": {Endpoints: map[string]config.EndpointConfig{
				"e1": {APIKey: "k1", BaseURL: "https://one.test", Protocol: "openai", Models: []string{"m1"}},
			}},
			"v2": {Endpoints: map[string]config.EndpointConfig{
				"e2": {APIKey: "k2", BaseURL: "https://two.test", Protocol: "openai", SelectedModel: "m2", Models: []string{"m2"}},
			}},
		},
	}
	_, prov, err := ResolveCurrentSelection(cfg)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if _, ok := prov.(*provider.FallbackProvider); !ok {
		t.Fatalf("pure-fallbacks-array config must yield a FallbackProvider wrapper, got %T", prov)
	}
}
