package wailskit

// Regression (r150): SaveAPIKey must refresh the running chat session's
// provider after persisting the new key. Providers only read credentials at
// construction time (see the #616 comment on CompleteAnthropicOAuth), so
// without the refresh the active conversation kept using the OLD key after a
// Settings rotation - requests kept failing with 401 until restart or a
// model switch, while the UI showed the new key as saved. The fix makes
// SaveAPIKey symmetric with App.UpdateConfig's post-save
// OnConfigProviderChanged and the #616/#670 OAuth refresh paths.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// minimalSelection installs an active vendor/endpoint selection with a single
// endpoint so cfg.Save() (called inside SaveAPIKey) passes its active-choice
// validation and the provider rebuild has something resolvable.
func minimalSelection(t *testing.T) {
	t.Helper()
	cfg := GetGlobalConfig()
	cfg.Vendor = "testv"
	cfg.Endpoint = "main"
	cfg.Model = "m1"
	cfg.Vendors = map[string]config.VendorConfig{
		"testv": {Endpoints: map[string]config.EndpointConfig{
			"main": {BaseURL: "https://example.com", Protocol: "openai"},
		}},
	}
}

func TestSaveAPIKeyRefreshesRunningProvider(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	minimalSelection(t)

	var events []string
	bridge := &ChatBridge{cfg: GetGlobalConfig()}
	bridge.EmitEvent = func(name string, _ ...interface{}) { events = append(events, name) }
	SetChatBridge(bridge)
	defer SetChatBridge(nil)

	if err := SaveAPIKey("testv", "main", "sk-NEW"); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	var refreshed bool
	for _, e := range events {
		if e == "config:updated" {
			refreshed = true
			break
		}
	}
	if !refreshed {
		t.Fatal("r150 regression: SaveAPIKey did not refresh the running provider (active session would keep using the old key until restart)")
	}
	if bridge.resolved == nil {
		t.Fatal("r150 regression: bridge.resolved not synced after SaveAPIKey")
	}
}

// A failed save must NOT trigger the refresh: the provider keeps its current
// (working) credential instead of being rebuilt against a change that never
// persisted.
func TestSaveAPIKeyFailureSkipsProviderRefresh(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")

	var events []string
	bridge := &ChatBridge{cfg: GetGlobalConfig()}
	bridge.EmitEvent = func(name string, _ ...interface{}) { events = append(events, name) }
	SetChatBridge(bridge)
	defer SetChatBridge(nil)

	if err := SaveAPIKey("no-such-vendor", "main", "sk-X"); err == nil {
		t.Fatal("expected error saving a key for an unknown vendor")
	}
	if len(events) != 0 {
		t.Fatalf("provider refresh fired on a failed save: %v", events)
	}
}

// A desktop process with no chat session yet must be unaffected: the refresh
// degrades to a safe no-op while the key write itself still succeeds.
func TestSaveAPIKeyNoBridgeNoop(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")
	minimalSelection(t)
	SetChatBridge(nil)

	if err := SaveAPIKey("testv", "main", "sk-NEW"); err != nil {
		t.Fatalf("SaveAPIKey without a registered bridge: %v", err)
	}
}
