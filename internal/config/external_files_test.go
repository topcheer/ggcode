package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMCPServers_RoundTripThroughExternalFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	cfg.MCPServers = []MCPServerConfig{
		{Name: "srv-a", Type: "stdio", Command: "npx", Args: []string{"-y", "srv-a"}},
		{Name: "srv-b", Type: "http", URL: "https://example.com/mcp"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	mcpData, err := os.ReadFile(MCPServersPath(tmpDir))
	if err != nil {
		t.Fatalf("mcp_servers.yaml should exist: %v", err)
	}
	mcpStr := string(mcpData)
	if !contains(mcpStr, "srv-a") || !contains(mcpStr, "srv-b") {
		t.Errorf("mcp_servers.yaml should contain both servers:\n%s", mcpStr)
	}

	cfgData, _ := os.ReadFile(cfgPath)
	if contains(string(cfgData), "mcp_servers") {
		t.Errorf("main config should NOT contain mcp_servers:\n%s", string(cfgData))
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Reload error: %v", err)
	}
	if len(cfg2.MCPServers) != 2 {
		t.Fatalf("expected 2 servers after reload, got %d: %+v", len(cfg2.MCPServers), cfg2.MCPServers)
	}
	foundA, foundB := false, false
	for _, s := range cfg2.MCPServers {
		if s.Name == "srv-a" && s.Command == "npx" {
			foundA = true
		}
		if s.Name == "srv-b" && s.URL == "https://example.com/mcp" {
			foundB = true
		}
	}
	if !foundA {
		t.Error("srv-a not found after reload")
	}
	if !foundB {
		t.Error("srv-b not found after reload")
	}
}

func TestMCPServers_EmptyRemovesExternalFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)
	cfg.MCPServers = []MCPServerConfig{
		{Name: "temp-srv", Type: "stdio", Command: "echo"},
	}
	cfg.Save()

	mcpPath := MCPServersPath(tmpDir)
	if !fileExists(mcpPath) {
		t.Fatal("mcp_servers.yaml should exist after saving servers")
	}

	cfg2, _ := Load(cfgPath)
	cfg2.MCPServers = nil
	cfg2.Save()

	if fileExists(mcpPath) {
		t.Error("mcp_servers.yaml should be removed when no servers remain")
	}
}

func TestMCPServers_EnvExpansion(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")
	t.Setenv("TEST_MCP_KEY", "secret-value-123")

	os.WriteFile(cfgPath, []byte("language: en\n"), 0644)

	mcpPath := MCPServersPath(tmpDir)
	os.WriteFile(mcpPath, []byte(`- name: env-srv
  type: stdio
  command: npx
  args:
    - -y
    - env-srv
  env:
    API_KEY: ${TEST_MCP_KEY}
`), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.MCPServers))
	}
	srv := cfg.MCPServers[0]
	if srv.Name != "env-srv" {
		t.Errorf("expected name env-srv, got %s", srv.Name)
	}
	if got := srv.Env["API_KEY"]; got != "secret-value-123" {
		t.Errorf("expected API_KEY expanded to 'secret-value-123', got %q", got)
	}
}

func TestMCPMigration_FromMainConfig(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	oldContent := `language: en
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
mcp_servers:
  - name: legacy-srv
    type: stdio
    command: npx
    args:
      - -y
      - legacy-srv
`
	os.WriteFile(cfgPath, []byte(oldContent), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "legacy-srv" {
		t.Fatalf("expected legacy-srv in config, got %+v", cfg.MCPServers)
	}

	mcpPath := MCPServersPath(tmpDir)
	if !fileExists(mcpPath) {
		t.Fatal("mcp_servers.yaml should be created during migration")
	}

	cfgData, _ := os.ReadFile(cfgPath)
	if contains(string(cfgData), "mcp_servers") {
		t.Errorf("main config should NOT have mcp_servers after migration:\n%s", string(cfgData))
	}

	cfg2, _ := Load(cfgPath)
	if len(cfg2.MCPServers) != 1 || cfg2.MCPServers[0].Name != "legacy-srv" {
		t.Fatalf("expected legacy-srv after reload, got %+v", cfg2.MCPServers)
	}
}

func TestIMConfig_RoundTripThroughExternalFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)
	cfg.IM.Enabled = true
	cfg.IM.Adapters = map[string]IMAdapterConfig{
		"test-bot": {
			Enabled:  true,
			Platform: "qq",
			Extra:    map[string]interface{}{"appid": "123"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	imPath := IMPath(tmpDir)
	if !fileExists(imPath) {
		t.Fatal("im.yaml should exist")
	}

	cfgData, _ := os.ReadFile(cfgPath)
	if contains(string(cfgData), "adapters") {
		t.Errorf("main config should NOT contain im.adapters:\n%s", string(cfgData))
	}

	cfg2, _ := Load(cfgPath)
	if !cfg2.IM.Enabled {
		t.Error("IM should be enabled after reload")
	}
	adapter, ok := cfg2.IM.Adapters["test-bot"]
	if !ok {
		t.Fatalf("test-bot adapter should survive reload")
	}
	if adapter.Platform != "qq" {
		t.Errorf("expected platform qq, got %s", adapter.Platform)
	}
}

func TestIMConfig_EmptyRemovesExternalFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)
	cfg.IM.Enabled = true
	cfg.Save()

	imPath := IMPath(tmpDir)
	if !fileExists(imPath) {
		t.Fatal("im.yaml should exist")
	}

	cfg2, _ := Load(cfgPath)
	cfg2.IM = IMConfig{}
	cfg2.Save()

	if fileExists(imPath) {
		t.Error("im.yaml should be removed when IM is empty/default")
	}
}

func TestIMMigration_FromMainConfig(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	oldContent := `language: en
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
im:
  enabled: true
  adapters:
    legacy-bot:
      platform: slack
      enabled: true
`
	os.WriteFile(cfgPath, []byte(oldContent), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if !cfg.IM.Enabled {
		t.Error("IM should be enabled")
	}
	adapter, ok := cfg.IM.Adapters["legacy-bot"]
	if !ok || adapter.Platform != "slack" {
		t.Fatalf("legacy-bot adapter should exist after load: %+v", cfg.IM.Adapters)
	}

	imPath := IMPath(tmpDir)
	if !fileExists(imPath) {
		t.Fatal("im.yaml should be created during migration")
	}

	cfgData, _ := os.ReadFile(cfgPath)
	if contains(string(cfgData), "legacy-bot") {
		t.Errorf("main config should NOT contain im section after migration:\n%s", string(cfgData))
	}

	cfg2, _ := Load(cfgPath)
	if !cfg2.IM.Enabled {
		t.Error("IM should be enabled after reload")
	}
	if _, ok := cfg2.IM.Adapters["legacy-bot"]; !ok {
		t.Error("legacy-bot should survive reload")
	}
}

func TestVendorsMigration_FromMainConfig(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg := DefaultConfig()
	cfg.Language = "en"
	cfg.Vendors["my-migration-vendor"] = VendorConfig{
		DisplayName: "Migration Test",
		APIKey:      "${MIGRATION_KEY}",
		Endpoints: map[string]EndpointConfig{
			"prod": {
				Protocol: "openai",
				BaseURL:  "https://migration.example.com/v1",
				Models:   []string{"m1", "m2"},
			},
		},
	}
	oldData, _ := yaml.Marshal(cfg)
	os.WriteFile(cfgPath, oldData, 0644)

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	vc, ok := cfg2.Vendors["my-migration-vendor"]
	if !ok {
		t.Fatal("custom vendor should exist after load")
	}
	if vc.DisplayName != "Migration Test" {
		t.Errorf("unexpected display name: %s", vc.DisplayName)
	}

	vendorsPath := VendorsPath(tmpDir)
	if !fileExists(vendorsPath) {
		t.Fatal("vendors.yaml should be created during migration")
	}
	vData, _ := os.ReadFile(vendorsPath)
	if !contains(string(vData), "my-migration-vendor") {
		t.Errorf("vendors.yaml should contain custom vendor:\n%s", string(vData))
	}

	cfgData, _ := os.ReadFile(cfgPath)
	if contains(string(cfgData), "my-migration-vendor") {
		t.Errorf("main config should NOT contain custom vendor after migration:\n%s", string(cfgData))
	}

	cfg3, _ := Load(cfgPath)
	if _, ok := cfg3.Vendors["my-migration-vendor"]; !ok {
		t.Error("custom vendor should survive reload")
	}
}

func TestExternalFiles_FirstLaunchNoData(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	os.WriteFile(cfgPath, []byte("language: en\n"), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	for _, name := range []string{"im.yaml", "mcp_servers.yaml"} {
		path := filepath.Join(tmpDir, name)
		if fileExists(path) {
			t.Errorf("%s should NOT exist on first launch with defaults", name)
		}
	}

}

func TestExternalFiles_AllThreeTogether(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)

	cfg.Vendors["all-test"] = VendorConfig{
		DisplayName: "All Test",
		Endpoints: map[string]EndpointConfig{
			"api": {Protocol: "openai", BaseURL: "https://all.example.com"},
		},
	}
	cfg.IM.Enabled = true
	cfg.IM.Adapters = map[string]IMAdapterConfig{
		"all-bot": {Enabled: true, Platform: "slack"},
	}
	cfg.MCPServers = []MCPServerConfig{
		{Name: "all-mcp", Type: "stdio", Command: "npx"},
	}
	cfg.Language = "zh-CN"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	for _, name := range []string{"vendors.yaml", "im.yaml", "mcp_servers.yaml"} {
		path := filepath.Join(tmpDir, name)
		if !fileExists(path) {
			t.Errorf("%s should exist", name)
		}
	}

	cfg2, _ := Load(cfgPath)
	if cfg2.Language != "zh-CN" {
		t.Errorf("language should be zh-CN, got %s", cfg2.Language)
	}
	if _, ok := cfg2.Vendors["all-test"]; !ok {
		t.Error("custom vendor lost")
	}
	if _, ok := cfg2.IM.Adapters["all-bot"]; !ok {
		t.Error("IM adapter lost")
	}
	found := false
	for _, s := range cfg2.MCPServers {
		if s.Name == "all-mcp" {
			found = true
		}
	}
	if !found {
		t.Error("MCP server lost")
	}
}

func TestExternalFiles_MultipleSavesIdempotent(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)
	cfg.MCPServers = []MCPServerConfig{
		{Name: "persist-srv", Type: "stdio", Command: "npx"},
	}
	cfg.IM.Adapters = map[string]IMAdapterConfig{
		"persist-bot": {Enabled: true, Platform: "qq"},
	}

	for i := 0; i < 3; i++ {
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save #%d error: %v", i, err)
		}

		cfg, _ = Load(cfgPath)
	}

	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "persist-srv" {
		t.Errorf("MCP server corrupted after multiple saves: %+v", cfg.MCPServers)
	}
	if _, ok := cfg.IM.Adapters["persist-bot"]; !ok {
		t.Error("IM adapter lost after multiple saves")
	}
}

func TestExternalFiles_OnlyDefaultsStrippedFromVendors(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, _ := Load(cfgPath)

	cfg.Vendors["unique-custom"] = VendorConfig{
		DisplayName: "Unique Custom",
		Endpoints: map[string]EndpointConfig{
			"api": {Protocol: "openai", BaseURL: "https://unique.example.com"},
		},
	}
	cfg.Save()

	vData, _ := os.ReadFile(VendorsPath(tmpDir))
	vStr := string(vData)

	if !contains(vStr, "unique-custom") {
		t.Errorf("vendors.yaml should contain custom vendor:\n%s", vStr)
	}

	for _, name := range []string{"openai", "anthropic", "gemini", "groq", "deepseek"} {

		if contains(vStr, "\n"+name+":") || strings.HasPrefix(vStr, name+":") {
			t.Errorf("vendors.yaml should NOT contain default vendor %q:\n%s", name, vStr)
		}
	}
}
