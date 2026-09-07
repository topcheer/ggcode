package config

import "testing"

func TestAIGatewayEndpointsGetOwnModels(t *testing.T) {
	cfg := DefaultConfig()
	gw, ok := cfg.Vendors["ai-gateway"]
	if !ok {
		t.Fatal("ai-gateway vendor missing")
	}
	checked := 0
	for name, ep := range gw.Endpoints {
		if len(ep.Models) == 0 {
			continue
		}
		checked++
		t.Logf("%s: %d models (first: %s)", name, len(ep.Models), ep.Models[0])
	}
	if checked < 12 {
		t.Fatalf("expected >=12 populated gateway endpoints, got %d", checked)
	}
	// Distinctness: 302.ai and OpenRouter must NOT share aihubmix's list.
	or := gw.Endpoints["openrouter"].Models
	ai := gw.Endpoints["302ai"].Models
	if len(or) > 0 && len(ai) > 0 && or[0] == ai[0] {
		t.Fatalf("openrouter and 302ai look like they share one list")
	}
}
