package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpsertMCPServerPatchPreservesUnsetFields (#249): overlaying a partial
// server onto an existing one must keep fields the patch leaves zero.
func TestUpsertMCPServerPatchPreservesUnsetFields(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCPServers = []MCPServerConfig{{
		Name:    "srv",
		Type:    "http",
		URL:     "https://old.example.com",
		Env:     map[string]string{"TOKEN": "t1"},
		Headers: map[string]string{"X-A": "1"},
	}}

	replaced := cfg.UpsertMCPServer(MCPServerConfig{
		Name: "srv",
		URL:  "https://new.example.com",
	})
	if !replaced {
		t.Fatal("expected existing server to be replaced")
	}
	got := cfg.MCPServers[0]
	if got.URL != "https://new.example.com" {
		t.Errorf("URL not updated: %q", got.URL)
	}
	if got.Type != "http" {
		t.Errorf("type cleared, want http: %q", got.Type)
	}
	if got.Env["TOKEN"] != "t1" {
		t.Errorf("env cleared: %+v", got.Env)
	}
	if got.Headers["X-A"] != "1" {
		t.Errorf("headers cleared: %+v", got.Headers)
	}
}

// TestMigrateVendorsFilePlaintextAPIKeys (#250): plaintext keys in the
// standalone vendors.yaml are moved to keys.env and replaced with ${VAR}
// references in the file.
func TestMigrateVendorsFilePlaintextAPIKeys(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	vendorsPath := filepath.Join(dir, "vendors.yaml")
	keysPath := filepath.Join(dir, "keys.env")

	yaml := []byte("myvendor:\n  display_name: My Vendor\n  api_key: sk-vendor-secret-123\n  endpoints:\n    ep1:\n      protocol: openai\n      base_url: https://api.example.com\n      api_key: sk-ep-secret-456\n")
	if err := os.WriteFile(vendorsPath, yaml, 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigrateVendorsFilePlaintextAPIKeys(vendorsPath, keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}

	data, _ := os.ReadFile(vendorsPath)
	content := string(data)
	if strings.Contains(content, "sk-vendor-secret-123") || strings.Contains(content, "sk-ep-secret-456") {
		t.Fatalf("plaintext keys still in vendors.yaml:\n%s", content)
	}
	if !strings.Contains(content, "${MYVENDOR_API_KEY}") {
		t.Fatalf("vendor key not rewritten as env reference:\n%s", content)
	}
	if !strings.Contains(content, "${MYVENDOR_EP1_API_KEY}") {
		t.Fatalf("endpoint key not rewritten as env reference:\n%s", content)
	}

	keysData, _ := os.ReadFile(keysPath)
	keysContent := string(keysData)
	if !strings.Contains(keysContent, "MYVENDOR_API_KEY='sk-vendor-secret-123'") {
		t.Fatalf("vendor key missing from keys.env:\n%s", keysContent)
	}
	if !strings.Contains(keysContent, "MYVENDOR_EP1_API_KEY='sk-ep-secret-456'") {
		t.Fatalf("endpoint key missing from keys.env:\n%s", keysContent)
	}
}

// TestMigrateVendorsFilePlaintextAPIKeys_NoFindings: files without plaintext
// keys are left untouched.
func TestMigrateVendorsFilePlaintextAPIKeys_NoFindings(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	vendorsPath := filepath.Join(dir, "vendors.yaml")
	original := "myvendor:\n  api_key: ${MYVENDOR_API_KEY}\n"
	if err := os.WriteFile(vendorsPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigrateVendorsFilePlaintextAPIKeys(vendorsPath, filepath.Join(dir, "keys.env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %+v", findings)
	}
	if _, err := os.Stat(filepath.Join(dir, "keys.env")); !os.IsNotExist(err) {
		t.Fatalf("keys.env unexpectedly written: %v", err)
	}
	data, _ := os.ReadFile(vendorsPath)
	if string(data) != original {
		t.Fatalf("vendors.yaml modified without findings:\n%s", data)
	}
}
