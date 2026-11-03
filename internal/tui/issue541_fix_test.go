package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/session"
)

func TestStartNormalTextRunStoresLastUserSubmission(t *testing.T) {
	m := newTestModel()
	if m.lastUserSubmission != "" {
		t.Fatalf("precondition: expected empty lastUserSubmission, got %q", m.lastUserSubmission)
	}
	_ = m.startNormalTextRun("fix the login bug", "fix the login bug", true)
	if m.lastUserSubmission != "fix the login bug" {
		t.Fatalf("startNormalTextRun did not store submission: got %q", m.lastUserSubmission)
	}
}

func TestEditCommandLoadsStoredSubmission(t *testing.T) {
	m := newTestModel()
	m.lastUserSubmission = "original prompt"
	cmd := m.handleEditCommand()
	if cmd != nil {
		t.Fatalf("handleEditCommand returned a command, expected nil")
	}
	if got := m.input.Value(); got != "original prompt" {
		t.Fatalf("input not populated from lastUserSubmission: got %q", got)
	}
}

func TestUndoRunBlockedWhileAgentRunning(t *testing.T) {
	m := newTestModel()
	m.setLoading(true)
	cmd := m.handleUndoRunCommand()
	if cmd != nil {
		t.Fatalf("handleUndoRunCommand must be a no-op while loading, got cmd %v", cmd)
	}
}

func TestUndoRunAllowedWhenIdle(t *testing.T) {
	m := newTestModel()

	cmd := m.handleUndoRunCommand()
	if cmd == nil {
		t.Fatalf("handleUndoRunCommand must proceed when idle, got nil cmd")
	}
}

func TestUndoRunNotWhitelistedWhileBusy(t *testing.T) {
	if shouldExecuteWhileBusy("/undo-run") {
		t.Fatalf("/undo-run must not execute while the agent is busy (would race agent writes)")
	}

	if !shouldExecuteWhileBusy("/pin") {
		t.Fatalf("/pin should still be allowed while busy")
	}
}

func mkCp(runID, file, old, new string) checkpoint.Checkpoint {
	return checkpoint.Checkpoint{RunID: runID, FilePath: file, OldContent: old, NewContent: new}
}

func TestAccumulateRunChangesMultipleEditsSameFile(t *testing.T) {

	cps := []checkpoint.Checkpoint{
		mkCp("r1", "a.go", "l1\nl2\nl3\n", "l1\nX\nl3\n"),
		mkCp("r1", "a.go", "l1\nX\nl3\n", "l1\nX\nl3\nl4\n"),
	}
	runFiles := map[string]bool{"a.go": true}
	changes := accumulateRunChanges(cps, "r1", runFiles)
	fc := changes["a.go"]
	if fc == nil {
		t.Fatalf("expected change entry for a.go")
	}
	if fc.edits != 2 {
		t.Fatalf("expected 2 accumulated edits, got %d", fc.edits)
	}

	if fc.added != 2 || fc.deleted != 1 {
		t.Fatalf("expected +2 -1 net diff across all checkpoints, got +%d -%d", fc.added, fc.deleted)
	}
}

func TestAccumulateRunChangesFiltersByRunID(t *testing.T) {
	cps := []checkpoint.Checkpoint{
		mkCp("r1", "a.go", "", "old run content\n"),
		mkCp("r2", "a.go", "old run content\n", "old run content\nnew\n"),
	}
	runFiles := map[string]bool{"a.go": true}
	changes := accumulateRunChanges(cps, "r2", runFiles)
	fc := changes["a.go"]
	if fc == nil || fc.edits != 1 {
		t.Fatalf("expected exactly 1 edit counted for run r2, got %+v", fc)
	}
	if fc.added != 1 || fc.deleted != 0 {
		t.Fatalf("run r2 summary contaminated by r1 checkpoint: +%d -%d", fc.added, fc.deleted)
	}
}

func TestAccumulateRunChangesIsNewNotFooledByEmptiedExistingFile(t *testing.T) {

	cps := []checkpoint.Checkpoint{
		mkCp("r1", "a.go", "real content\n", ""),
		mkCp("r2", "a.go", "", "new content\n"),
	}
	runFiles := map[string]bool{"a.go": true}
	changes := accumulateRunChanges(cps, "r2", runFiles)
	if fc := changes["a.go"]; fc == nil || fc.isNew {
		t.Fatalf("emptied pre-existing file must not be reported as new: %+v", changes["a.go"])
	}

	cps2 := []checkpoint.Checkpoint{mkCp("r1", "b.go", "", "fresh\n")}
	changes2 := accumulateRunChanges(cps2, "r1", map[string]bool{"b.go": true})
	if fc := changes2["b.go"]; fc == nil || !fc.isNew {
		t.Fatalf("genuinely new file must be reported as new: %+v", changes2["b.go"])
	}
}

func mkSes(id string) *session.Session {
	return &session.Session{ID: id, Title: id}
}

func TestNextSessionInCycleCurrentNotListed(t *testing.T) {

	sessions := []*session.Session{mkSes("s1"), mkSes("s2")}
	current := mkSes("fresh")

	got := nextSessionInCycle(sessions, current, 1)
	if got == nil || got.ID != "s1" {
		t.Fatalf("forward cycle from unlisted current: got %v, want s1", got)
	}

	got = nextSessionInCycle(sessions, current, -1)
	if got == nil || got.ID != "s2" {
		t.Fatalf("backward cycle from unlisted current: got %v, want s2", got)
	}
}

func TestNextSessionInCycleCurrentListed(t *testing.T) {
	sessions := []*session.Session{mkSes("s1"), mkSes("s2"), mkSes("s3")}
	got := nextSessionInCycle(sessions, mkSes("s2"), 1)
	if got == nil || got.ID != "s3" {
		t.Fatalf("forward from listed current: got %v, want s3", got)
	}
	got = nextSessionInCycle(sessions, mkSes("s1"), -1)
	if got == nil || got.ID != "s3" {
		t.Fatalf("backward wrap from listed current: got %v, want s3", got)
	}
}

func TestNextSessionInCycleOnlyCurrentSession(t *testing.T) {

	if got := nextSessionInCycle(nil, mkSes("fresh"), 1); got != nil {
		t.Fatalf("expected no switch when only the current session exists, got %v", got)
	}
}

func TestMouseWheelScrollsActivePanelViewport(t *testing.T) {
	m := newTestModel()
	m.statsPanel = &statsPanelState{viewport: newViewport()}
	m.statsPanel.viewport.SetSize(40, 5)
	m.statsPanel.viewport.SetContent(strings.Repeat("panel line\n", 40))
	if m.statsPanel.viewport.YOffset() != 0 {
		t.Fatalf("precondition: viewport must start at top")
	}

	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	um, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update did not return a Model")
	}
	if um.statsPanel == nil {
		t.Fatalf("stats panel lost during update")
	}
	if um.statsPanel.viewport.YOffset() == 0 {
		t.Fatalf("mouse wheel down did not scroll the active panel viewport (YOffset still 0)")
	}
}

func TestMouseWheelFallsBackToChatListWithoutPanel(t *testing.T) {
	m := newTestModel()
	if vp := m.activePanelViewport(); vp != nil {
		t.Fatalf("expected nil viewport when no panel is open")
	}

	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if _, ok := updated.(Model); !ok {
		t.Fatalf("Update did not return a Model")
	}
}

func TestUsageAllowedWhileBusy(t *testing.T) {
	if !shouldExecuteWhileBusy("/usage") {
		t.Fatalf("/usage must execute while busy (pure UI panel, no agent dependency)")
	}
}
