package config

import (
	"os"
	"path/filepath"
	"testing"
)

// r140: the main Load path expands ${VAR} refs in the raw map and then does a
// re-marshal + typed unmarshal round-trip. yaml.Marshal quotes a plain numeric
// string as `"30"`, so a numeric reference (`max_iterations: ${N}`) failed the
// typed unmarshal with "cannot unmarshal !!str into int" and the whole Load
// errored out. Values expansion actually changed must recover their implicit
// YAML scalar type; values expansion did NOT touch must keep their type, and
// expanded values containing YAML structure indicators must stay strings.

func writeMainLoadConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadNumericEnvRefRecoversType(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_N", "30")
	path := writeMainLoadConfig(t, "max_iterations: ${R140_TEST_N}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with numeric env ref failed: %v", err)
	}
	if cfg.MaxIterations != 30 {
		t.Fatalf("MaxIterations = %d, want 30", cfg.MaxIterations)
	}
}

func TestLoadBoolEnvRefRecoversType(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_FLAG", "true")
	path := writeMainLoadConfig(t, "mcp_sampling_disabled: ${R140_TEST_FLAG}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with bool env ref failed: %v", err)
	}
	if !cfg.MCPSamplingDisabled {
		t.Fatal("MCPSamplingDisabled = false, want true")
	}
}

func TestLoadNestedNumericEnvRefRecoversType(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_PORT", "8907")
	path := writeMainLoadConfig(t, "a2a:\n  port: ${R140_TEST_PORT}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with nested numeric env ref failed: %v", err)
	}
	if cfg.A2A.Port != 8907 {
		t.Fatalf("A2A.Port = %d, want 8907", cfg.A2A.Port)
	}
}

// Expansion must not change values it did not touch: a pre-existing quoted
// numeric string stays a string (observed on a string field).
func TestLoadUnexpandedQuotedStringStaysString(t *testing.T) {
	withTestHome(t)
	path := writeMainLoadConfig(t, "language: \"12345\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Language != "12345" {
		t.Fatalf("Language = %q, want \"12345\"", cfg.Language)
	}
}

// An expanded value containing YAML structure indicators ("a: b") must stay a
// string, not be re-parsed into a map by the coerce step.
func TestLoadExpandedValueWithYAMLStructureStaysString(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_STRUCT", "a: b")
	path := writeMainLoadConfig(t, "language: ${R140_TEST_STRUCT}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Language != "a: b" {
		t.Fatalf("Language = %q, want literal \"a: b\"", cfg.Language)
	}
}

// Unresolved refs keep the literal pattern (existing expander contract); they
// must not be coerced into something else or break Load.
func TestLoadUnresolvedRefStaysLiteral(t *testing.T) {
	withTestHome(t)
	path := writeMainLoadConfig(t, "language: ${R140_TEST_MISSING_XYZ}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Language != "${R140_TEST_MISSING_XYZ}" {
		t.Fatalf("Language = %q, want literal ref preserved", cfg.Language)
	}
}

// External section files (vendors.yaml / im.yaml) share the same
// expand → re-marshal → typed unmarshal chain; numeric/bool refs there used
// to fail the same way (and silently drop the whole section, since these
// loaders return nil on unmarshal errors).
func TestLoadIMFileBoolEnvRefRecoversType(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_IMFLAG", "true")
	dir := t.TempDir()
	path := filepath.Join(dir, "im.yaml")
	if err := os.WriteFile(path, []byte("enabled: ${R140_TEST_IMFLAG}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	im := loadIMFile(path)
	if im == nil {
		t.Fatal("loadIMFile returned nil (unmarshal failed)")
	}
	if !im.Enabled {
		t.Fatal("im.Enabled = false, want true")
	}
}

func TestLoadVendorsFileNumericEnvRefRecoversType(t *testing.T) {
	withTestHome(t)
	t.Setenv("R140_TEST_CW", "200000")
	dir := t.TempDir()
	path := filepath.Join(dir, "vendors.yaml")
	content := "myvendor:\n  endpoints:\n    ep1:\n      context_window: ${R140_TEST_CW}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vendors := loadVendorsFile(path)
	if vendors == nil {
		t.Fatal("loadVendorsFile returned nil (unmarshal failed)")
	}
	ep, ok := vendors["myvendor"].Endpoints["ep1"]
	if !ok {
		t.Fatal("endpoint ep1 missing after load")
	}
	if ep.ContextWindow != 200000 {
		t.Fatalf("ContextWindow = %d, want 200000", ep.ContextWindow)
	}
}
