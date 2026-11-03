package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4697_ReadErrorAbortsSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(home, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte("model: m1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithInstance(cfgPath, t.TempDir())
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfgPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err == nil {
		t.Fatal("Save must abort on a real read error, not fall into full-write (#4697)")
	}
}

func TestIssue4697_ExternalSectionFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendors.yaml"), 0700); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{IM: IMConfig{Enabled: true}}
	err := saveExternalSections(cfg, dir, map[string]VendorConfig{
		"t4697": {APIKey: "k"},
	})
	if err == nil {
		t.Fatal("saveExternalSections must return the write failure, not discard it (#4697)")
	}
}

func TestIssue4697_SaveSurfacesExternalSectionsWired(t *testing.T) {
	data, err := os.ReadFile("config_save.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `if err := saveExternalSections(c, extDir, vendorsToSave); err != nil {`) {
		t.Fatal("Save must propagate saveExternalSections failures (#4697 defect B)")
	}
}
