package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue608VendorsEnvRefsPreservedOnSave is the regression test for #608:
// vendors.yaml may reference environment variables (${VAR}) in fields like
// base_url / display_name / models / tags. Load() expands them into memory and
// auto-saves — before the fix, SaveVendors rewrote the file with the
// materialized literal values, destroying the env references. After the fix,
// a ${VAR} leaf whose expansion equals the in-memory value is kept verbatim.
func TestIssue608VendorsEnvRefsPreservedOnSave(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("VER96_FOO_URL", "https://foo.internal.example.com/v1")

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: verfoov
endpoint: main
model: m1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// vendors.yaml with a ${VAR} reference in base_url (the probe form from
	// the issue: base_url: ${VER96_FOO_URL}).
	vendorsPath := filepath.Join(cfgDir, "vendors.yaml")
	vendorsYAML := `
verfoov:
  display_name: Foo Vendor
  endpoints:
    main:
      protocol: openai
      base_url: ${VER96_FOO_URL}
`
	if err := os.WriteFile(vendorsPath, []byte(vendorsYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// Load() expands ${VER96_FOO_URL} in memory and auto-saves.
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Sanity: the in-memory config carries the EXPANDED value.
	ep, ok := cfg.Vendors["verfoov"].Endpoints["main"]
	if !ok {
		t.Fatal("endpoint main missing after load")
	}
	if ep.BaseURL != "https://foo.internal.example.com/v1" {
		t.Fatalf("in-memory base_url should be expanded, got %q", ep.BaseURL)
	}

	// Explicit Save() too (callers other than the auto-save path).
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := os.ReadFile(vendorsPath)
	if err != nil {
		t.Fatalf("reading vendors.yaml after save: %v", err)
	}
	if !strings.Contains(string(out), "${VER96_FOO_URL}") {
		t.Errorf("vendors.yaml lost ${VER96_FOO_URL} reference — env var materialized into literal value (#608). file:\n%s", out)
	}
	if !strings.Contains(string(out), "verfoov") {
		t.Errorf("vendor verfoov should still be persisted. file:\n%s", out)
	}

	// Reload must still yield the expanded value (round-trip stability).
	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	ep2 := cfg2.Vendors["verfoov"].Endpoints["main"]
	if ep2.BaseURL != "https://foo.internal.example.com/v1" {
		t.Fatalf("re-loaded base_url should still expand, got %q", ep2.BaseURL)
	}
}

// TestIssue608ChangedValueStillOverwritesEnvRef: if the in-memory value no
// longer matches the expansion of the on-disk ${VAR} reference (the user
// actually changed it), the new literal value must be written — the #608 fix
// must not pin stale references.
func TestIssue608ChangedValueStillOverwritesEnvRef(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("VER96_FOO_URL", "https://foo.internal.example.com/v1")

	vendorsPath := filepath.Join(cfgDir, "vendors.yaml")
	vendorsYAML := `
verbarv:
  endpoints:
    main:
      protocol: openai
      base_url: ${VER96_FOO_URL}
`
	if err := os.WriteFile(vendorsPath, []byte(vendorsYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// Save a vendor whose base_url differs from the env expansion.
	changed := map[string]VendorConfig{
		"verbarv": {
			Endpoints: map[string]EndpointConfig{
				"main": {
					Protocol: "openai",
					BaseURL:  "https://changed.example.com/v1",
				},
			},
		},
	}
	if err := SaveVendors(cfgDir, changed); err != nil {
		t.Fatalf("SaveVendors: %v", err)
	}

	out, err := os.ReadFile(vendorsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "https://changed.example.com/v1") {
		t.Errorf("changed base_url must overwrite the stale ${VAR} reference. file:\n%s", out)
	}
	if strings.Contains(string(out), "${VER96_FOO_URL}") {
		t.Errorf("stale ${VER96_FOO_URL} reference must not survive a value change. file:\n%s", out)
	}
}
