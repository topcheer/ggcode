package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// issue740Setup loads an isolated config (HOME redirected to a temp dir) and
// installs it as the global config, returning the cfg and its file path.
// The base shape mirrors zz_issue734_test.go: a single vendor/endpoint with
// known scalar fields so a partial-mutation leak is observable field by field.
func issue740Setup(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: a
endpoint: main
model: m1
language: en
default_mode: supervised
max_iterations: 10
vendors:
  a:
    endpoints:
      main:
        protocol: openai
        base_url: https://a.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	SetConfig(cfg)
	return cfg, cfgPath
}

// issue740AssertUntouched verifies the in-memory cfg and the GetFullConfig
// snapshot still report the pristine base values after a failed UpdateConfig.
func issue740AssertUntouched(t *testing.T, cfg *config.Config, where string) {
	t.Helper()
	if cfg.Vendor != "a" {
		t.Errorf("%s: cfg.Vendor = %q, want unchanged %q (#740 partial mutation)", where, cfg.Vendor, "a")
	}
	if cfg.Endpoint != "main" {
		t.Errorf("%s: cfg.Endpoint = %q, want %q", where, cfg.Endpoint, "main")
	}
	if cfg.Model != "m1" {
		t.Errorf("%s: cfg.Model = %q, want %q", where, cfg.Model, "m1")
	}
	if cfg.Language != "en" {
		t.Errorf("%s: cfg.Language = %q, want %q", where, cfg.Language, "en")
	}
	if cfg.DefaultMode != "supervised" {
		t.Errorf("%s: cfg.DefaultMode = %q, want %q", where, cfg.DefaultMode, "supervised")
	}
	if cfg.MaxIterations != 10 {
		t.Errorf("%s: cfg.MaxIterations = %d, want 10", where, cfg.MaxIterations)
	}
	fc, err := GetFullConfig()
	if err != nil {
		t.Fatalf("%s: GetFullConfig: %v", where, err)
	}
	if fc.Vendor != "a" || fc.Model != "m1" || fc.Language != "en" || fc.DefaultMode != "supervised" || fc.MaxIter != 10 {
		t.Errorf("%s: GetFullConfig reports polluted values (#740): %+v", where, fc)
	}
}

// TestIssue740BatchVendorErrorLeavesConfigUntouched reproduces the SettingsPage
// six-field batch shape: several valid fields plus a vendor that does not
// exist, with a baseURL update targeting it. Before #740 the vendor/endpoint
// existence check ran mid-chain, so every earlier field (vendor, endpoint,
// model, language, defaultMode, maxIterations...) was already written into the
// shared in-memory cfg while Save() never ran: the UI reported the failed
// values as active and the next successful save silently persisted them.
func TestIssue740BatchVendorErrorLeavesConfigUntouched(t *testing.T) {
	cfg, cfgPath := issue740Setup(t)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	bad := map[string]interface{}{
		"vendor":        "ghost", // does not exist
		"endpoint":      "main",
		"model":         "evil-model",
		"language":      "zh",
		"defaultMode":   "bypass",
		"maxIterations": float64(999),
		"baseURL":       "https://evil.example.com/v1",
	}
	if err := UpdateConfig(bad); err == nil {
		t.Fatal("UpdateConfig with nonexistent vendor must fail")
	} else if !strings.Contains(err.Error(), "vendor") {
		t.Errorf("unexpected error: %v", err)
	}

	issue740AssertUntouched(t, cfg, "after vendor error")

	// Nothing may have been persisted either: the update failed.
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("config file changed despite failed update (#740):\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestIssue740BatchEndpointErrorLeavesConfigUntouched covers the second
// mid-chain existence failure: valid vendor, nonexistent endpoint.
func TestIssue740BatchEndpointErrorLeavesConfigUntouched(t *testing.T) {
	cfg, cfgPath := issue740Setup(t)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	bad := map[string]interface{}{
		"endpoint":      "ghost-ep",
		"model":         "evil-model",
		"language":      "zh",
		"maxIterations": float64(999),
		"baseURL":       "https://evil.example.com/v1",
	}
	if err := UpdateConfig(bad); err == nil {
		t.Fatal("UpdateConfig with nonexistent endpoint must fail")
	} else if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("unexpected error: %v", err)
	}

	issue740AssertUntouched(t, cfg, "after endpoint error")

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("config file changed despite failed update (#740):\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestIssue740BadDurationsLeaveConfigUntouched covers the two duration parse
// failures (subAgentTimeout / swarmTimeout). They sat at the very END of the
// old mutation chain, so a bad value leaked every preceding field.
func TestIssue740BadDurationsLeaveConfigUntouched(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]interface{}
		wantIn string
	}{
		{
			name: "subAgentTimeout",
			values: map[string]interface{}{
				"model":                 "evil-model",
				"language":              "zh",
				"maxIterations":         float64(999),
				"subAgentMaxConcurrent": float64(13),
				"subAgentTimeout":       "45 fortnights",
			},
			wantIn: "subAgentTimeout",
		},
		{
			name: "swarmTimeout",
			values: map[string]interface{}{
				"model":             "evil-model",
				"language":          "zh",
				"maxIterations":     float64(999),
				"swarmMaxTeammates": float64(7),
				"swarmTimeout":      "not-a-duration",
			},
			wantIn: "swarmTimeout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, cfgPath := issue740Setup(t)
			wantSub := cfg.SubAgents.Timeout
			wantSwarm := cfg.Swarm.TeammateTimeout
			before, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}

			if err := UpdateConfig(tc.values); err == nil {
				t.Fatal("UpdateConfig with invalid duration must fail")
			} else if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error should mention %s, got: %v", tc.wantIn, err)
			}

			issue740AssertUntouched(t, cfg, "after duration error")
			if cfg.SubAgents.Timeout != wantSub {
				t.Errorf("SubAgents.Timeout = %v, want unchanged %v", cfg.SubAgents.Timeout, wantSub)
			}
			if cfg.Swarm.TeammateTimeout != wantSwarm {
				t.Errorf("Swarm.TeammateTimeout = %v, want unchanged %v", cfg.Swarm.TeammateTimeout, wantSwarm)
			}

			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Errorf("config file changed despite failed update (#740):\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// TestIssue740ValidBatchStillAppliesAndSaves guards the happy path: a fully
// valid batch must keep applying every field and persisting via Save(), and a
// retry after a failed update must still succeed once the offending value is
// corrected (no residual poisoned state from the failed attempt).
func TestIssue740ValidBatchStillAppliesAndSaves(t *testing.T) {
	cfg, cfgPath := issue740Setup(t)

	good := map[string]interface{}{
		"vendor":          "a",
		"endpoint":        "main",
		"model":           "m2",
		"language":        "zh",
		"maxIterations":   float64(20),
		"baseURL":         "https://b740.example.com/v1",
		"subAgentTimeout": "90s",
		"swarmTimeout":    "3m",
	}
	if err := UpdateConfig(good); err != nil {
		t.Fatalf("valid UpdateConfig failed: %v", err)
	}
	if cfg.Model != "m2" || cfg.Language != "zh" || cfg.MaxIterations != 20 {
		t.Errorf("valid batch not applied: model=%q language=%q maxIter=%d", cfg.Model, cfg.Language, cfg.MaxIterations)
	}
	if cfg.SubAgents.Timeout != 90*time.Second {
		t.Errorf("SubAgents.Timeout = %v, want 90s", cfg.SubAgents.Timeout)
	}
	if cfg.Swarm.TeammateTimeout != 3*time.Minute {
		t.Errorf("Swarm.TeammateTimeout = %v, want 3m", cfg.Swarm.TeammateTimeout)
	}
	if ep, ok := cfg.Vendors["a"].Endpoints["main"]; !ok || ep.BaseURL != "https://b740.example.com/v1" {
		t.Errorf("baseURL not applied: %+v", cfg.Vendors["a"].Endpoints["main"])
	}
	onDisk, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// baseURL lives in the vendors file, not ggcode.yaml; its in-memory
	// application is asserted above.
	for _, want := range []string{"m2", "zh"} {
		if !strings.Contains(string(onDisk), want) {
			t.Errorf("saved config missing %q; file:\n%s", want, onDisk)
		}
	}

	// Failed update then corrected retry: the failed attempt must leave no
	// trace that pollutes the retry's result.
	fail := map[string]interface{}{"model": "leaky", "vendor": "ghost", "baseURL": "https://x.example.com"}
	if err := UpdateConfig(fail); err == nil {
		t.Fatal("ghost vendor must fail")
	}
	if cfg.Model != "m2" {
		t.Errorf("failed update leaked model = %q, want m2 (#740)", cfg.Model)
	}
	retry := map[string]interface{}{"model": "m3"}
	if err := UpdateConfig(retry); err != nil {
		t.Fatalf("retry after failed update: %v", err)
	}
	if cfg.Model != "m3" {
		t.Errorf("retry not applied: model=%q, want m3", cfg.Model)
	}
	onDisk, err = os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "leaky") {
		t.Errorf("leaked field from failed update persisted (#740); file:\n%s", onDisk)
	}
}
