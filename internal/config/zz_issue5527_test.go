package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIssue5527DryRunRoundsPins covers the #5527 opt-in dry-run flags on the
// memory.curator config surface: parse, accessor defaults, negative clamp,
// and the default-off (nil) byte-identical contract.
func TestIssue5527DryRunRoundsPins(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	yaml := `memory:
  curator:
    sweep: true
    dry_run_rounds: 3
    dry_run: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.MemoryCuratorSweepDryRunRounds(); got != 3 {
		t.Errorf("dry_run_rounds accessor = %d, want 3", got)
	}
	if !cfg.MemoryCuratorSweepForceDryRun() {
		t.Error("dry_run accessor = false, want true")
	}
	if !cfg.MemoryCuratorSweepEnabled() {
		t.Error("sweep accessor = false, want true")
	}
}

// TestIssue5527DryRunDefaultsOff pins the default (flag absent) contract:
// both dry-run surfaces read as off — the autonomy path stays
// byte-identical (same class as #5511's MaxNoDiffRounds default).
func TestIssue5527DryRunDefaultsOff(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	if err := os.WriteFile(path, []byte("language: en\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.MemoryCuratorSweepDryRunRounds(); got != 0 {
		t.Errorf("absent dry_run_rounds must read 0, got %d", got)
	}
	if cfg.MemoryCuratorSweepForceDryRun() {
		t.Error("absent dry_run must read false")
	}
}

// TestIssue5527NegativeRoundsClamped pins that a negative N is clamped to 0
// (off) rather than enabling anything.
func TestIssue5527NegativeRoundsClamped(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	yaml := "memory:\n  curator:\n    dry_run_rounds: -2\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.MemoryCuratorSweepDryRunRounds(); got != 0 {
		t.Errorf("negative dry_run_rounds must clamp to 0, got %d", got)
	}
}
