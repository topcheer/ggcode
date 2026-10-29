package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// newImpersonateTestModel builds the minimal Model needed to drive
// applyImpersonateSettings: a panel with a chosen version and a config rooted
// at dir.
func newImpersonateTestModel(t *testing.T, dir, version string) *Model {
	t.Helper()
	cfg, err := config.Load(filepath.Join(dir, "ggcode-test.yaml"))
	if err != nil {
		t.Skipf("config load in temp dir failed: %v", err)
	}
	vi := textinput.New()
	vi.SetValue(version)
	m := &Model{config: cfg}
	m.impersonatePanel = &impersonatePanelState{
		currentPreset: "none",
		versionInput:  vi,
		editingHeader: -1,
	}
	return m
}

// #4590: a failed SaveImpersonation must roll the runtime impersonation back
// to the previous identity - runtime and disk must not fork.
func TestIssue4590_PersistFailureRollsBackRuntime(t *testing.T) {
	// Failure injection: seed a plain writable config file first (so Load
	// succeeds and pins the path), then swap the file for a directory.
	// patchConfigFile's os.ReadFile then fails with EISDIR (not IsNotExist),
	// so the persist reliably errors on every platform.
	dir := t.TempDir()
	fp := filepath.Join(dir, "ggcode-test.yaml")
	if err := os.WriteFile(fp, []byte("version: 1.3.0\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// Establish the "old" identity the disk will keep.
	oldHdrs := map[string]string{"X-Old": "1"}
	provider.SetActiveImpersonation(nil, "v100.0", oldHdrs)
	t.Cleanup(func() { provider.SetActiveImpersonation(nil, "", nil) })

	// Load while the path is still a valid file (pins the config path).
	m := newImpersonateTestModel(t, dir, "v131.0")

	// Now swap the file for a directory: the next SaveImpersonation
	// reliably fails with EISDIR.
	if err := os.Remove(fp); err != nil {
		t.Fatalf("remove seed: %v", err)
	}
	if err := os.MkdirAll(fp, 0o755); err != nil {
		t.Fatalf("swap to dir: %v", err)
	}

	_ = m.applyImpersonateSettings()

	preset, version, headers := provider.GetActiveImpersonation()
	if version != "v100.0" {
		t.Fatalf("#4590 runtime/disk fork: version=%q, want rolled back v100.0", version)
	}
	if headers["X-Old"] != "1" {
		t.Fatalf("#4590 headers not restored: %v", headers)
	}
	if preset != nil {
		t.Fatalf("#4590 preset not restored: %v", preset)
	}
	if !m.impersonatePanel.persistFailed {
		t.Fatal("persistFailed must remain set after rollback")
	}
	if !strings.Contains(m.impersonatePanel.message, "identity not applied") {
		t.Fatalf("message should explain identity not applied, got %q", m.impersonatePanel.message)
	}
}

// Happy path: writable directory keeps the NEW identity (no over-rollback).
func TestIssue4590_SuccessKeepsNewIdentity(t *testing.T) {
	dir := t.TempDir()
	provider.SetActiveImpersonation(nil, "", nil)
	t.Cleanup(func() { provider.SetActiveImpersonation(nil, "", nil) })

	m := newImpersonateTestModel(t, dir, "v131.0")
	_ = m.applyImpersonateSettings()

	_, version, _ := provider.GetActiveImpersonation()
	if version != "v131.0" {
		t.Fatalf("happy path must keep new version, got %q", version)
	}
	if m.impersonatePanel.persistFailed {
		t.Fatal("persistFailed must be false on success")
	}
}
