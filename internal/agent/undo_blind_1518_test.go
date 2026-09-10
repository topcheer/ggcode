package agent

import "testing"

// #1518: pins for all four cases.
func Test1518UndoBlindCharterScenario(t *testing.T) {
	// Case B: undo_edit (pathless args) must set the wildcard; a following
	// blind edit must fire the tree-wide warning.
	s := newUndoBlindState()
	if msg := s.recordToolCall("undo_edit", []byte(`{"action":"undo","checkpoint_id":"cp-1"}`)); msg != "" {
		t.Fatalf("undo op must not warn: %q", msg)
	}
	if !s.pendingUndoFiles["*"] {
		t.Fatal("undo_edit did not set the wildcard (charter scenario was 100% dead)")
	}
	msg := s.recordToolCall("edit_file", []byte(`{"file_path":"/w/a.go","old_text":"a","new_text":"b"}`))
	if msg == "" {
		t.Fatal("blind edit after undo_edit must warn")
	}

	// Case B2: git_revert (bare) joins the wildcard group.
	s2 := newUndoBlindState()
	s2.recordToolCall("git_revert", []byte(`{"revision":"abc123"}`))
	if !s2.pendingUndoFiles["*"] {
		t.Fatal("bare git_revert must set the wildcard")
	}

	// Case D2: an unrelated pathless read (git_show without path) must NOT
	// disarm the wildcard.
	s3 := newUndoBlindState()
	s3.recordToolCall("undo_edit", []byte(`{"action":"undo"}`))
	s3.recordToolCall("git_show", []byte(`{"revision":"HEAD"}`))
	if !s3.pendingUndoFiles["*"] {
		t.Fatal("pathless git_show must not clear the wildcard")
	}
	// But a real file-bearing read does.
	s3.recordToolCall("read_file", []byte(`{"path":"/w/a.go"}`))
	if s3.pendingUndoFiles["*"] {
		t.Fatal("file-bearing read must clear the wildcard")
	}
}

// #1518 case C: batch_replace's bare-string files[] must produce targets.
func Test1518BatchReplaceBareFiles(t *testing.T) {
	targets := projectMemoryTargetsForTool("batch_replace",
		[]byte(`{"files":["/w/internal/x/a.go","/w/internal/y/b.go"],"pattern":"a","replacement":"b"}`))
	found := map[string]bool{}
	for _, tg := range targets {
		found[tg] = true
	}
	if !found["/w/internal/x/a.go"] || !found["/w/internal/y/b.go"] {
		t.Fatalf("bare files[] not collected: %v", targets)
	}
}

// #1518 case D1: REJECTED after checking - the line-inclusion fix
// contradicts the deliberate line-shift-stability design pinned by
// TestCheckUncheckedTypeAssert_LineShiftNotReflagged. Documented as a
// trade-off at the function; this pin asserts the kept behavior so the
// decision is visible in tests.
func Test1518AssertFingerprintLineStableByDesign(t *testing.T) {
	a := uncheckedAssertInfo{line: 10, expr: "x.(string)"}
	b := uncheckedAssertInfo{line: 30, expr: "x.(string)"}
	if assertFingerprint(a) != assertFingerprint(b) {
		t.Fatal("line-shift-stability is the deliberate design (see LineShiftNotReflagged)")
	}
}
