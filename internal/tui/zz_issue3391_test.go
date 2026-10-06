package tui

// #3391 probes: a single [a] on a GLOBAL-scope staging skill must not take
// immediate effect - the panel mirrors the command path's --confirm-global
// gate (knight_commands.go: "a warning that is followed by immediate effect
// in the same keypress is not a confirmation gate"). Project scope stays
// single-key. #3389 probe: the navigation domain and the render fetches
// share one list limit constant (pre-fix: count fetched 50, renderers 20,
// the down key walked into items the panel never showed).

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

	// First [a]: arms the confirm, does NOT promote.
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

	// Second [a] at the same index: executes.
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
	// knightSections order: status, budget, queue, skills, staging, ...
	m.knightPanel.selectedIndex = 4

	// Arm, then navigate: the pending confirm must reset so a later [a] at
	// the same visible index re-arms instead of executing.
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

	// Re-pressing [a] after navigation must re-arm (not execute).
	out, _ := m.knightPanelAction("staging", 0, "approve")
	mOut, ok := asModel(out)
	if !ok || stagingCount(t, mOut) != 1 {
		t.Fatal("post-navigation [a] must re-arm, not execute")
	}
}

// #3389: navigation domain and render fetches must share the same limit.
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
