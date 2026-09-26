package config

import (
	"os"
	"path/filepath"
	"testing"
)

// validMainYAML mirrors the minimal config that passes Load's Validate(): a
// vendor with one configured endpoint, so the tests exercise the external
// file path rather than vendor validation.
const validMainYAML = `vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: globalkey123
    endpoints:
      main:
        protocol: openai
        base_url: https://global.example.com/v1
`

// takeAllConfigLoadWarnings drains the collector so each test starts from a
// clean slate regardless of what earlier tests recorded.
func takeAllConfigLoadWarnings(t *testing.T) []LoadWarning {
	t.Helper()
	return TakeConfigLoadWarnings()
}

// TestLoadRecordsWarningForCorruptIMFile covers the r145 audit finding: a
// syntactically broken im.yaml was previously swallowed with only a debug
// log, and the next Save() rewrote the file from in-memory defaults with no
// user-visible trace. Load must record a warning naming the file.
func TestLoadRecordsWarningForCorruptIMFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte(validMainYAML), 0600); err != nil {
		t.Fatal(err)
	}
	imPath := filepath.Join(cfgDir, "im.yaml")
	if err := os.WriteFile(imPath, []byte("im:\n  adapters:\n    - broken: [unclosed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	takeAllConfigLoadWarnings(t) // drain before Load

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_ = cfg

	ws := TakeConfigLoadWarnings()
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 load warning, got %d: %+v", len(ws), ws)
	}
	if ws[0].File != imPath {
		t.Errorf("warning file = %q, want %q", ws[0].File, imPath)
	}
	if ws[0].Err == "" {
		t.Errorf("warning err text is empty")
	}
}

// TestLoadRecordsWarningsForAllCorruptExternalFiles verifies the collector
// covers vendors.yaml and mcp_servers.yaml alongside im.yaml.
func TestLoadRecordsWarningsForAllCorruptExternalFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte(validMainYAML), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := map[string]string{
		"vendors.yaml":     "zai: {endpoints: [unclosed\n",
		"mcp_servers.yaml": "- name: a\n  command: [unclosed\n",
	}
	for name, content := range corrupt {
		if err := os.WriteFile(filepath.Join(cfgDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	takeAllConfigLoadWarnings(t)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ws := TakeConfigLoadWarnings()
	got := map[string]string{}
	for _, w := range ws {
		got[filepath.Base(w.File)] = w.Err
	}
	for name := range corrupt {
		if _, ok := got[name]; !ok {
			t.Errorf("no load warning recorded for %s; got %+v", name, ws)
		}
	}
}

// TestLoadNoWarningForHealthyExternalFiles guards against false positives.
func TestLoadNoWarningForHealthyExternalFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte(validMainYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "im.yaml"), []byte("platforms:\n  telegram: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "mcp_servers.yaml"), []byte("[]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	takeAllConfigLoadWarnings(t)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ws := TakeConfigLoadWarnings(); len(ws) != 0 {
		t.Errorf("expected no load warnings for healthy files, got %+v", ws)
	}
}

// TestTakeConfigLoadWarningsConsumeOnce verifies the collector is drained by
// a single take, so startup surfaces each failure exactly once.
func TestTakeConfigLoadWarningsConsumeOnce(t *testing.T) {
	takeAllConfigLoadWarnings(t)

	recordLoadWarning("/tmp/a.yaml", "err-a")
	first := TakeConfigLoadWarnings()
	if len(first) != 1 || first[0].File != "/tmp/a.yaml" {
		t.Fatalf("first take = %+v, want one warning for /tmp/a.yaml", first)
	}
	if second := TakeConfigLoadWarnings(); len(second) != 0 {
		t.Fatalf("second take = %+v, want empty", second)
	}
}
