//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue585_PlatformSwitchDiscardsOldExtra verifies that SaveIMAdapter
// discards old Extra fields when the platform changes (Bug I1).
// This prevents cross-platform credential leakage (e.g., telegram bot_token
// becoming slack's bot_token).
func TestIssue585_PlatformSwitchDiscardsOldExtra(t *testing.T) {
	tmpDir := t.TempDir()

	// Create temporary config directory
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	// Initial config with telegram adapter having bot_token
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["test-adapter"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": "111:TELEGRAM_SECRET"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	// Switch platform to slack WITHOUT providing bot_token
	// This simulates the UI edit flow where secret fields are masked and not sent
	values := map[string]string{
		"platform": "slack",
		"enabled":  "true",
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

	// Reload and verify: platform changed to slack, but bot_token was NOT inherited
	cfg2, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	acfg, ok := cfg2.IM.Adapters["test-adapter"]
	if !ok {
		t.Fatal("adapter not found after update")
	}
	if acfg.Platform != "slack" {
		t.Errorf("expected platform=slack, got %s", acfg.Platform)
	}
	if acfg.Extra != nil {
		if token, ok := acfg.Extra["bot_token"]; ok {
			t.Errorf("bot_token should not exist after platform switch (cross-platform credential leak), got: %v", token)
		}
	}
}

// TestIssue585_SamePlatformPreservesExtra verifies that SaveIMAdapter
// preserves existing Extra fields when editing the same platform (Bug I1 negative test).
// This ensures the fix doesn't break the existing correct behavior for same-platform edits.
func TestIssue585_SamePlatformPreservesExtra(t *testing.T) {
	tmpDir := t.TempDir()

	// Create temporary config directory
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	// Initial config with telegram adapter having bot_token
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["test-adapter"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": "111:TELEGRAM_SECRET"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	// Edit same platform (telegram) WITHOUT providing bot_token
	// This simulates the UI edit flow where secret fields are masked and not sent
	values := map[string]string{
		"platform": "telegram",
		"enabled":  "false", // Just changing enabled state
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

	// Reload and verify: platform unchanged, bot_token WAS preserved
	cfg2, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	acfg, ok := cfg2.IM.Adapters["test-adapter"]
	if !ok {
		t.Fatal("adapter not found after update")
	}
	if acfg.Platform != "telegram" {
		t.Errorf("expected platform=telegram, got %s", acfg.Platform)
	}
	if acfg.Enabled {
		t.Errorf("expected enabled=false, got true")
	}
	if acfg.Extra == nil {
		t.Fatal("Extra should not be nil for same-platform edit")
	}
	token, ok := acfg.Extra["bot_token"]
	if !ok {
		t.Fatal("bot_token should be preserved for same-platform edit")
	}
	if token != "111:TELEGRAM_SECRET" {
		t.Errorf("expected bot_token=111:TELEGRAM_SECRET, got %v", token)
	}
}

// TestIssue585_TestIMConnectionValidatesRequiredFields verifies that
// TestIMConnection fails when required fields are missing or empty (Bug I2).
func TestIssue585_TestIMConnectionValidatesRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	// Create temporary config directory
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	// Test missing required field
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["telegram-missing-token"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{}, // Empty Extra - bot_token missing
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-missing-token")
	if err == nil {
		t.Error("TestIMConnection should fail when required field bot_token is missing")
	}
	t.Logf("Expected error for missing bot_token: %v", err)

	// Test empty required field
	cfg.IM.Adapters["telegram-empty-token"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": ""}, // Empty string
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-empty-token")
	if err == nil {
		t.Error("TestIMConnection should fail when required field bot_token is empty")
	}
	t.Logf("Expected error for empty bot_token: %v", err)

	// Test bogus token (non-empty but invalid) - should PASS field validation
	// (connectivity validation requires actual adapter runtime)
	cfg.IM.Adapters["telegram-bogus-token"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": "totally-bogus"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-bogus-token")
	if err != nil {
		t.Errorf("TestIMConnection should pass field validation for non-empty bot_token (actual connectivity validation requires adapter runtime): %v", err)
	}
}

// TestIssue585_TestIMConnectionSlackMultipleRequiredFields verifies that
// TestIMConnection validates multiple required fields for platforms like Slack.
func TestIssue585_TestIMConnectionSlackMultipleRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	// Create temporary config directory
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	// Test slack with only bot_token (missing app_token)
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["slack-partial"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "slack",
		Extra:    map[string]interface{}{"bot_token": "xoxb-test"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("slack-partial")
	if err == nil {
		t.Error("TestIMConnection should fail when required field app_token is missing for Slack")
	}
	t.Logf("Expected error for missing app_token: %v", err)

	// Test slack with all required fields present
	cfg.IM.Adapters["slack-complete"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "slack",
		Extra: map[string]interface{}{
			"bot_token": "xoxb-test",
			"app_token": "xapp-test",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("slack-complete")
	if err != nil {
		t.Errorf("TestIMConnection should pass field validation for Slack with all required fields: %v", err)
	}
}

// TestIssue585_PlatformSwitchTelegramToSlackIsolated confirms the
// specific probe case from issue #585: telegram bot_token should NOT become
// slack bot_token after platform switch.
func TestIssue585_PlatformSwitchTelegramToSlackIsolated(t *testing.T) {
	tmpDir := t.TempDir()

	// Create temporary config directory
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	// Exact probe scenario: start with telegram adapter
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["test-adapter"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": "111:TELEGRAM_SECRET"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save initial config: %v", err)
	}

	// Switch to slack without any bot_token in the update
	// (simulates #107 secret field masking behavior)
	values := map[string]string{
		"platform": "slack",
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

	// Verify: slack has NO bot_token (old telegram token was discarded)
	cfg2, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	acfg, ok := cfg2.IM.Adapters["test-adapter"]
	if !ok {
		t.Fatal("adapter not found after platform switch")
	}
	if acfg.Platform != "slack" {
		t.Fatalf("expected platform=slack, got %s", acfg.Platform)
	}

	// This is the critical check: bot_token should NOT exist
	if acfg.Extra != nil {
		if token, exists := acfg.Extra["bot_token"]; exists {
			t.Fatalf("CROSS-PLATFORM CREDENTIAL LEAK: telegram bot_token=%v became slack bot_token", token)
		}
	}
}
