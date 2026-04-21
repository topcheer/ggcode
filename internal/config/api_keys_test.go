package config

import (
	"os"
	"path/filepath"
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
