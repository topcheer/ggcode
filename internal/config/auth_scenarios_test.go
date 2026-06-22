package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Scenario 1: GitHub zero-config (built-in client_id)
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_GitHubZeroConfig(t *testing.T) {
	yaml := `
a2a:
  disabled: false
  auth:
    oauth2:
      provider: "github"
`
	cfg := parseTestConfig(t, yaml)

	if cfg.A2A.Auth.OAuth2 == nil {
		t.Fatal("expected oauth2 config")
	}
	if cfg.A2A.Auth.OAuth2.Provider != "github" {
		t.Errorf("expected github, got %s", cfg.A2A.Auth.OAuth2.Provider)
	}
	if cfg.A2A.Auth.OAuth2.Flow != "" {
		t.Errorf("expected empty flow (auto), got %s", cfg.A2A.Auth.OAuth2.Flow)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: GitHub Device Flow with custom client_id
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_GitHubDeviceFlow(t *testing.T) {
	yaml := `
a2a:
  auth:
    oauth2:
      provider: "github"
      client_id: "my-own-github-app"
      client_secret: "shhh-secret"
      flow: "device"
`
	cfg := parseTestConfig(t, yaml)

	oc := cfg.A2A.Auth.OAuth2
	if oc.Provider != "github" {
		t.Errorf("expected github, got %s", oc.Provider)
	}
	if oc.ClientID != "my-own-github-app" {
		t.Errorf("expected my-own-github-app, got %s", oc.ClientID)
	}
	if oc.ClientSecret != "shhh-secret" {
		t.Error("expected client_secret")
	}
	if oc.Flow != "device" {
		t.Errorf("expected device, got %s", oc.Flow)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Custom IdP with manual URLs
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_CustomIdP(t *testing.T) {
	yaml := `
a2a:
  auth:
    oauth2:
      issuer_url: "https://my-idp.example.com"
      client_id: "my-app-id"
      client_secret: "my-secret"
      flow: "pkce"
      scopes: "openid profile email"
`
	cfg := parseTestConfig(t, yaml)

	oc := cfg.A2A.Auth.OAuth2
	if oc.IssuerURL != "https://my-idp.example.com" {
		t.Errorf("expected custom issuer, got %s", oc.IssuerURL)
	}
	if oc.ClientID != "my-app-id" {
		t.Errorf("expected my-app-id, got %s", oc.ClientID)
	}
	if oc.Flow != "pkce" {
		t.Errorf("expected pkce, got %s", oc.Flow)
	}
	if oc.Scopes != "openid profile email" {
		t.Errorf("expected scopes, got %s", oc.Scopes)
	}
	if oc.Provider != "" {
		t.Errorf("expected no provider preset, got %s", oc.Provider)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: OIDC provider
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_OIDC(t *testing.T) {
	yaml := `
a2a:
  auth:
    oidc:
      provider: "auth0"
      client_id: "auth0-client"
      client_secret: "auth0-secret"
      scopes: "openid profile email"
`
	cfg := parseTestConfig(t, yaml)

	oidc := cfg.A2A.Auth.OIDC
	if oidc == nil {
		t.Fatal("expected oidc config")
	}
	if oidc.Provider != "auth0" {
		t.Errorf("expected auth0, got %s", oidc.Provider)
	}
	if oidc.Scopes != "openid profile email" {
		t.Errorf("expected scopes, got %s", oidc.Scopes)
	}
	if oidc.ClientSecret != "auth0-secret" {
		t.Error("expected client_secret in OIDC")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: All auth methods enabled
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_AllAuthMethods(t *testing.T) {
	yaml := `
a2a:
  auth:
    api_key: "shared-secret-key"
    oauth2:
      provider: "github"
      flow: "device"
    oidc:
      provider: "google"
      client_id: "google-client"
    mtls:
      cert_file: ".ggcode/certs/server.pem"
      key_file: ".ggcode/certs/server.key"
      ca_file: ".ggcode/certs/ca.pem"
`
	cfg := parseTestConfig(t, yaml)

	if cfg.A2A.Auth.APIKey != "shared-secret-key" {
		t.Errorf("expected api_key, got %s", cfg.A2A.Auth.APIKey)
	}
	if cfg.A2A.Auth.OAuth2 == nil {
		t.Error("expected oauth2")
	}
	if cfg.A2A.Auth.OIDC == nil {
		t.Error("expected oidc")
	}
	if cfg.A2A.Auth.MTLS == nil {
		t.Error("expected mtls")
	}
	if cfg.A2A.Auth.MTLS.CertFile != ".ggcode/certs/server.pem" {
		t.Errorf("expected cert path, got %s", cfg.A2A.Auth.MTLS.CertFile)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: API key only (simplest)
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_APIKeyOnly(t *testing.T) {
	yaml := `
a2a:
  auth:
    api_key: "simple-key"
`
	cfg := parseTestConfig(t, yaml)

	if cfg.A2A.Auth.APIKey != "simple-key" {
		t.Errorf("expected simple-key, got %s", cfg.A2A.Auth.APIKey)
	}
	if cfg.A2A.Auth.OAuth2 != nil {
		t.Error("expected no oauth2")
	}
	if cfg.A2A.Auth.OIDC != nil {
		t.Error("expected no oidc")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: No auth (open)
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_NoAuth(t *testing.T) {
	yaml := `
a2a:
  disabled: false
`
	cfg := parseTestConfig(t, yaml)

	if cfg.A2A.Auth.APIKey != "" {
		t.Error("expected no api_key")
	}
	if cfg.A2A.Auth.OAuth2 != nil {
		t.Error("expected no oauth2")
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: mTLS only
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_MTLSOnly(t *testing.T) {
	yaml := `
a2a:
  auth:
    mtls:
      cert_file: "/etc/certs/server.pem"
      key_file: "/etc/certs/server.key"
      ca_file: "/etc/certs/ca.pem"
`
	cfg := parseTestConfig(t, yaml)

	if cfg.A2A.Auth.MTLS == nil {
		t.Fatal("expected mtls")
	}
	if cfg.A2A.Auth.APIKey != "" {
		t.Error("expected no api_key")
	}
	if cfg.A2A.Auth.OAuth2 != nil {
		t.Error("expected no oauth2")
	}
}

// ---------------------------------------------------------------------------
// Scenario 9: Legacy a2a.api_key auto-migrates to auth.api_key
// ---------------------------------------------------------------------------

func TestOAuth2Scenario_LegacyA2AAPIKeyMigration(t *testing.T) {
	yaml := `
a2a:
  api_key: "my-old-key"
`
	cfg := parseTestConfig(t, yaml)

	// Legacy key should be migrated to auth.api_key
	if cfg.A2A.Auth.APIKey != "my-old-key" {
		t.Errorf("expected auth.api_key=my-old-key after migration, got %q", cfg.A2A.Auth.APIKey)
	}
	if !cfg.A2A.HasAuth() {
		t.Error("expected HasAuth()=true after migration")
	}
}

func TestOAuth2Scenario_LegacyA2AAPIKeyDoesNotOverrideNew(t *testing.T) {
	yaml := `
a2a:
  api_key: "old-key"
  auth:
    api_key: "new-key"
`
	cfg := parseTestConfig(t, yaml)

	// New format takes priority; legacy key is discarded
	if cfg.A2A.Auth.APIKey != "new-key" {
		t.Errorf("expected auth.api_key=new-key, got %q", cfg.A2A.Auth.APIKey)
	}
}

// ---------------------------------------------------------------------------
// Scenario 10: Instance-level override
// ---------------------------------------------------------------------------
func TestOAuth2Scenario_InstanceOverride(t *testing.T) {
	tmpDir := t.TempDir()
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)

	overrideYaml := `
auth:
  api_key: "instance-specific-key"
  oauth2:
    provider: "github"
    flow: "device"
`
	os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte(overrideYaml), 0644)

	// Load override
	override := LoadA2AOverride(tmpDir)
	if override == nil {
		t.Fatal("expected override")
	}

	// Merge into base config
	base := A2AConfig{
		Auth: A2AAuthConfig{APIKey: "global-key"},
	}
	MergeA2AConfig(&base, override)

	if base.Auth.APIKey != "instance-specific-key" {
		t.Errorf("expected instance key to override, got %s", base.Auth.APIKey)
	}
	if base.Auth.OAuth2 == nil {
		t.Error("expected oauth2 from override")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------
func parseTestConfig(t *testing.T, yamlContent string) *Config {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func TestHasAuth(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected bool
	}{
		{"no auth", "", false},
		{"auth.api_key", "a2a:\n  auth:\n    api_key: key123\n", true},
		{"auth.api_keys", "a2a:\n  auth:\n    api_keys: [\"k1\", \"k2\"]\n", true},
		{"oauth2", "a2a:\n  auth:\n    oauth2:\n      provider: github\n", true},
		{"oidc", "a2a:\n  auth:\n    oidc:\n      provider: google\n", true},
		{"mtls", "a2a:\n  auth:\n    mtls:\n      cert_file: test.pem\n", true},
		{"empty api_key", "a2a:\n  api_key: \"\"\n  auth:\n    api_key: \"\"\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseTestConfig(t, tt.yaml)
			if got := cfg.A2A.HasAuth(); got != tt.expected {
				t.Errorf("HasAuth() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestA2AHostDefault(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected string
	}{
		{"no auth, lan_discovery default → 0.0.0.0", "", "0.0.0.0"},
		{"lan_discovery: false → 127.0.0.1", "a2a:\n  lan_discovery: false\n", "127.0.0.1"},
		{"with auth.api_key → 0.0.0.0", "a2a:\n  auth:\n    api_key: key\n", "0.0.0.0"},
		{"with oauth2 → 0.0.0.0", "a2a:\n  auth:\n    oauth2:\n      provider: github\n", "0.0.0.0"},
		{"explicit host override", "a2a:\n  host: 192.168.1.1\n  auth:\n    api_key: key\n", "192.168.1.1"},
		{"empty explicit host with auth → 0.0.0.0", "a2a:\n  host: \"\"\n  auth:\n    api_key: key\n", "0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseTestConfig(t, tt.yaml)
			if cfg.A2A.Host != tt.expected {
				t.Errorf("Host = %q, want %q", cfg.A2A.Host, tt.expected)
			}
		})
	}
}
