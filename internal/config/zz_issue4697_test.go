package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4697 (defect A): a REAL read error (not file-missing) must abort Save -
// the old code fell into the full-write branch, skipping deep-merge and
// tombstone protection, silently wiping disk-only fields and reporting
// success. A directory-as-path yields EISDIR (not IsNotExist) while the
// parent stays writable, matching the filed trigger.
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

	// Swap the config path to a DIRECTORY after loading: ReadFile in Save
	// fails with EISDIR (a real read error, not IsNotExist).
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

// #4697 (defect B): an external-section write failure must surface from
// Save() - the old discard reported success while vendors.yaml silently
// failed and the config reverted on restart.
func TestIssue4697_ExternalSectionFailureSurfaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(home, "ggcode.yaml")
	cfg, err := LoadWithInstance(cfgPath, t.TempDir())
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	// Force a non-empty vendor set so the baseline Save definitely writes
	// vendors.yaml (an empty/fully-default set may be skipped by SaveVendors'
	// strip logic, which made this test environment-dependent).
	cfg.Vendors["t4697"] = VendorConfig{APIKey: "k4697"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("baseline Save (writable dirs) must succeed: %v", err)
	}

	// Locate the persisted vendors.yaml deterministically (same resolution
	// Save uses) and poison it: replace with a directory so SaveVendors
	// fails while everything else stays writable.
	vendorsPath := VendorsPath(filepath.Dir(cfg.FilePath))
	if _, serr := os.Stat(vendorsPath); serr != nil {
		t.Fatalf("baseline Save must persist vendors.yaml at %s: %v", vendorsPath, serr)
	}
	if err := os.Remove(vendorsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vendorsPath, 0700); err != nil {
		t.Fatal(err)
	}
	err = cfg.Save()
	if err == nil || !strings.Contains(err.Error(), "external sections") {
		t.Fatalf("Save must surface the external-section write failure, got %v (#4697)", err)
	}
}
