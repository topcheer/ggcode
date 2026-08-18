package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMergeA2AConfigExplicitFalseReenables verifies #665: a workspace
// .ggcode/a2a.yaml with an explicit `disabled: false` must re-enable an A2A
// that was disabled in the global config (instance-wins semantics, matching
// instance_delta.go's bidirectional diffA2A handling).
func TestMergeA2AConfigExplicitFalseReenables(t *testing.T) {
	dir := t.TempDir()
	ggcodeDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte("disabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	override := LoadA2AOverride(dir)
	if override == nil {
		t.Fatal("expected non-nil override")
	}
	base := &A2AConfig{Disabled: true}
	MergeA2AConfig(base, override)
	if base.Disabled {
		t.Error("explicit `disabled: false` in workspace a2a.yaml should re-enable globally disabled A2A (#665)")
	}
}

// TestMergeA2AConfigExplicitTrueDisables: explicit `disabled: true` still
// disables (the pre-existing one-way direction keeps working).
func TestMergeA2AConfigExplicitTrueDisables(t *testing.T) {
	dir := t.TempDir()
	ggcodeDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte("disabled: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	base := &A2AConfig{Disabled: false}
	MergeA2AConfig(base, LoadA2AOverride(dir))
	if !base.Disabled {
		t.Error("explicit `disabled: true` should disable A2A")
	}
}

// TestMergeA2AConfigAbsentKeyPreservesGlobal: when the key is absent from
// the override yaml entirely, the global value must be preserved (no
// accidental default-false takeover).
func TestMergeA2AConfigAbsentKeyPreservesGlobal(t *testing.T) {
	dir := t.TempDir()
	ggcodeDir := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte("port: 9000\n"), 0644); err != nil {
		t.Fatal(err)
	}

	base := &A2AConfig{Disabled: true}
	MergeA2AConfig(base, LoadA2AOverride(dir))
	if !base.Disabled {
		t.Error("absent `disabled` key must preserve the global disabled=true value")
	}
	if base.Port != 9000 {
		t.Errorf("other fields still merge: port got %d, want 9000", base.Port)
	}
}

// TestMergeA2AConfigProgrammaticLegacyOneWay: programmatically constructed
// overrides (no explicit yaml marker) keep the legacy one-way behavior —
// Disabled=true disables, Disabled=false is a no-op.
func TestMergeA2AConfigProgrammaticLegacyOneWay(t *testing.T) {
	base := &A2AConfig{Disabled: false}
	MergeA2AConfig(base, &A2AConfig{Disabled: true})
	if !base.Disabled {
		t.Error("legacy path: programmatic Disabled=true should still disable")
	}

	base2 := &A2AConfig{Disabled: true}
	MergeA2AConfig(base2, &A2AConfig{Disabled: false})
	if !base2.Disabled {
		t.Error("legacy path: programmatic Disabled=false (zero-value, no marker) must remain a no-op")
	}
}
