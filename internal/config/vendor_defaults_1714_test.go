package config

import "testing"

func TestLookupVendorModelsMoonshotKimi1714(t *testing.T) {

	mm := lookupVendorModels("moonshotai")
	if len(mm) == 0 {
		t.Fatal("moonshotai must resolve to a non-empty model list")
	}
	kk := lookupVendorModels("kimi-for-coding")
	if len(kk) == 0 {
		t.Fatal("kimi-for-coding source must resolve")
	}

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
