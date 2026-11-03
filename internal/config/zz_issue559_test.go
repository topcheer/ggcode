package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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

		{"${UNSET_VAR:-fallback}", "fallback"},
		{"${EMPTY_VAR:-fallback}", "fallback"},
		{"${SET_VAR:-fallback}", "value-of-set"},

		{"${UNSET_VAR-fallback}", "fallback"},
		{"${EMPTY_VAR-fallback}", ""},
		{"${SET_VAR-fallback}", "value-of-set"},

		{"${SET_VAR}", "value-of-set"},
		{"${UNSET_VAR}", "${UNSET_VAR}"},

		{"key=${UNSET_VAR:-abc}suffix", "key=abcsuffix"},
	}
	for _, c := range cases {
		if got := ExpandEnvWithLookup(c.in, lookup); got != c.want {
			t.Errorf("ExpandEnvWithLookup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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

	if vc.DisplayName != "Z.ai Custom" {
		t.Errorf("DisplayName = %q, want external override %q", vc.DisplayName, "Z.ai Custom")
	}
	if ep := vc.Endpoints["cn-api"]; ep.APIKey != "sk-user" || ep.BaseURL != "https://cn.example.com/api" {
		t.Errorf("cn-api override not applied: %+v", ep)
	}

	if ep := vc.Endpoints["cn-coding"]; ep.BaseURL != "https://cn.example.com/coding" {
		t.Errorf("cn-coding built-in base_url clobbered: %+v", ep)
	}
}

func TestIssue559E_LoadExternalVendorsKeepsBuiltinEndpoints(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "ggcode.yaml")

	mainCfg := "# minimal\n"
	if err := os.WriteFile(mainPath, []byte(mainCfg), 0600); err != nil {
		t.Fatal(err)
	}

	vendorsYAML := "zai:\n  display_name: Z.ai Custom\n  endpoints:\n    cn-api:\n      base_url: https://open.bigmodel.cn/api/paas/v4\n"
	if err := os.WriteFile(filepath.Join(dir, "vendors.yaml"), []byte(vendorsYAML), 0600); err != nil {
		t.Fatal(err)
	}

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

func TestIssue559G_KnightUnsetStillOmitted(t *testing.T) {
	kc := KnightConfig{Enabled: false}
	data, err := yaml.Marshal(kc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enabled") {
		t.Errorf("unset enabled should stay omitted, got:\n%s", data)
	}
}

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
