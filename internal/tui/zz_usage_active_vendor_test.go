package tui

// Owner ruling 2026-09-15: the usage panel shows the CURRENT vendor only -
// the fleet view (every keyed vendor probed) read as garbage rows. And
// /usage must appear in slash completion (the #2356 cut shipped the
// command without registering it).

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestUsagePanelProbesActiveVendorOnly(t *testing.T) {
	var m Model
	m.config = &config.Config{Vendors: map[string]config.VendorConfig{
		"zai":        {Endpoints: map[string]config.EndpointConfig{"e": {APIKey: "k1"}}},
		"openrouter": {Endpoints: map[string]config.EndpointConfig{"e": {APIKey: "k2"}}},
	}}
	m.startupVendor = "zai"
	m.activeVendor = "zai"
	// no service adapters registered -> Has() false -> empty is fine; we
	// assert the CANDIDATE SET is filtered by active vendor before Has.
	// Direct check: probeableVendors must never return the non-active
	// keyed vendor.
	got := m.probeableVendors()
	for _, v := range got {
		if v != "zai" {
			t.Fatalf("probeableVendors returned non-active vendor %q", v)
		}
	}
	if len(got) > 1 {
		t.Fatalf("expected at most the active vendor, got %v", got)
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
