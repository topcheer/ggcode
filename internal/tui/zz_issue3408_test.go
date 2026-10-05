package tui

// #3408 probes: the global-promote confirm gate (#3391) keyed its armed
// state by staging LIST INDEX, but the list is re-read on every keypress -
// after a reject (or a promote from another surface) the same index points
// at a different skill, and the second [a] would promote a skill the user
// never saw a confirmation for. The fix keys the armed state by skill PATH
// and resets it on every non-confirm keypress (tab / esc / left navigation
// / reject / freeze / unfreeze / delete).

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
	// File names force a stable listing order: aaa-first < zzz-second.
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

// Core scenario from the issue: arm skill A, reject it, the surviving skill
// B slides into A's index - a first [a] on B must RE-ARM (show B's own
// confirmation), never promote B unseen.
func TestIssue3408_RejectShiftsIndex_NoUnseenPromote(t *testing.T) {
	m, pathA, pathB := new3408Model(t)
	m.knightPanel.focus = 1
	m.knightPanel.selectedIndex = 4 // staging section

	// Arm A at idx 0.
	out, _ := m.knightPanelAction("staging", 0, "approve")
	m1, ok := asModel(out)
	if !ok || m1.knightPanel.pendingGlobalApprovePath != pathA {
		t.Fatalf("arm A failed: pending=%q", m1.knightPanel.pendingGlobalApprovePath)
	}

	// Reject A: B now occupies idx 0.
	out, _ = m1.knightPanelAction("staging", 0, "reject")
	m2, ok := asModel(out)
	if !ok || stagingCount(t, m2) != 1 {
		t.Fatalf("reject A failed, staging=%d", stagingCount(t, m2))
	}

	// First [a] on B (same idx 0): must re-arm against B's path, not execute.
	out, _ = m2.knightPanelAction("staging", 0, "approve")
	m3, ok := asModel(out)
	if !ok || stagingCount(t, m3) != 1 {
		t.Fatal("post-reject [a] on the shifted skill promoted it WITHOUT its own confirmation")
	}
	if m3.knightPanel.pendingGlobalApprovePath != pathB {
		t.Fatalf("post-reject [a] must arm B's path, got %q", m3.knightPanel.pendingGlobalApprovePath)
	}
}

// Armed state must not survive context switches: tab, esc-to-left, and any
// left-panel navigation all cancel it.
func TestIssue3408_ArmedStateResetsOnContextSwitch(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"left-nav", tea.KeyPressMsg{Code: tea.KeyDown}}, // routed via left when focus=0
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

// Direct panel-action path: rejecting any skill also clears the armed state
// (defense in depth next to the key-level reset in updateKnightPanelRight).
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
