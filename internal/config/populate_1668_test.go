package config

import "testing"

// #1668 case 3 pin: an unknown vendor's endpoints each get the provider
// list their OWN BaseURL matches - not the vendor's first sorted URL.
func Test1668PerEndpointAttribution(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"my-gateway": {
			Endpoints: map[string]EndpointConfig{
				"a": {BaseURL: "https://api.z.ai/v1"},
				"b": {BaseURL: "https://api.deepseek.com/v1"},
			},
		},
	}}
	populateDefaultModels(cfg)
	epA := cfg.Vendors["my-gateway"].Endpoints["a"]
	epB := cfg.Vendors["my-gateway"].Endpoints["b"]
	if len(epA.Models) == 0 {
		t.Fatal("endpoint a (z.ai URL) must receive the matched provider's list")
	}
	if len(epB.Models) == 0 {
		t.Fatal("endpoint b (deepseek URL) must receive the matched provider's list")
	}
	hasGLM, hasDeep := false, false
	for _, m := range epA.Models {
		if len(m) > 3 && m[:3] == "glm" {
			hasGLM = true
		}
	}
	for _, m := range epB.Models {
		if len(m) >= 8 && m[:8] == "deepseek" {
			hasDeep = true
		}
	}
	if !hasGLM || !hasDeep {
		t.Fatalf("per-endpoint attribution wrong: endpoint a hasGLM=%v, endpoint b hasDeep=%v", hasGLM, hasDeep)
	}
}
