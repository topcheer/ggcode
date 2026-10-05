package tui

// #3391 probes: a single [a] on a GLOBAL-scope staging skill must not
// promote it - the panel mirrors the command path's --confirm-global gate
// (#1363: a warning followed by immediate effect in the same keypress is
// not a confirmation gate). Project-scope skills promote on first press.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
)

func stagingModel3391(t *testing.T, home, proj, stagingDir, name, scope string) (Model, string) {
	t.Helper()
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(stagingDir, "knight-20260420-"+name+".md")
	body := "---\nname: " + name + "\ndescription: probe skill\nscope: " + scope + "\ncreated_by: knight\n---\n# Probe\n\n## Steps\n1. Do the thing\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	k := knight.New(config.KnightConfig{Enabled: true}, home, proj, nil)
	if err := k.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { k.Stop() })
	m := newTestModel()
	m.SetKnight(k)
	m.knightPanel = newKnightPanel()
	return m, path
}

func TestIssue3391_GlobalApproveNeedsSecondKeypress(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	m, path := stagingModel3391(t, home, proj, filepath.Join(home, ".ggcode", "skills-staging"), "probe-global", "global")

	// First [a]: warns and arms - the staging file must still exist.
	m1, _ := m.knightPanelAction("staging", 0, "approve")
	mm, ok := m1.(*Model)
	if !ok {
		t.Fatalf("knightPanelAction returned %T", m1)
	}
	if !strings.Contains(mm.knightPanel.message, "GLOBAL scope") {
		t.Fatalf("first press must warn about GLOBAL scope, got %q", mm.knightPanel.message)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("first press must NOT promote a global staging skill: %v", err)
	}

	// Second [a] within the window: promotes.
	m2, _ := mm.knightPanelAction("staging", 0, "approve")
	mm2, ok := m2.(*Model)
	if !ok {
		t.Fatalf("knightPanelAction returned %T", m2)
	}
	if !strings.Contains(mm2.knightPanel.message, "Approved") {
		t.Fatalf("second press must promote, got %q", mm2.knightPanel.message)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("confirmed press must promote the staging file out of staging")
	}
}

func TestIssue3391_ArmExpiry(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	m, path := stagingModel3391(t, home, proj, filepath.Join(home, ".ggcode", "skills-staging"), "probe-expire", "global")

	m1, _ := m.knightPanelAction("staging", 0, "approve")
	mm, _ := m1.(*Model)
	// Simulate the arm window elapsing.
	mm.knightPanel.globalConfirmAt = time.Now().Add(-11 * time.Second)
	m2, _ := mm.knightPanelAction("staging", 0, "approve")
	mm2, _ := m2.(*Model)
	if !strings.Contains(mm2.knightPanel.message, "GLOBAL scope") {
		t.Fatalf("expired arm must re-warn, not promote, got %q", mm2.knightPanel.message)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expired arm must not promote: %v", err)
	}
}

func TestIssue3391_ProjectScopeImmediate(t *testing.T) {
	proj := t.TempDir()
	home := t.TempDir()
	m, path := stagingModel3391(t, home, proj, filepath.Join(proj, ".ggcode", "skills-staging"), "probe-proj", "project")

	m1, _ := m.knightPanelAction("staging", 0, "approve")
	mm, ok := m1.(*Model)
	if !ok {
		t.Fatalf("knightPanelAction returned %T", m1)
	}
	if !strings.Contains(mm.knightPanel.message, "Approved") {
		t.Fatalf("project scope must promote on first press, got %q", mm.knightPanel.message)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("project-scope press must promote immediately")
	}
}
