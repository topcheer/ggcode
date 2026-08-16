package tui

// Characteristic tests for the issue #541 fixes:
//   A) lastUserSubmission producer restored in startNormalTextRun
//   C) /undo-run guarded by m.loading + removed from busy whitelist
//   D) change_summary accumulates all checkpoints, filters by RunID, fixes isNew
//   E) cycleSession handles a current session missing from the store listing
//   B) MouseWheelMsg routes to the active panel's viewport

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/session"
)

// --- Bug A: lastUserSubmission producer ---

// The producer was dropped by the e29ea0f3 refactor; restoring it means every
// normal user submission must land in m.lastUserSubmission so /retry and /edit
// stop hitting their empty branches.
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

// --- Bug C: /undo-run loading guard + busy whitelist ---

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
	// agent is nil in the test model → handler returns the "checkpoint
	// disabled" message command. Reaching that branch proves the loading
	// guard did not short-circuit.
	cmd := m.handleUndoRunCommand()
	if cmd == nil {
		t.Fatalf("handleUndoRunCommand must proceed when idle, got nil cmd")
	}
}

func TestUndoRunNotWhitelistedWhileBusy(t *testing.T) {
	if shouldExecuteWhileBusy("/undo-run") {
		t.Fatalf("/undo-run must not execute while the agent is busy (would race agent writes)")
	}
	// Adjacent whitelisted commands must keep working while busy.
	if !shouldExecuteWhileBusy("/pin") {
		t.Fatalf("/pin should still be allowed while busy")
	}
}

// --- Bug D: change_summary accounting ---

func mkCp(runID, file, old, new string) checkpoint.Checkpoint {
	return checkpoint.Checkpoint{RunID: runID, FilePath: file, OldContent: old, NewContent: new}
}

func TestAccumulateRunChangesMultipleEditsSameFile(t *testing.T) {
	cps := []checkpoint.Checkpoint{
		mkCp("r1", "a.go", "l1\nl2\nl3", "l1\nX\nl3"),
		mkCp("r1", "a.go", "l1\nX\nl3", "l1\nX\nl3\nl4"),
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
	// Net diff must span first OldContent → last NewContent: +2 (X, l4) -1 (l2).
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
	// File existed with content, was emptied in run r1, edited in run r2.
	cps := []checkpoint.Checkpoint{
		mkCp("r1", "a.go", "real content\n", ""),
		mkCp("r2", "a.go", "", "new content\n"),
	}
	runFiles := map[string]bool{"a.go": true}
	changes := accumulateRunChanges(cps, "r2", runFiles)
	if fc := changes["a.go"]; fc == nil || fc.isNew {
		t.Fatalf("emptied pre-existing file must not be reported as new: %+v", changes["a.go"])
	}
	// Contrast: a checkpoint log that never saw non-empty content → new file.
	cps2 := []checkpoint.Checkpoint{mkCp("r1", "b.go", "", "fresh\n")}
	changes2 := accumulateRunChanges(cps2, "r1", map[string]bool{"b.go": true})
	if fc := changes2["b.go"]; fc == nil || !fc.isNew {
		t.Fatalf("genuinely new file must be reported as new: %+v", changes2["b.go"])
	}
}

// --- Bug E: cycleSession with unlisted current session ---

func mkSes(id string) *session.Session {
	return &session.Session{ID: id, Title: id}
}

func TestNextSessionInCycleCurrentNotListed(t *testing.T) {
	// Fresh /clear session is absent from the store listing.
	sessions := []*session.Session{mkSes("s1"), mkSes("s2")}
	current := mkSes("fresh")

	// Forward cycle must step from the fresh session to the first listed
	// session (s1), not silently treat index 0 of the listing as current.
	got := nextSessionInCycle(sessions, current, 1)
	if got == nil || got.ID != "s1" {
		t.Fatalf("forward cycle from unlisted current: got %v, want s1", got)
	}

	// Backward cycle wraps to the last listed session.
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
	// Listing is empty but the fresh session exists: cycling is a no-op.
	if got := nextSessionInCycle(nil, mkSes("fresh"), 1); got != nil {
		t.Fatalf("expected no switch when only the current session exists, got %v", got)
	}
}

// --- Bug B: mouse wheel routes to the active panel viewport ---

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
	// Must not panic with no panel and no chatList.
	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if _, ok := updated.(Model); !ok {
		t.Fatalf("Update did not return a Model")
	}
}
