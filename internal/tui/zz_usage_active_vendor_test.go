package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestUsagePanelProbesByURLCurrentEndpointOnly(t *testing.T) {

	var m Model
	m.activeVendor = "mycustom"
	m.activeEndpoint = "e1"
	m.config = &config.Config{Vendors: map[string]config.VendorConfig{
		"mycustom": {Endpoints: map[string]config.EndpointConfig{
			"e1": {BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", APIKey: "k1", DefaultModel: "glm-5"},
		}},
		"openrouter": {Endpoints: map[string]config.EndpointConfig{
			"e": {BaseURL: "https://openrouter.ai/api/v1", APIKey: "k2"},
		}},
	}}
	m.ensureUsageService()
	got := m.probeableVendors()
	if len(got) != 1 || got[0] != "zai" {
		t.Fatalf("URL-matched probe = %v, want [zai]", got)
	}

	m2 := m
	m2.config.Vendors["mycustom"].Endpoints["e1"] = config.EndpointConfig{
		BaseURL: "https://internal-gw.corp/api", APIKey: "k1",
	}
	if got2 := m2.probeableVendors(); len(got2) != 0 {
		t.Fatalf("unknown-host vendor must not probe, got %v", got2)
	}

	m3 := m
	m3.config.Vendors["mycustom"].Endpoints["e1"] = config.EndpointConfig{
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKey: "",
	}
	m3.config.Vendors["mycustom"].Endpoints["sibling"] = config.EndpointConfig{
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKey: "k2",
	}
	if got3 := m3.probeableVendors(); len(got3) != 0 {
		t.Fatalf("keyless CURRENT endpoint must not probe (no sibling fallback), got %v", got3)
	}
}

func TestUsageSlashCompletionRegistered(t *testing.T) {
	found := false
	for _, c := range SlashCommands {
		if c == "/usage" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("/usage missing from SlashCommands completion list")
	}
	if !strings.Contains(SlashCommandDescriptions["/usage"], "vendor") {
		t.Fatal("/usage missing a description")
	}
}
