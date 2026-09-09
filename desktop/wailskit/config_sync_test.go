package wailskit

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #1847 case 1/3: an EXTERNAL change to the config file must reach
// globalCfg via the sync poller, and a subsequent in-process save must
// not roll it back (stale non-zero snapshot deep-merge).
func TestConfigFileSyncPollerPicksUpExternalChange1847(t *testing.T) {
	cfgPath, _ := setupConfigTestEnv(t, "")
	cfg := GetGlobalConfig()
	cfg.Vendor = "deskvendor"
	cfg.Endpoint = "main"
	cfg.Vendors = map[string]config.VendorConfig{
		"deskvendor": {Endpoints: map[string]config.EndpointConfig{
			"main": {BaseURL: "https://example.com", Protocol: "openai"},
		}},
	}
	cfg.DefaultMode = "auto"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	globalMu.Lock()
	noteConfigFileSaved()
	globalMu.Unlock()

	// External session (TUI/CLI) patches the file AFTER our last save.
	time.Sleep(50 * time.Millisecond)
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	vendorsBlock := "vendors:\n  deskvendor:\n    endpoints:\n      main:\n        base_url: https://example.com\n        protocol: openai\n"
	patched := strings.Replace(string(raw), "default_mode: auto", "default_mode: plan", 1)
	if !strings.Contains(patched, "default_mode: plan") {
		patched += "default_mode: plan\n"
	}
	if !strings.Contains(patched, "vendors:") {
		patched += vendorsBlock
	}
	if err := os.WriteFile(cfgPath, []byte(patched), 0644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	// Poller window: force a sync cycle.
	globalMu.Lock()
	syncCfgFileLocked()
	globalMu.Unlock()

	if got := GetGlobalConfig().DefaultMode; got != "plan" {
		t.Fatalf("poller must refresh external change, default_mode=%q", got)
	}
	// A delta save from the desktop now preserves the external value.
	if err := UpdateConfig(map[string]interface{}{"model": "m2"}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	re, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if re.DefaultMode != "plan" {
		t.Fatalf("desktop save rolled back external change: default_mode=%q", re.DefaultMode)
	}
}
