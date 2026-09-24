package checkpoint

import "testing"

// #2696: Clear() reset checkpoints/redoStack/corrections/evictedRuns but not
// fileExisted. The map records each file's FIRST checkpoint's Existed value
// (seen-only write, never updated - #1539 case D), so reusing the Manager
// after a session switch (TUI switchToSession) kept the PREVIOUS session's
// values: a pre-existing file edited in the new session was reported as
// IsNew (and vice versa), and the map grew unboundedly across sessions.
func TestIssue2696_ClearResetsFileExisted(t *testing.T) {
	m := NewManager(50)
	// Session A: /x is created fresh (write_file semantics).
	m.SaveWithExistence("/x", "", "content", "write_file", false)

	if mf := m.ModifiedFiles(); len(mf) != 1 || !mf[0].IsNew {
		t.Fatalf("session A: expected /x IsNew=true, got %+v", mf)
	}

	m.Clear()

	// Session B (same Manager, per switchToSession reuse): /x now EXISTS on
	// disk and is edited - the stale false from session A must not leak.
	m.SaveWithExistence("/x", "content", "content2", "edit_file", true)

	if mf := m.ModifiedFiles(); len(mf) != 1 {
		t.Fatalf("expected 1 modified file, got %+v", mf)
	} else if mf[0].IsNew {
		t.Fatal("post-Clear edit of a pre-existing file reported IsNew=true: stale fileExisted leaked across sessions (#2696)")
	}

	// Reverse direction: file that existed in session A but is fresh in B.
	m2 := NewManager(50)
	m2.SaveWithExistence("/y", "old", "new", "edit_file", true)
	m2.Clear()
	m2.SaveWithExistence("/y", "", "created", "write_file", false)
	if mf := m2.ModifiedFiles(); len(mf) != 1 || !mf[0].IsNew {
		t.Fatalf("fresh creation after Clear must be IsNew=true, got %+v", mf)
	}
}

// The map must actually be dropped, not just bypassed: after Clear the next
// save re-seeds from the real stat result (memory growth across sessions is
// the secondary symptom).
func TestIssue2696_ClearDropsMapEntries(t *testing.T) {
	m := NewManager(50)
	m.SaveWithExistence("/a", "", "c", "write_file", false)
	m.SaveWithExistence("/b", "", "c", "write_file", false)
	m.Clear()
	m.mu.Lock()
	leaked := len(m.fileExisted)
	m.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("fileExisted retains %d entries after Clear; unbounded cross-session growth (#2696)", leaked)
	}
}
