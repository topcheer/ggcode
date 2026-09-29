package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/commands"
)

func newDraftSkillsTestModel(t *testing.T, withLiveSkill bool) *Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // isolate SetEnabled persistence
	project := t.TempDir()
	draftDir := filepath.Join(project, ".ggcode", "skill-drafts")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	draft := "---\nname: build-20260929-run1\ndescription: fix the failing build\nstatus: draft\n---\n\n# draft\n"
	if err := os.WriteFile(filepath.Join(draftDir, "build-20260929-run1.md"), []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	if withLiveSkill {
		skillDir := filepath.Join(project, ".ggcode", "skills", "toggle-target")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: toggle-target\ndescription: t\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := &Model{commandMgr: commands.NewManager(project)}
	m.openSkillsPanel()
	return m
}

func pressKey(m *Model, key string) {
	var msg tea.KeyPressMsg
	if key == "space" {
		msg = tea.KeyPressMsg{Code: tea.KeySpace}
	} else {
		msg = tea.KeyPressMsg{Text: key}
	}
	updated, _ := m.handleSkillsPanelKey(msg)
	*m = updated
}

// liveIndexOf returns the draft-offset cursor index of a named live skill in
// panel render order (drafts first, then grouped live skills).
func liveIndexOf(m *Model, name string) int {
	skills := m.listSkills()
	start := 0
	if m.skillsPanel != nil {
		page := clampSkillsPage(m.skillsPanel.page, skillsPageCount(len(skills)))
		start = page * skillsPerPage
	}
	end := start + skillsPerPage
	if end > len(skills) {
		end = len(skills)
	}
	for i, cmd := range groupedSkillsFlat(skills[start:end]) {
		if cmd != nil && cmd.Name == name {
			return len(m.listSkillDrafts()) + i
		}
	}
	return -1
}

func TestSkillsPanelShowsDraftsAndActivates(t *testing.T) {
	m := newDraftSkillsTestModel(t, false)
	project := m.commandMgr.ProjectDir()

	out := m.renderSkillsPanel()
	if !strings.Contains(out, "Draft skills (pending review)") {
		t.Fatalf("panel does not surface drafts:\n%s", out)
	}
	if !strings.Contains(out, "build-20260929-run1") {
		t.Fatalf("draft name missing from panel:\n%s", out)
	}

	// Cursor 0 = the draft. Space must NOT toggle anything, just hint.
	pressKey(m, "space")
	if m.skillsPanel.status != "draft skill: press a to activate" {
		t.Fatalf("space on draft should hint activation, got %q", m.skillsPanel.status)
	}
	if _, err := os.Stat(filepath.Join(project, ".ggcode", "skills", "build-20260929-run1")); !os.IsNotExist(err) {
		t.Fatal("space on draft must not create the skill")
	}

	// 'a' activates: draft moves into the live project registry.
	pressKey(m, "a")
	activated := filepath.Join(project, ".ggcode", "skills", "build-20260929-run1", "SKILL.md")
	if _, err := os.Stat(activated); err != nil {
		t.Fatalf("activated skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".ggcode", "skill-drafts", "build-20260929-run1.md")); !os.IsNotExist(err) {
		t.Fatal("draft file should be removed after activation")
	}
	if !strings.Contains(m.skillsPanel.status, "activated build-20260929-run1") {
		t.Fatalf("status = %q", m.skillsPanel.status)
	}
	found := false
	for _, cmd := range m.listSkills() {
		if cmd.Name == "build-20260929-run1" {
			found = true
		}
	}
	if !found {
		t.Error("activated skill not visible in live skill list")
	}
	// The draft section is gone after activation.
	if out := m.renderSkillsPanel(); strings.Contains(out, "Draft skills (pending review)") {
		t.Fatalf("draft section should disappear after activation:\n%s", out)
	}
}

func TestSkillsPanelDraftCursorBounds(t *testing.T) {
	m := newDraftSkillsTestModel(t, true)

	// 1 draft (cursor 0) + N live skills (bundled + project): cursor starts
	// on the draft and can move into live territory.
	if m.skillsPanel.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0 (draft first)", m.skillsPanel.cursor)
	}
	pressKey(m, "down")
	if m.skillsPanel.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after first down", m.skillsPanel.cursor)
	}
	// Walk to the bottom; further downs must be bounded, not panic.
	for i := 0; i < 100; i++ {
		pressKey(m, "down")
	}
	bottom := m.skillsPanel.cursor
	pressKey(m, "down")
	if m.skillsPanel.cursor != bottom {
		t.Fatalf("cursor not bounded: %d -> %d", bottom, m.skillsPanel.cursor)
	}

	// 'a' with the cursor on a live skill is a no-op.
	m.skillsPanel.cursor = len(m.listSkillDrafts()) // first live skill
	pressKey(m, "a")
	if m.skillsPanel.status != "" {
		t.Fatalf("activate beyond drafts should be a no-op, got %q", m.skillsPanel.status)
	}
	// Draft file still present.
	if _, err := os.Stat(filepath.Join(m.commandMgr.ProjectDir(), ".ggcode", "skill-drafts", "build-20260929-run1.md")); err != nil {
		t.Fatal("draft must remain when 'a' targets a live skill")
	}
}

func TestSkillsPanelToggleSkipsDraftIndex(t *testing.T) {
	m := newDraftSkillsTestModel(t, true)

	// Space at the draft index hints; it must not toggle any live skill.
	pressKey(m, "space")
	if cmd, ok := m.commandMgr.Get("toggle-target"); ok && cmd != nil && !cmd.Enabled {
		t.Error("space on the draft index disabled a live skill")
	}

	// Space at toggle-target's real render position toggles it.
	pos := liveIndexOf(m, "toggle-target")
	if pos < 0 {
		t.Fatal("toggle-target not found in panel render order")
	}
	m.skillsPanel.cursor = pos
	pressKey(m, "space")
	if cmd, ok := m.commandMgr.Get("toggle-target"); !ok || cmd == nil {
		t.Fatal("toggle-target skill not loaded")
	} else if cmd.Enabled {
		t.Error("space at toggle-target's index did not toggle it")
	}
}
