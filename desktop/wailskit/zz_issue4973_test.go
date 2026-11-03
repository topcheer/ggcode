package wailskit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

const issue4973YAML = `vendor: alpha
endpoint: main
model: modelB
vendors:
  alpha:
    endpoints:
      main:
        protocol: anthropic
        base_url: https://a.example.com
        defaultModel: modelB
  gamma:
    endpoints:
      alt:
        protocol: anthropic
        base_url: https://c.example.com
        defaultModel: modelD
`

// TestIssue4973ProviderSwitchRevertsAfterSync guards the end-to-end path:
// settings save -> UpdateConfig -> ticker reload -> resolved stays on the new
// selection (no revert to the old provider).
func TestIssue4973ProviderSwitchRevertsAfterSync(t *testing.T) {
	if os.Getenv("GGCODE_TEST_SLOW") == "" {
		t.Skip("set GGCODE_TEST_SLOW=1 to run the 2s-ticker reproduction")
	}
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")
	if err := os.WriteFile(mainPath, []byte(issue4973YAML), 0o600); err != nil {
		t.Fatalf("write main config: %v", err)
	}

	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	SetConfig(cfg)

	if err := UpdateConfig(map[string]interface{}{
		"vendor":   "gamma",
		"endpoint": "alt",
		"model":    "modelD",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	res1, err := GetResolvedEndpoint()
	if err != nil {
		t.Fatalf("GetResolvedEndpoint #1: %v", err)
	}
	if res1.VendorID != "gamma" || res1.Model != "modelD" {
		t.Fatalf("immediate resolve should be gamma/alt/modelD, got %s/%s/%s", res1.VendorID, res1.EndpointID, res1.Model)
	}

	time.Sleep(3 * time.Second)

	res2, err := GetResolvedEndpoint()
	if err != nil {
		t.Fatalf("GetResolvedEndpoint #2: %v", err)
	}
	if res2.VendorID != "gamma" || res2.Model != "modelD" {
		t.Fatalf("REVERTED (#4973): expected gamma/alt/modelD after ticker, got %s/%s/%s", res2.VendorID, res2.EndpointID, res2.Model)
	}
}

// TestIssue4973BridgeStaleSnapshotRevertsSession reproduces the actual root
// cause: the ChatBridge holds a one-time snapshot of globalCfg; once the
// configFileSync ticker swaps globalCfg for a freshly loaded object, the
// bridge's stale snapshot made OnConfigProviderChanged resolve the OLD
// provider and write it back into the live session (the UI "reverts" to A/B
// right after saving C/D). The refreshConfigSnapshot re-sync fixes it.
func TestIssue4973BridgeStaleSnapshotRevertsSession(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")
	if err := os.WriteFile(mainPath, []byte(issue4973YAML), 0o600); err != nil {
		t.Fatalf("write main config: %v", err)
	}

	cfgOld, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	SetConfig(cfgOld)

	// Bridge captured at startup: snapshot == cfgOld (alpha/main/modelB).
	b := &ChatBridge{cfg: cfgOld}
	ses := &session.Session{Vendor: "alpha", Endpoint: "main", Model: "modelB"}
	b.mu.Lock()
	b.currentSes = ses
	b.mu.Unlock()
	defer SetChatBridge(nil)
	SetChatBridge(b)

	// Simulate an external file change: ticker reloads from disk and replaces
	// globalCfg with a fresh object (same on-disk values, new pointer).
	cfgFresh, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load fresh: %v", err)
	}
	SetConfig(cfgFresh)
	if GetGlobalConfig() == cfgOld {
		t.Fatal("test setup: fresh pointer should differ from old snapshot")
	}

	// User saves C/D in settings: UpdateConfig mutates globalCfg (fresh)...
	if err := UpdateConfig(map[string]interface{}{
		"vendor":   "gamma",
		"endpoint": "alt",
		"model":    "modelD",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	// ...and the app then notifies the bridge.
	b.OnConfigProviderChanged()

	// Without the fix the session was rewritten with the STALE selection.
	b.mu.Lock()
	gotVendor, gotEndpoint, gotModel := ses.Vendor, ses.Endpoint, ses.Model
	b.mu.Unlock()
	if gotVendor != "gamma" || gotEndpoint != "alt" || gotModel != "modelD" {
		t.Fatalf("session reverted to stale selection (#4973): got %s/%s/%s, want gamma/alt/modelD", gotVendor, gotEndpoint, gotModel)
	}
}

// TestIssue4973ModelKeptOnProviderSwitch covers the follow-up report: after
// switching vendor kimi->zai with model glm-5.3 selected in the dropdown, the
// resolved model must be glm-5.3 even when the zai endpoint carries a stale
// SelectedModel (k3-256k from an earlier session).
func TestIssue4973ModelKeptOnProviderSwitch(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")
	yaml := `vendor: kimi
endpoint: main
model: k3-256k
vendors:
  kimi:
    endpoints:
      main:
        protocol: anthropic
        base_url: https://kimi.example.com
        defaultModel: k3-256k
  zai:
    endpoints:
      cn:
        protocol: openai
        base_url: https://api.z.ai/api/coding/paas/v4
        defaultModel: k3-256k
        selectedModel: k3-256k
`
	if err := os.WriteFile(mainPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	SetConfig(cfg)

	if err := UpdateConfig(map[string]interface{}{
		"vendor":   "zai",
		"endpoint": "cn",
		"model":    "glm-5.3",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	res, err := GetResolvedEndpoint()
	if err != nil {
		t.Fatalf("GetResolvedEndpoint: %v", err)
	}
	t.Logf("resolved after switch: vendor=%s endpoint=%s model=%s", res.VendorID, res.EndpointID, res.Model)
	if res.VendorID != "zai" {
		t.Fatalf("vendor should be zai, got %s", res.VendorID)
	}
	if res.Model != "glm-5.3" {
		t.Fatalf("model should be glm-5.3 (explicit selection beats endpoint SelectedModel), got %s", res.Model)
	}
}

// TestIssue4973EmptyModelResetsToEndpointDefault covers the reported follow-up:
// the frontend can save a vendor/endpoint switch with model:"" (dropdown left
// on the placeholder). cfg.Model must reset to the new endpoint's selection
// instead of keeping the previous vendor's model string.
func TestIssue4973EmptyModelResetsToEndpointDefault(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")
	yaml := `vendor: kimi
endpoint: main
model: k3-256k
vendors:
  kimi:
    endpoints:
      main:
        protocol: anthropic
        base_url: https://kimi.example.com
        defaultModel: k3-256k
  zai:
    endpoints:
      cn:
        protocol: openai
        base_url: https://api.z.ai/api/coding/paas/v4
        default_model: glm-5.3
        selected_model: glm-5.3
`
	if err := os.WriteFile(mainPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	SetConfig(cfg)

	// Frontend saved with model:"" (placeholder still selected).
	if err := UpdateConfig(map[string]interface{}{
		"vendor":   "zai",
		"endpoint": "cn",
		"model":    "",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	res, err := GetResolvedEndpoint()
	if err != nil {
		t.Fatalf("GetResolvedEndpoint: %v", err)
	}
	t.Logf("resolved: vendor=%s endpoint=%s model=%s", res.VendorID, res.EndpointID, res.Model)
	if res.Model != "glm-5.3" {
		t.Fatalf("empty model on vendor switch must reset to endpoint selection glm-5.3, got %s (stale k3-256k leak = #4973)", res.Model)
	}
}
