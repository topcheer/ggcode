//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue585_PlatformSwitchDiscardsOldExtra(t *testing.T) {
	tmpDir := t.TempDir()

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

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

	values := map[string]string{
		"platform": "slack",
		"enabled":  "true",
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

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

func TestIssue585_SamePlatformPreservesExtra(t *testing.T) {
	tmpDir := t.TempDir()

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

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

	values := map[string]string{
		"platform": "telegram",
		"enabled":  "false",
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

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

func TestIssue585_TestIMConnectionValidatesRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

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
		Extra:    map[string]interface{}{},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-missing-token")
	if err == nil {
		t.Error("TestIMConnection should fail when required field bot_token is missing")
	}
	t.Logf("Expected error for missing bot_token: %v", err)

	cfg.IM.Adapters["telegram-empty-token"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": ""},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-empty-token")
	if err == nil {
		t.Error("TestIMConnection should fail when required field bot_token is empty")
	}
	t.Logf("Expected error for empty bot_token: %v", err)

	cfg.IM.Adapters["telegram-nil-token"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra:    map[string]interface{}{"bot_token": nil},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err = TestIMConnection("telegram-nil-token")
	if err == nil {
		t.Error("TestIMConnection should fail when required field bot_token is yaml null (#1026)")
	}
	t.Logf("Expected error for nil bot_token: %v", err)

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

func TestIssue585_TestIMConnectionSlackMultipleRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

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

func TestIssue585_PlatformSwitchTelegramToSlackIsolated(t *testing.T) {
	tmpDir := t.TempDir()

	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

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

	values := map[string]string{
		"platform": "slack",
	}
	if err := SaveIMAdapter("test-adapter", values); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}

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

	if acfg.Extra != nil {
		if token, exists := acfg.Extra["bot_token"]; exists {
			t.Fatalf("CROSS-PLATFORM CREDENTIAL LEAK: telegram bot_token=%v became slack bot_token", token)
		}
	}
}
