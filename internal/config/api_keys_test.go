package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratePlaintextAPIKeys_NoPlaintext(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	content := []byte("vendor: zai\nendpoint: cn-coding-openai\nvendors:\n  zai:\n    api_key: ${ZAI_API_KEY}\n")
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigratePlaintextAPIKeys(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got %d", len(findings))
	}

	// File should be unchanged
	data, _ := os.ReadFile(cfgPath)
	if string(data) != string(content) {
		t.Fatalf("file was modified when it should not have been")
	}
}

func TestMigratePlaintextAPIKeys_VendorLevelKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	yaml := []byte("vendor: zai\nendpoint: cn-coding-openai\nvendors:\n  zai:\n    display_name: Z.ai\n    api_key: sk-test-1234567890abcdef\n    endpoints:\n      cn-coding-openai:\n        protocol: openai\n")
	if err := os.WriteFile(cfgPath, yaml, 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigratePlaintextAPIKeys(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Vendor != "zai" {
		t.Errorf("expected vendor zai, got %s", f.Vendor)
	}
	if f.EnvVar != "ZAI_API_KEY" {
		t.Errorf("expected env var ZAI_API_KEY, got %s", f.EnvVar)
	}

	// Check environment variable was set
	if v := os.Getenv("ZAI_API_KEY"); v != "sk-test-1234567890abcdef" {
		t.Errorf("expected env var set to plaintext key, got %q", v)
	}

	// Check keys.env was written
	keysData, err := os.ReadFile(KeysEnvPath())
	if err != nil {
		t.Logf("keys.env not found at %s (expected in CI), skipping content check", KeysEnvPath())
	} else {
		if !containsString(string(keysData), "ZAI_API_KEY") {
			t.Errorf("keys.env does not contain ZAI_API_KEY: %s", keysData)
		}
	}

	// Check YAML was rewritten with ${VAR}
	data, _ := os.ReadFile(cfgPath)
	if !containsString(string(data), "${ZAI_API_KEY}") {
		t.Errorf("config was not rewritten: %s", data)
	}
	if containsString(string(data), "sk-test-1234567890abcdef") {
		t.Errorf("plaintext key still in config: %s", data)
	}
}

func TestMigratePlaintextAPIKeys_EndpointLevelKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	yaml := []byte("vendor: myvendor\nendpoint: ep1\nvendors:\n  myvendor:\n    display_name: My Vendor\n    api_key: ${MYVENDOR_API_KEY}\n    endpoints:\n      ep1:\n        protocol: openai\n        api_key: sk-ep-secret-key-12345\n")
	if err := os.WriteFile(cfgPath, yaml, 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigratePlaintextAPIKeys(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Vendor != "myvendor" {
		t.Errorf("expected vendor myvendor, got %s", f.Vendor)
	}
	if f.Endpoint != "ep1" {
		t.Errorf("expected endpoint ep1, got %s", f.Endpoint)
	}
	if f.EnvVar != "MYVENDOR_EP1_API_KEY" {
		t.Errorf("expected env var MYVENDOR_EP1_API_KEY, got %s", f.EnvVar)
	}

	// Check YAML was rewritten
	data, _ := os.ReadFile(cfgPath)
	if !containsString(string(data), "${MYVENDOR_EP1_API_KEY}") {
		t.Errorf("endpoint key was not rewritten: %s", data)
	}
	if containsString(string(data), "sk-ep-secret-key-12345") {
		t.Errorf("plaintext endpoint key still in config: %s", data)
	}
}

func TestMigratePlaintextAPIKeys_ConfigFilePermission(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	yaml := []byte("vendor: zai\nendpoint: cn-coding-openai\nvendors:\n  zai:\n    api_key: sk-test-1234567890abcdef\n")
	if err := os.WriteFile(cfgPath, yaml, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := MigratePlaintextAPIKeys(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// The function writes with 0600 and forces chmod.
	stat, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	perm := stat.Mode().Perm()
	// After explicit chmod(0600), permissions should be exactly 0600.
	if perm != 0600 {
		t.Errorf("expected config file permission 0600, got %o", perm)
	}
}

func TestLoadKeysEnv(t *testing.T) {
	dir := t.TempDir()

	// Write a keys.env file
	keysPath := filepath.Join(dir, "keys.env")
	content := "# Managed by ggcode\nexport TEST_GGCODE_KEY='my-secret-value'\n"
	if err := os.WriteFile(keysPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Temporarily override KeysEnvPath
	defer func() { keysEnvPathOverride = "" }()
	keysEnvPathOverride = keysPath

	// Ensure env var is not already set
	os.Unsetenv("TEST_GGCODE_KEY")

	if err := LoadKeysEnv(); err != nil {
		t.Fatal(err)
	}

	if v := os.Getenv("TEST_GGCODE_KEY"); v != "my-secret-value" {
		t.Errorf("expected TEST_GGCODE_KEY=my-secret-value, got %q", v)
	}
}

func TestLoadKeysEnv_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()

	keysPath := filepath.Join(dir, "keys.env")
	content := "export TEST_GGCODE_EXISTING='from-keys-env'\n"
	if err := os.WriteFile(keysPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Set env var before loading keys.env
	os.Setenv("TEST_GGCODE_EXISTING", "from-shell")
	defer os.Unsetenv("TEST_GGCODE_EXISTING")

	keysEnvPathOverride = keysPath
	defer func() { keysEnvPathOverride = "" }()

	if err := LoadKeysEnv(); err != nil {
		t.Fatal(err)
	}

	// Shell env should take precedence
	if v := os.Getenv("TEST_GGCODE_EXISTING"); v != "from-shell" {
		t.Errorf("shell env should take precedence, got %q", v)
	}
}

// Helper for string containment check
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstr(s, sub)))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLooksLikeSecretField(t *testing.T) {
	cases := []struct {
		key      string
		expected bool
	}{
		{"appsecret", true},
		{"app_secret", true},
		{"bot_token", true},
		{"token", true},
		{"password", true},
		{"credential", true},
		{"Authorization", false}, // no match — "authorization" doesn't contain secret/token/password/credential
		{"appid", false},
		{"app_id", false},
		{"display_name", false},
		{"base_url", false},
		{"SecretKey", true}, // case-insensitive
	}
	for _, tc := range cases {
		got := looksLikeSecretField(tc.key)
		if got != tc.expected {
			t.Errorf("looksLikeSecretField(%q) = %v, want %v", tc.key, got, tc.expected)
		}
	}
}

func TestDetectPlaintextIMSecrets(t *testing.T) {
	yaml := `
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn:
        protocol: openai
        base_url: https://example.com
im:
  adapters:
    qq-bot:
      extra:
        appid: "123456"
        appsecret: plaintext-qq-secret
    discord-bot:
      extra:
        token: plaintext-discord-token
        display_name: MyBot
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0600)

	findings, err := DetectPlaintextAPIKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	var imFindings []APIKeyFinding
	for _, f := range findings {
		if f.Section == "im" {
			imFindings = append(imFindings, f)
		}
	}
	if len(imFindings) != 2 {
		t.Fatalf("expected 2 IM findings, got %d: %+v", len(imFindings), imFindings)
	}
	// Check that appid was NOT flagged
	for _, f := range imFindings {
		if strings.Contains(f.KeyPath, "appid") {
			t.Errorf("appid should not be flagged as secret: %s", f.KeyPath)
		}
	}
	// Check that display_name was NOT flagged
	for _, f := range imFindings {
		if strings.Contains(f.KeyPath, "display_name") {
			t.Errorf("display_name should not be flagged as secret: %s", f.KeyPath)
		}
	}
}

func TestDetectPlaintextMCPSecrets(t *testing.T) {
	yaml := `
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn:
        protocol: openai
        base_url: https://example.com
mcp_servers:
  - name: my-server
    type: stdio
    env:
      API_KEY: plaintext-mcp-key
      HARMLESS_VAR: ${ALREADY_A_REF}
    headers:
      Authorization: "Bearer plaintext-bearer-token"
  - name: clean-server
    type: http
    url: https://example.com/mcp
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0600)

	findings, err := DetectPlaintextAPIKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	var mcpFindings []APIKeyFinding
	for _, f := range findings {
		if f.Section == "mcp_env" || f.Section == "mcp_headers" {
			mcpFindings = append(mcpFindings, f)
		}
	}
	if len(mcpFindings) != 2 {
		t.Fatalf("expected 2 MCP findings, got %d: %+v", len(mcpFindings), mcpFindings)
	}
	// Verify env var names are prefixed with GGCODE_MCP_
	for _, f := range mcpFindings {
		if !strings.HasPrefix(f.EnvVar, "GGCODE_MCP_") {
			t.Errorf("expected MCP env var to start with GGCODE_MCP_, got %s", f.EnvVar)
		}
	}
}

func TestMigrateIMSecrets(t *testing.T) {
	yaml := `
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn:
        protocol: openai
        base_url: https://example.com
im:
  adapters:
    qq-bot:
      extra:
        appid: "123456"
        appsecret: plaintext-qq-secret
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0600)

	findings, err := MigratePlaintextAPIKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	var imFindings []APIKeyFinding
	for _, f := range findings {
		if f.Section == "im" {
			imFindings = append(imFindings, f)
		}
	}
	if len(imFindings) != 1 {
		t.Fatalf("expected 1 IM migration, got %d", len(imFindings))
	}

	// Read back the migrated config
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	migrated := string(data)
	if !strings.Contains(migrated, "${GGCODE_IM_QQ_BOT_APPSECRET}") {
		t.Errorf("expected env ref in migrated config, got:\n%s", migrated)
	}
	if strings.Contains(migrated, "plaintext-qq-secret") {
		t.Error("plaintext secret should be removed from config")
	}
	// appid should be untouched
	if !strings.Contains(migrated, `"123456"`) {
		t.Error("appid should be untouched")
	}

	// Check keys.env
	keysEnv := filepath.Join(tmp, "keys.env")
	keysEnvPathOverride = keysEnv
	defer func() { keysEnvPathOverride = "" }()
	// The keys.env was written to default path, not our override, so check env var
	val := os.Getenv("GGCODE_IM_QQ_BOT_APPSECRET")
	if val != "plaintext-qq-secret" {
		t.Errorf("expected os.Getenv to return migrated value, got %q", val)
	}
}

func TestMigrateMCPSecrets(t *testing.T) {
	yaml := `
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn:
        protocol: openai
        base_url: https://example.com
mcp_servers:
  - name: my-server
    type: stdio
    env:
      API_KEY: plaintext-mcp-key
    headers:
      Authorization: "Bearer plaintext-bearer"
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0600)

	findings, err := MigratePlaintextAPIKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	var mcpFindings []APIKeyFinding
	for _, f := range findings {
		if f.Section == "mcp_env" || f.Section == "mcp_headers" {
			mcpFindings = append(mcpFindings, f)
		}
	}
	if len(mcpFindings) != 2 {
		t.Fatalf("expected 2 MCP migrations, got %d", len(mcpFindings))
	}

	// Read back migrated config
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	migrated := string(data)
	if strings.Contains(migrated, "plaintext-mcp-key") {
		t.Error("plaintext mcp key should be removed")
	}
	if strings.Contains(migrated, "plaintext-bearer") {
		t.Error("plaintext bearer should be removed")
	}
	if !strings.Contains(migrated, "${GGCODE_MCP_MY_SERVER_API_KEY}") {
		t.Errorf("expected MCP env ref in migrated config, got:\n%s", migrated)
	}
}
