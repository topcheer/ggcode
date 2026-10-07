package checkpoint

// sa-91 tests: edit provenance (why-this-edit attribution). Covers intent
// stamping lifecycle (set/clear/run-reset), task-scoped queries, per-task
// grouping, narrative rendering, and legacy compatibility (zero-value intent
// changes nothing).

import (
	"strings"
	"testing"
)

func TestEditIntent_StampedOnSaveAndCleared(t *testing.T) {
	m := NewManager(100)
	m.StartRun("run-1")

	// Before any intent: legacy checkpoint, no attribution.
	cp0 := m.Save("/a.go", "old", "new", "edit_file")
	if cp0.Intent != (EditIntent{}) {
		t.Fatalf("legacy checkpoint should carry zero intent, got %+v", cp0.Intent)
	}

	m.SetActiveIntent(EditIntent{TaskID: "t-1", TaskDesc: "fix login bug"})
	cp1 := m.Save("/a.go", "new", "newer", "edit_file")
	if cp1.Intent.TaskID != "t-1" || cp1.Intent.TaskDesc != "fix login bug" {
		t.Fatalf("intent not stamped: %+v", cp1.Intent)
	}

	// Clearing stops attribution on subsequent saves.
	m.SetActiveIntent(EditIntent{})
	cp2 := m.Save("/a.go", "newer", "newest", "edit_file")
	if cp2.Intent.TaskID != "" {
		t.Fatalf("cleared intent still stamped: %+v", cp2.Intent)
	}

	// A new run resets any stale intent (does not cross runs).
	m.SetActiveIntent(EditIntent{TaskID: "t-1"})
	m.StartRun("run-2")
	cp3 := m.Save("/a.go", "x", "y", "edit_file")
	if cp3.Intent.TaskID != "" {
		t.Fatalf("intent leaked across runs: %+v", cp3.Intent)
	}
}

func TestEditsForTask_FiltersAndOrders(t *testing.T) {
	m := NewManager(100)
	m.SetActiveIntent(EditIntent{TaskID: "t-1", TaskDesc: "task one"})
	m.Save("/a.go", "1", "2", "edit_file")
	m.Save("/b.go", "1", "2", "edit_file")
	m.SetActiveIntent(EditIntent{TaskID: "t-2", TaskDesc: "task two"})
	m.Save("/c.go", "1", "2", "edit_file")
	m.SetActiveIntent(EditIntent{}) // unattributed
	m.Save("/d.go", "1", "2", "edit_file")

	got := m.EditsForTask("t-1")
	if len(got) != 2 {
		t.Fatalf("t-1 edits = %d, want 2", len(got))
	}
	if got[0].FilePath != "/a.go" || got[1].FilePath != "/b.go" {
		t.Fatalf("chronological order broken: %s, %s", got[0].FilePath, got[1].FilePath)
	}
	for _, cp := range got {
		if cp.Intent.TaskID != "t-1" {
			t.Fatalf("foreign edit in t-1 result: %+v", cp)
		}
	}
	if n := len(m.EditsForTask("t-2")); n != 1 {
		t.Fatalf("t-2 edits = %d, want 1", n)
	}
	if n := len(m.EditsForTask("missing")); n != 0 {
		t.Fatalf("unknown task returned %d edits", n)
	}
	if n := len(m.EditsForTask("")); n != 0 {
		t.Fatalf("empty taskID returned %d edits", n)
	}
}

func TestTasksWithEdits_GroupsByTask(t *testing.T) {
	m := NewManager(100)
	m.SetActiveIntent(EditIntent{TaskID: "t-1", TaskDesc: "first"})
	m.Save("/a.go", "1", "2", "edit_file")
	m.Save("/a.go", "2", "3", "edit_file") // same file twice
	m.Save("/b.go", "1", "2", "edit_file")
	m.SetActiveIntent(EditIntent{TaskID: "t-2", TaskDesc: "second"})
	m.Save("/b.go", "2", "3", "edit_file") // b.go shared across tasks

	sums := m.TasksWithEdits()
	if len(sums) != 2 {
		t.Fatalf("tasks = %d, want 2", len(sums))
	}
	s1 := sums[0]
	if s1.TaskID != "t-1" || s1.TaskDesc != "first" {
		t.Fatalf("summary[0] wrong: %+v", s1)
	}
	if s1.Edits != 3 {
		t.Fatalf("t-1 edit count = %d, want 3", s1.Edits)
	}
	if len(s1.Files) != 2 || s1.Files[0] != "/a.go" || s1.Files[1] != "/b.go" {
		t.Fatalf("t-1 files wrong (want unique, first-touch order): %v", s1.Files)
	}
	if sums[1].Edits != 1 || len(sums[1].Files) != 1 || sums[1].Files[0] != "/b.go" {
		t.Fatalf("t-2 summary wrong: %+v", sums[1])
	}
}

func TestNarrate_RendersTasksAndUnattributed(t *testing.T) {
	m := NewManager(100)
	if got := m.Narrate(); got != "" {
		t.Fatalf("empty manager should narrate empty, got %q", got)
	}

	m.SetActiveIntent(EditIntent{TaskID: "t-1", TaskDesc: "add tests"})
	m.Save("/x_test.go", "", "package", "write_file")
	m.SetActiveIntent(EditIntent{})
	m.Save("/y.go", "1", "2", "edit_file") // unattributed

	n := m.Narrate()
	for _, want := range []string{"[t-1]", "add tests", "/x_test.go", "edits: 1", "unattributed: 1"} {
		if !strings.Contains(n, want) {
			t.Errorf("Narrate missing %q in:\n%s", want, n)
		}
	}
}
