package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/knight"
)

func new3408Model(t *testing.T) (Model, string, string) {
	t.Helper()
	home := t.TempDir()
	proj := t.TempDir()
	stagingDir := filepath.Join(home, ".ggcode", "skills-staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, desc := range map[string]string{
		"aaa-first.md":  "first global skill",
		"zzz-second.md": "second global skill",
	} {
		skill := "---\nname: " + name[:len(name)-3] + "\ndescription: " + desc + "\n---\n\n## Steps\n\n1. do the thing\n"
		if err := os.WriteFile(filepath.Join(stagingDir, name), []byte(skill), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k := knight.New(config.KnightConfig{}, home, proj, nil)
	staging, err := k.Index().StagingSkills()
	if err != nil || len(staging) != 2 {
		t.Fatalf("seed staging: %v (%d)", err, len(staging))
	}
	m := newTestModel()
	m.knight = k
	m.knightPanel = newKnightPanel()
	return m, staging[0].Path, staging[1].Path
}

func TestIssue3408_RejectShiftsIndex_NoUnseenPromote(t *testing.T) {
	m, pathA, pathB := new3408Model(t)
	m.knightPanel.focus = 1
	m.knightPanel.selectedIndex = 4

	out, _ := m.knightPanelAction("staging", 0, "approve")
	m1, ok := asModel(out)
	if !ok || m1.knightPanel.pendingGlobalApprovePath != pathA {
		t.Fatalf("arm A failed: pending=%q", m1.knightPanel.pendingGlobalApprovePath)
	}

	out, _ = m1.knightPanelAction("staging", 0, "reject")
	m2, ok := asModel(out)
	if !ok || stagingCount(t, m2) != 1 {
		t.Fatalf("reject A failed, staging=%d", stagingCount(t, m2))
	}

	out, _ = m2.knightPanelAction("staging", 0, "approve")
	m3, ok := asModel(out)
	if !ok || stagingCount(t, m3) != 1 {
		t.Fatal("post-reject [a] on the shifted skill promoted it WITHOUT its own confirmation")
	}
	if m3.knightPanel.pendingGlobalApprovePath != pathB {
		t.Fatalf("post-reject [a] must arm B's path, got %q", m3.knightPanel.pendingGlobalApprovePath)
	}
}

func TestIssue3408_ArmedStateResetsOnContextSwitch(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"left-nav", tea.KeyPressMsg{Code: tea.KeyDown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, pathA, _ := new3408Model(t)
			m.knightPanel.focus = 1
			m.knightPanel.selectedIndex = 4
			m.knightPanel.pendingGlobalApprovePath = pathA
			out, _ := m.updateKnightPanel(tc.key)
			after, ok := asModel(out)
			if !ok || after.knightPanel.pendingGlobalApprovePath != "" {
				t.Fatalf("%s must cancel the armed confirm, got %q", tc.name, after.knightPanel.pendingGlobalApprovePath)
			}
		})
	}
}

func TestIssue3408_RejectActionClearsArmedState(t *testing.T) {
	m, pathA, _ := new3408Model(t)
	m.knightPanel.selectedIndex = 4
	m.knightPanel.pendingGlobalApprovePath = pathA
	out, _ := m.knightPanelAction("staging", 0, "reject")
	after, ok := asModel(out)
	if !ok || after.knightPanel.pendingGlobalApprovePath != "" {
		t.Fatalf("reject must clear the armed state, got %q", after.knightPanel.pendingGlobalApprovePath)
	}
}
