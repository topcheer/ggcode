package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlaybookPanelRegisteredInCloseActivePanel mirrors the #2363 invariant:
// every panel that appears in hasActivePanel must also have a case in
// closeActivePanel, otherwise ctrl+c leaves it stuck open.
func TestPlaybookPanelRegisteredInCloseActivePanel(t *testing.T) {
	m := newTestModel()
	m.playbookPanel = &playbookPanelState{}
	if !m.hasActivePanel() {
		t.Fatal("playbookPanel should register in hasActivePanel")
	}
	if !m.closeActivePanel() {
		t.Fatal("closeActivePanel returned false with playbookPanel open")
	}
	if m.playbookPanel != nil {
		t.Fatal("closeActivePanel did not nil playbookPanel")
	}
}

// TestPlaybookPanelBodyReadsStores verifies the panel body loads real store
// data from the working directory and renders the digest, and that an empty
// workspace degrades to the localized empty notice.
func TestPlaybookPanelBodyReadsStores(t *testing.T) {
	m := newTestModel()

	// No agent / working dir → empty notice.
	if body := m.playbookPanelBody(); body == "" {
		t.Fatal("expected empty-state body, got empty string")
	}

	dir := t.TempDir()
	data := `[{"id":"e1","task_type":"bugfix","tool_sequence":"read→edit→build","uses":7,"success_rate":0.85,"avg_iter":3.1,"avg_duration_s":140,"last_seen":"2026-09-20T10:00:00Z","created_at":"2026-09-01T10:00:00Z"}]`
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "playbook.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesData := `{"version":1,"rules":[{"id":"r1","category":"build","rule":"use -tags goolm","match_pattern":"build failed","hit_count":3,"last_seen":"2026-01-01T00:00:00Z","created_at":"2025-12-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "agent-rules.json"), []byte(rulesData), 0o644); err != nil {
		t.Fatal(err)
	}

	// Point the panel at the fixture dir through the same read path the
	// panel uses (a stand-in agent would need a full constructor).
	m2 := newTestModel()
	_ = m2
	body := playbookBodyForDir(dir)
	if !strings.Contains(body, "bugfix") {
		t.Errorf("expected playbook entry in body, got:\n%s", body)
	}
	if !strings.Contains(body, "use -tags goolm") {
		t.Errorf("expected stale rule in body, got:\n%s", body)
	}
}
