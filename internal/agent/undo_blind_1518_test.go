package agent

import "testing"

func Test1518UndoBlindCharterScenario(t *testing.T) {

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

	s2 := newUndoBlindState()
	s2.recordToolCall("git_revert", []byte(`{"revision":"abc123"}`))
	if !s2.pendingUndoFiles["*"] {
		t.Fatal("bare git_revert must set the wildcard")
	}

	s3 := newUndoBlindState()
	s3.recordToolCall("undo_edit", []byte(`{"action":"undo"}`))
	s3.recordToolCall("git_show", []byte(`{"revision":"HEAD"}`))
	if !s3.pendingUndoFiles["*"] {
		t.Fatal("pathless git_show must not clear the wildcard")
	}

	s3.recordToolCall("read_file", []byte(`{"path":"/w/a.go"}`))
	if !s3.pendingUndoFiles["*"] {
		t.Fatal("file-bearing read must not clear the wildcard (#2772)")
	}
}

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

func Test1518AssertFingerprintLineStableByDesign(t *testing.T) {
	a := uncheckedAssertInfo{line: 10, expr: "x.(string)"}
	b := uncheckedAssertInfo{line: 30, expr: "x.(string)"}
	if assertFingerprint(a) != assertFingerprint(b) {
		t.Fatal("line-shift-stability is the deliberate design (see LineShiftNotReflagged)")
	}
}
