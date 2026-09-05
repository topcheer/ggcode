package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
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
