package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func issue610WriteConfig(t *testing.T, dir, extra string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	yamlText := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: key610
    endpoints:
      main:
        protocol: openai
        base_url: https://test610.example.com/v1
` + extra
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestIssue610ClearedStringFieldDoesNotResurrect(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue610WriteConfig(t, cfgDir, "extra_prompt: old-prompt-610\n")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExtraPrompt != "old-prompt-610" {
		t.Fatalf("after first Load, ExtraPrompt = %q, want %q", cfg.ExtraPrompt, "old-prompt-610")
	}

	cfg.ExtraPrompt = ""
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "extra_prompt") {
		t.Fatalf("extra_prompt still present in config after clear+Save:\n%s", raw)
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if cfg2.ExtraPrompt != "" {
		t.Fatalf("extra_prompt resurrected after clear+Save+Load: %q", cfg2.ExtraPrompt)
	}
}

func TestIssue610ClearIsIdempotentAcrossSaves(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue610WriteConfig(t, cfgDir, "extra_prompt: old-prompt-610\n")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ExtraPrompt = ""
	for i := 0; i < 3; i++ {
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save #%d: %v", i+1, err)
		}
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "extra_prompt") {
		t.Fatalf("extra_prompt resurrected by repeated Save:\n%s", raw)
	}
}

func TestIssue610NeverOnDiskKeyNotWritten(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue610WriteConfig(t, cfgDir, "")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExtraPrompt != "" {
		t.Fatalf("expected empty ExtraPrompt, got %q", cfg.ExtraPrompt)
	}

	other := []byte("\nextra_prompt: written-by-other-process\n")
	if err := appendYAMLToFile(cfgPath, other); err != nil {
		t.Fatal(err)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "written-by-other-process") {
		t.Fatalf("Save erased a value written by another process after Load (#284 regression):\n%s", raw)
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if cfg2.ExtraPrompt != "written-by-other-process" {
		t.Fatalf("re-Load lost other process value: %q", cfg2.ExtraPrompt)
	}
}

func TestIssue610EnvRefNotTombstoned(t *testing.T) {
	existing := map[string]interface{}{
		"extra_prompt": "${GGCODE_ISSUE610_PROMPT}",
		"plain":        "survives",
	}
	snap := map[string]string{
		"extra_prompt": "${GGCODE_ISSUE610_PROMPT}",
	}
	cleared := map[string]bool{"extra_prompt": true}

	applyClearedTombstones(existing, snap, cleared)

	if _, ok := existing["extra_prompt"]; !ok {
		t.Fatal("env-var reference was tombstoned away (#608 regression)")
	}
	if existing["plain"] != "survives" {
		t.Fatalf("unrelated key modified: %v", existing["plain"])
	}
}

func TestIssue610DiskValueChangedAfterLoadPreserved(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue610WriteConfig(t, cfgDir, "extra_prompt: old-prompt-610\n")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ExtraPrompt = ""

	if err := replaceYAMLValue(cfgPath, "extra_prompt: old-prompt-610", "extra_prompt: newer-prompt-610"); err != nil {
		t.Fatal(err)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "newer-prompt-610") {
		t.Fatalf("Save deleted a concurrently-changed value (#284 regression):\n%s", raw)
	}
}

func TestIssue610NestedStringClear(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue610WriteConfig(t, cfgDir, "a2a:\n  auth:\n    mtls:\n      cert_file: /tmp/cert-610.pem\n")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.A2A.Auth.MTLS == nil || cfg.A2A.Auth.MTLS.CertFile != "/tmp/cert-610.pem" {
		t.Fatalf("a2a.auth.mtls.cert_file not loaded: %+v", cfg.A2A.Auth.MTLS)
	}

	cfg.A2A.Auth.MTLS.CertFile = ""
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cert_file") {
		t.Fatalf("a2a.auth.mtls.cert_file resurrected after clear+Save:\n%s", raw)
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if cfg2.A2A.Auth.MTLS != nil && cfg2.A2A.Auth.MTLS.CertFile != "" {
		t.Fatalf("cert_file resurrected on re-Load: %q", cfg2.A2A.Auth.MTLS.CertFile)
	}
}

func appendYAMLToFile(path string, extra []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(extra)
	return err
}

func replaceYAMLValue(path, old, new string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), old) {
		return os.ErrNotExist
	}
	replaced := strings.Replace(string(data), old, new, 1)
	return os.WriteFile(path, []byte(replaced), 0600)
}
