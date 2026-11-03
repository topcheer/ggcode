package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
)

func new3391Model(t *testing.T) (Model, string) {
	t.Helper()
	home := t.TempDir()
	proj := t.TempDir()
	stagingDir := filepath.Join(home, ".ggcode", "skills-staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: risky-global\ndescription: injected into every project\n---\n\n## Steps\n\n1. do the thing\n"
	if err := os.WriteFile(filepath.Join(stagingDir, "risky-global.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	k := knight.New(config.KnightConfig{}, home, proj, nil)
	staging, err := k.Index().StagingSkills()
	if err != nil || len(staging) != 1 {
		t.Fatalf("seed staging: %v (%d)", err, len(staging))
	}
	m := newTestModel()
	m.knight = k
	m.knightPanel = newKnightPanel()
	return m, staging[0].Path
}

func stagingCount(t *testing.T, m Model) int {
	t.Helper()
	s, err := m.knight.Index().StagingSkills()
	if err != nil {
		t.Fatal(err)
	}
	return len(s)
}

func asModel(v any) (Model, bool) {
	switch t := v.(type) {
	case Model:
		return t, true
	case *Model:
		return *t, true
	}
	return Model{}, false
}

func TestIssue3391_GlobalApproveNeedsSecondKeypress(t *testing.T) {
	m, skillPath := new3391Model(t)

	out, _ := m.knightPanelAction("staging", 0, "approve")
	m1, ok := asModel(out)
	if !ok {
		t.Fatalf("returned %T", out)
	}
	if stagingCount(t, m1) != 1 {
		t.Fatal("first [a] on a global staging skill must NOT promote")
	}
	if !strings.Contains(m1.knightPanel.message, "global") || m1.knightPanel.pendingGlobalApprovePath != skillPath {
		t.Fatalf("first [a] must arm the confirm: msg=%q pending=%q", m1.knightPanel.message, m1.knightPanel.pendingGlobalApprovePath)
	}

	out, _ = m1.knightPanelAction("staging", 0, "approve")
	m2, ok := asModel(out)
	if !ok {
		t.Fatalf("returned %T", out)
	}
	if stagingCount(t, m2) != 0 {
		t.Fatal("second [a] must promote the global staging skill")
	}
	if m2.knightPanel.pendingGlobalApprovePath != "" {
		t.Fatalf("pending must reset after execution, got %q", m2.knightPanel.pendingGlobalApprovePath)
	}
}

func TestIssue3391_NavigationCancelsArmedConfirm(t *testing.T) {
	m, skillPath := new3391Model(t)
	m.knightPanel.focus = 1

	m.knightPanel.selectedIndex = 4

	outArm, _ := m.knightPanelAction("staging", 0, "approve")
	mArm, ok := asModel(outArm)
	if !ok || mArm.knightPanel.pendingGlobalApprovePath != skillPath {
		t.Fatal("arm failed")
	}
	m.knightPanel.pendingGlobalApprovePath = skillPath
	navModel, _ := m.updateKnightPanelRight(tea.KeyPressMsg{Code: tea.KeyDown})
	nav, ok := asModel(navModel)
	if !ok || nav.knightPanel.pendingGlobalApprovePath != "" {
		t.Fatal("navigation must cancel the armed global confirm")
	}

	out, _ := m.knightPanelAction("staging", 0, "approve")
	mOut, ok := asModel(out)
	if !ok || stagingCount(t, mOut) != 1 {
		t.Fatal("post-navigation [a] must re-arm, not execute")
	}
}

func TestIssue3389_CountAndRenderShareListLimit(t *testing.T) {
	src, err := os.ReadFile("knight_panel.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, call := range []string{"RecentProjectImprovementProposals(", "RecentSemanticMemory("} {
		for _, lit := range []string{"(50)", "(20)"} {
			if strings.Contains(text, call+lit[1:len(lit)-1]+")") {
				t.Fatalf("%s still called with a literal %s - count and render must share knightPanelListLimit", call, lit)
			}
		}
	}
	if !strings.Contains(text, "knightPanelListLimit") {
		t.Fatal("knightPanelListLimit const must exist")
	}
}
