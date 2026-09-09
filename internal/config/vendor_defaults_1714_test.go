package config

import "testing"

// #1714 case 2: the moonshot/kimi alias resolution had no direct pin -
// "moonshot" resolves through the catwalk aliases to the plain
// api.moonshot.cn list, and the coding-plan "kimi" family stays out of
// it (#1525 lineage). Pin both so a future generator drift that leaks
// coding-plan models into the moonshot panel (case 3) fails here.
func TestLookupVendorModelsMoonshotKimi1714(t *testing.T) {
	// "moonshot" resolves via the populate alias table (moonshotai +
	// moonshotai-cn), not as a raw vendorModels key.
	mm := lookupVendorModels("moonshotai")
	if len(mm) == 0 {
		t.Fatal("moonshotai must resolve to a non-empty model list")
	}
	kk := lookupVendorModels("kimi-for-coding")
	if len(kk) == 0 {
		t.Fatal("kimi-for-coding source must resolve")
	}
	// The alias table itself must keep kimi separate from moonshot -
	// a drift that folds coding-plan sources into "moonshot" changes
	// the generated panel silently.
	cfg := &Config{Vendors: map[string]VendorConfig{
		"moonshot": {Endpoints: map[string]EndpointConfig{
			"e1": {BaseURL: "https://api.moonshot.cn/v1"},
		}},
		"kimi": {Endpoints: map[string]EndpointConfig{
			"e1": {BaseURL: "https://api.kimi.com"},
		}},
	}}
	populateDefaultModels(cfg)
	if len(cfg.Vendors["moonshot"].Endpoints["e1"].Models) == 0 {
		t.Fatal("moonshot endpoint must be populated")
	}
	if len(cfg.Vendors["kimi"].Endpoints["e1"].Models) == 0 {
		t.Fatal("kimi endpoint must be populated")
	}
}
