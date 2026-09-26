package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The #608 fix taught SaveVendors to keep ${VAR} references alive when an
// expanded in-memory config is written back. SaveIMConfig and SaveMCPServers
// write sibling external files (im.yaml, mcp_servers.yaml) that go through the
// exact same Load-time expansion (loadIMFile / loadMCPServersFile) but lacked
// the restore step — any Save materialized env-managed credentials
// (adapters.*.env, server env/headers) into literal plaintext. These tests
// extend the #608 contract to both writers.

// TestIssue608ParityIMEnvRefsPreservedOnSave: im.yaml with a ${VAR} in
// adapters.<name>.env must survive Load (expands + auto-save) and an explicit
// Save with the reference intact.
func TestIssue608ParityIMEnvRefsPreservedOnSave(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("IM96_TOKEN", "tokval-96")

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	// Build a valid main config from the defaults first (before the external
	// files exist, so Save's empty-section cleanup cannot delete the fixtures).
	base := DefaultConfig()
	base.FilePath = cfgPath
	if err := base.Save(); err != nil {
		t.Fatalf("default Save: %v", err)
	}

	imPath := filepath.Join(cfgDir, "im.yaml")
	imYAML := `
enabled: true
adapters:
  qq:
    enabled: true
    platform: qq
    env:
      TOKEN: ${IM96_TOKEN}
`
	if err := os.WriteFile(imPath, []byte(imYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ad, ok := cfg.IM.Adapters["qq"]
	if !ok {
		t.Fatal("adapter qq missing after load")
	}
	if ad.Env["TOKEN"] != "tokval-96" {
		t.Fatalf("in-memory TOKEN should be expanded, got %q", ad.Env["TOKEN"])
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := os.ReadFile(imPath)
	if err != nil {
		t.Fatalf("reading im.yaml after save: %v", err)
	}
	if !strings.Contains(string(out), "${IM96_TOKEN}") {
		t.Errorf("im.yaml lost ${IM96_TOKEN} reference — env var materialized into literal value (#608 parity). file:\n%s", out)
	}
	if !strings.Contains(string(out), "qq") {
		t.Errorf("adapter qq should still be persisted. file:\n%s", out)
	}

	// Reload must still yield the expanded value (round-trip stability).
	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if got := cfg2.IM.Adapters["qq"].Env["TOKEN"]; got != "tokval-96" {
		t.Fatalf("re-loaded TOKEN should still expand, got %q", got)
	}
}

// TestIssue608ParityIMChangedValueOverwritesEnvRef: a genuinely edited value
// must overwrite the stale reference.
func TestIssue608ParityIMChangedValueOverwritesEnvRef(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("IM96_TOKEN", "tokval-96")

	imPath := filepath.Join(cfgDir, "im.yaml")
	imYAML := `
enabled: true
adapters:
  qq:
    enabled: true
    platform: qq
    env:
      TOKEN: ${IM96_TOKEN}
`
	if err := os.WriteFile(imPath, []byte(imYAML), 0600); err != nil {
		t.Fatal(err)
	}

	changed := &IMConfig{
		Enabled: true,
		Adapters: map[string]IMAdapterConfig{
			"qq": {
				Enabled:  true,
				Platform: "qq",
				Env:      map[string]string{"TOKEN": "rotated-literal"},
			},
		},
	}
	if err := SaveIMConfig(cfgDir, changed); err != nil {
		t.Fatalf("SaveIMConfig: %v", err)
	}

	out, err := os.ReadFile(imPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rotated-literal") {
		t.Errorf("rotated TOKEN must overwrite the stale ${VAR} reference. file:\n%s", out)
	}
	if strings.Contains(string(out), "${IM96_TOKEN}") {
		t.Errorf("stale ${IM96_TOKEN} reference must not survive a value change. file:\n%s", out)
	}
}

// TestIssue608ParityMCPEnvRefsPreservedOnSave: mcp_servers.yaml env/header
// ${VAR} references must survive Load + Save.
func TestIssue608ParityMCPEnvRefsPreservedOnSave(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MCP96_TOKEN", "mcptok-96")

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	base := DefaultConfig()
	base.FilePath = cfgPath
	if err := base.Save(); err != nil {
		t.Fatalf("default Save: %v", err)
	}

	mcpPath := filepath.Join(cfgDir, "mcp_servers.yaml")
	mcpYAML := `
- name: srv96
  type: stdio
  command: /bin/srv96
  env:
    TOKEN: ${MCP96_TOKEN}
  headers:
    Authorization: Bearer ${MCP96_TOKEN}
`
	if err := os.WriteFile(mcpPath, []byte(mcpYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var srv *MCPServerConfig
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == "srv96" {
			srv = &cfg.MCPServers[i]
		}
	}
	if srv == nil {
		t.Fatal("server srv96 missing after load")
	}
	if srv.Env["TOKEN"] != "mcptok-96" {
		t.Fatalf("in-memory TOKEN should be expanded, got %q", srv.Env["TOKEN"])
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading mcp_servers.yaml after save: %v", err)
	}
	if strings.Count(string(out), "${MCP96_TOKEN}") != 2 {
		t.Errorf("mcp_servers.yaml lost ${MCP96_TOKEN} references (env + header) — materialized into literal values (#608 parity). file:\n%s", out)
	}

	// Reload must still yield the expanded value (round-trip stability).
	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	for _, s := range cfg2.MCPServers {
		if s.Name == "srv96" && s.Env["TOKEN"] != "mcptok-96" {
			t.Fatalf("re-loaded TOKEN should still expand, got %q", s.Env["TOKEN"])
		}
	}
}

// TestIssue608ParityMCPChangedValueOverwritesEnvRef: a genuinely edited env
// value must overwrite the stale reference.
func TestIssue608ParityMCPChangedValueOverwritesEnvRef(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("MCP96_TOKEN", "mcptok-96")

	mcpPath := filepath.Join(cfgDir, "mcp_servers.yaml")
	mcpYAML := `
- name: srv96
  type: stdio
  command: /bin/srv96
  env:
    TOKEN: ${MCP96_TOKEN}
`
	if err := os.WriteFile(mcpPath, []byte(mcpYAML), 0600); err != nil {
		t.Fatal(err)
	}

	changed := []MCPServerConfig{
		{
			Name:    "srv96",
			Type:    "stdio",
			Command: "/bin/srv96",
			Env:     map[string]string{"TOKEN": "rotated-mcp-literal"},
		},
	}
	if err := SaveMCPServers(cfgDir, changed); err != nil {
		t.Fatalf("SaveMCPServers: %v", err)
	}

	out, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rotated-mcp-literal") {
		t.Errorf("rotated TOKEN must overwrite the stale ${VAR} reference. file:\n%s", out)
	}
	if strings.Contains(string(out), "${MCP96_TOKEN}") {
		t.Errorf("stale ${MCP96_TOKEN} reference must not survive a value change. file:\n%s", out)
	}
}
