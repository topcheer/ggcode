package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---- Bug F: ${VAR:-default} interpolation ----

// #559 F: expandEnvWithLookup_DefaultValue tests ${VAR:-default} semantics.
func TestIssue559F_ExpandEnvDefaultValue(t *testing.T) {
	lookup := func(name string) (string, bool) {
		switch name {
		case "SET_VAR":
			return "value-of-set", true
		case "EMPTY_VAR":
			return "", true
		default:
			return "", false
		}
	}
	cases := []struct {
		in   string
		want string
	}{
		// :- form: unset or empty → default
		{"${UNSET_VAR:-fallback}", "fallback"},
		{"${EMPTY_VAR:-fallback}", "fallback"},
		{"${SET_VAR:-fallback}", "value-of-set"},
		// - form: unset → default; set (even empty) → value
		{"${UNSET_VAR-fallback}", "fallback"},
		{"${EMPTY_VAR-fallback}", ""},
		{"${SET_VAR-fallback}", "value-of-set"},
		// plain form unchanged behavior
		{"${SET_VAR}", "value-of-set"},
		{"${UNSET_VAR}", "${UNSET_VAR}"},
		// embedded in larger string
		{"key=${UNSET_VAR:-abc}suffix", "key=abcsuffix"},
	}
	for _, c := range cases {
		if got := ExpandEnvWithLookup(c.in, lookup); got != c.want {
			t.Errorf("ExpandEnvWithLookup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---- Bug E: external vendors.yaml field-level merge ----

// #559 E: mergeVendors must NOT drop built-in endpoints that the external
// definition does not mention (probe: zai 6 → 1).
func TestIssue559E_MergeVendorsPreservesUnmentionedBuiltInEndpoints(t *testing.T) {
	base := map[string]VendorConfig{
		"zai": {
			DisplayName: "Z.ai",
			Endpoints: map[string]EndpointConfig{
				"cn-api":     {BaseURL: "https://cn.example.com/api"},
				"cn-coding":  {BaseURL: "https://cn.example.com/coding"},
				"global-api": {BaseURL: "https://global.example.com/api"},
			},
		},
	}
	// User overrode only the cn-api endpoint (e.g. added a key).
	external := map[string]VendorConfig{
		"zai": {
			DisplayName: "Z.ai Custom",
			Endpoints: map[string]EndpointConfig{
				"cn-api": {BaseURL: "https://cn.example.com/api", APIKey: "sk-user"},
			},
		},
	}
	merged := mergeVendors(base, external)
	vc, ok := merged["zai"]
	if !ok {
		t.Fatal("zai vendor missing after merge")
	}
	for _, ep := range []string{"cn-api", "cn-coding", "global-api"} {
		if _, ok := vc.Endpoints[ep]; !ok {
			t.Errorf("endpoint %q dropped by field-level merge (Bug E regression): have %v", ep, vc.Endpoints)
		}
	}
	// External override must still win for the fields it defines.
	if vc.DisplayName != "Z.ai Custom" {
		t.Errorf("DisplayName = %q, want external override %q", vc.DisplayName, "Z.ai Custom")
	}
	if ep := vc.Endpoints["cn-api"]; ep.APIKey != "sk-user" || ep.BaseURL != "https://cn.example.com/api" {
		t.Errorf("cn-api override not applied: %+v", ep)
	}
	// Built-in values kept where external gives zero values.
	if ep := vc.Endpoints["cn-coding"]; ep.BaseURL != "https://cn.example.com/coding" {
		t.Errorf("cn-coding built-in base_url clobbered: %+v", ep)
	}
}

// #559 E: end-to-end — Load() with an external vendors.yaml that overrides one
// field of a built-in vendor must keep all built-in endpoints available.
func TestIssue559E_LoadExternalVendorsKeepsBuiltinEndpoints(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")
	// Minimal main config; the built-in zai vendor comes from DefaultConfig's
	// merge path, so no vendor/endpoint pinning here.
	mainCfg := "# minimal\n"
	if err := os.WriteFile(mainPath, []byte(mainCfg), 0600); err != nil {
		t.Fatal(err)
	}
	// External vendors.yaml overriding one field of the built-in zai vendor.
	vendorsYAML := "zai:\n  display_name: Z.ai Custom\n  endpoints:\n    cn-api:\n      base_url: https://open.bigmodel.cn/api/paas/v4\n"
	if err := os.WriteFile(filepath.Join(dir, "vendors.yaml"), []byte(vendorsYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// No GGCODE_SKIP_AUTOCONFIG here: it would skip loadExternalSections
	// entirely (Load gates it behind !skipAuto). All writes go to the TempDir
	// (Save derives extDir from Dir(FilePath)), never the real ~/.ggcode.
	cfg, err := Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vc, ok := cfg.Vendors["zai"]
	if !ok {
		t.Fatal("zai vendor missing")
	}
	if len(vc.Endpoints) < 2 {
		t.Errorf("Bug E regression: built-in zai endpoints collapsed to %d (%v)", len(vc.Endpoints), vc.Endpoints)
	}
	if vc.DisplayName != "Z.ai Custom" {
		t.Errorf("external display_name override not applied: %q", vc.DisplayName)
	}
}

// ---- Bug G-adjacent: Knight explicit false persists ----

// #559 G: explicit enabled=false (enabledSet) must survive MarshalYAML.
func TestIssue559G_KnightExplicitFalsePersists(t *testing.T) {
	kc := KnightConfig{Enabled: false}
	kc.SetEnabledExplicitly()
	data, err := yaml.Marshal(kc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("explicit disable not persisted, got:\n%s", data)
	}
	// Round-trip: unmarshal must set Enabled=false (and enabledSet).
	var back KnightConfig
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Enabled {
		t.Error("explicit false came back as true after round-trip")
	}
	if !back.HasExplicitEnabled() {
		t.Error("enabledSet lost after round-trip")
	}
}

// #559 G: unset enabled stays omitted (no file bloat, no behavior change).
func TestIssue559G_KnightUnsetStillOmitted(t *testing.T) {
	kc := KnightConfig{Enabled: false} // never explicitly set
	data, err := yaml.Marshal(kc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enabled") {
		t.Errorf("unset enabled should stay omitted, got:\n%s", data)
	}
}

// #559 G: explicit true still marshals.
func TestIssue559G_KnightExplicitTrueMarshals(t *testing.T) {
	kc := KnightConfig{Enabled: true}
	kc.SetEnabledExplicitly()
	data, err := yaml.Marshal(kc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "enabled: true") {
		t.Errorf("explicit true missing, got:\n%s", data)
	}
}
