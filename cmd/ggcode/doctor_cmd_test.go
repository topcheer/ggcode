package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// doctorTestConfig writes a minimal but fully-resolvable config and returns
// its path. HOME is isolated so keys.env auto-migration cannot touch the
// developer's real environment.
func doctorTestConfig(t *testing.T, extra string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `vendor: testv
endpoint: teste
model: test-model
vendors:
  testv:
    endpoints:
      teste:
        protocol: openai
        base_url: http://127.0.0.1:9
        api_key: sk-doctor-test
` + extra
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// mustLoadDoctorConfig loads a config from raw YAML and fails the test on
// error, for exercising individual check functions directly.
func mustLoadDoctorConfig(t *testing.T, extra string) *config.Config {
	t.Helper()
	path := doctorTestConfig(t, extra)
	cfg, err := config.LoadWithInstance(path, "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func TestRunDoctorChecksHealthyConfig(t *testing.T) {
	path := doctorTestConfig(t, "")
	checks, fails := runDoctorChecks(path)
	if fails != 0 {
		for _, c := range checks {
			t.Logf("%s -> %s: %s %v", c.Name, c.Status, c.Detail, c.Items)
		}
		t.Fatalf("expected 0 failures, got %d", fails)
	}
	byName := map[string]doctorCheck{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"config", "vendor/endpoint", "api key", "model", "mcp servers", "git"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing check %q", name)
		}
	}
	if c := byName["config"]; c.Status != "ok" {
		t.Errorf("config check status = %q (%s %v), want ok", c.Status, c.Detail, c.Items)
	}
	if c := byName["vendor/endpoint"]; c.Status != "ok" {
		t.Errorf("vendor/endpoint status = %q: %s", c.Status, c.Detail)
	}
	if c := byName["model"]; c.Status != "ok" || c.Detail != "test-model" {
		t.Errorf("model check = %q %q", c.Status, c.Detail)
	}
}

func TestRunDoctorChecksSurfacesUnknownKeys(t *testing.T) {
	path := doctorTestConfig(t, "modle: typo-value\n")
	checks, fails := runDoctorChecks(path)
	if fails != 0 {
		t.Fatalf("unknown keys are warnings, not failures (got %d)", fails)
	}
	var cfgCheck *doctorCheck
	for i := range checks {
		if checks[i].Name == "config" {
			cfgCheck = &checks[i]
		}
	}
	if cfgCheck == nil {
		t.Fatal("config check missing")
	}
	if cfgCheck.Status != "warn" {
		t.Fatalf("config status = %q, want warn", cfgCheck.Status)
	}
	joined := strings.Join(cfgCheck.Items, "\n")
	if !strings.Contains(joined, "modle") || !strings.Contains(joined, "model") {
		t.Errorf("expected modle finding with model hint, got: %s", joined)
	}
}

func TestRunDoctorChecksMissingConfigFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	checks, fails := runDoctorChecks(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if fails != 0 {
		t.Fatalf("absent config file is not a failure (got %d)", fails)
	}
	for _, c := range checks {
		if c.Name == "config" && c.Status != "ok" {
			t.Errorf("absent config should be ok/defaults, got %q: %s", c.Status, c.Detail)
		}
	}
}

func TestRunDoctorChecksUnresolvableEndpoint(t *testing.T) {
	path := doctorTestConfig(t, "")
	// Point at a vendor that does not exist.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), "vendor: testv", "vendor: nosuchvendor", 1)
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	checks, fails := runDoctorChecks(path)
	if fails == 0 {
		t.Fatalf("unresolvable vendor must fail")
	}
	// LoadWithInstance validates vendors itself, so the config check is the
	// one that fails; dependent checks degrade to warnings.
	found := false
	for _, c := range checks {
		if c.Name == "config" && c.Status == "fail" && strings.Contains(c.Detail, "nosuchvendor") {
			found = true
		}
	}
	if !found {
		t.Errorf("config check should fail on unknown vendor, got %+v", checks)
	}
}

func TestDoctorCheckMCPCatchesDuplicates(t *testing.T) {
	cfg := mustLoadDoctorConfig(t, `mcp_servers:
  - name: dup
    command: foo
  - name: dup
    command: bar
`)
	check := doctorCheckMCP(cfg)
	if check.Status != "warn" {
		t.Errorf("expected warn, got %q: %v", check.Status, check.Items)
	}
	joined := strings.Join(check.Items, "\n")
	if !strings.Contains(joined, "duplicate") {
		t.Errorf("expected duplicate-name finding, got: %s", joined)
	}
}
