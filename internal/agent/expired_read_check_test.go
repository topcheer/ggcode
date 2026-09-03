package agent

import (
	"strings"
	"testing"
)

func TestExpiredRead_BasicExpiryDetection(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	// Agent reads a file, then edits it -> should warn about expiry.
	s.recordRead("/project/main.go")
	hint := s.recordEdit("/project/main.go")
	if hint == "" {
		t.Fatal("expected expiry hint when editing a previously-read file")
	}
	if !strings.Contains(hint, "pre-edit read is stale") {
		t.Fatalf("hint should mention the stale pre-edit read, got: %s", hint)
	}
	// #707: the imperative "Re-read for current content" contradicted the
	// post-edit re-read detector — the message must point at the edit result,
	// never instruct a re-read.
	if strings.Contains(hint, "Re-read for current content") {
		t.Fatalf("hint must not instruct a re-read (contradicts checkPostEditReread), got: %s", hint)
	}
}

func TestExpiredRead_NoPriorRead(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	// Agent edits a file it never read -> no expiry warning (unread-edit
	// guard handles that case).
	hint := s.recordEdit("/project/new.go")
	if hint != "" {
		t.Fatalf("expected no hint for file never read, got: %s", hint)
	}
}

func TestExpiredRead_OnlyOncePerFile(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	s.recordRead("/project/a.go")
	h1 := s.recordEdit("/project/a.go")
	if h1 == "" {
		t.Fatal("expected first hint")
	}
	// Re-read and re-edit -> should NOT warn again for same file.
	s.recordRead("/project/a.go")
	h2 := s.recordEdit("/project/a.go")
	if h2 != "" {
		t.Fatalf("expected no duplicate hint, got: %s", h2)
	}
}

func TestExpiredRead_MaxWarningCap(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	count := 0
	for i := 0; i < 10; i++ {
		path := "/project/file" + string(rune('A'+i)) + ".go"
		s.recordRead(path)
		if h := s.recordEdit(path); h != "" {
			count++
		}
	}
	if count > maxExpiredReadWarnings {
		t.Fatalf("expected at most %d warnings, got %d", maxExpiredReadWarnings, count)
	}
	if count != maxExpiredReadWarnings {
		t.Fatalf("expected exactly %d warnings, got %d", maxExpiredReadWarnings, count)
	}
}

func TestExpiredRead_PostEditReread(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	// Agent edits a file (without prior read -- no expiry notice).
	s.recordEdit("/project/x.go")

	// Agent then re-reads it -> should warn about post-edit re-read.
	hint := s.checkPostEditReread("/project/x.go")
	if hint == "" {
		t.Fatal("expected post-edit re-read hint")
	}
	if !strings.Contains(hint, "edit result") {
		t.Fatalf("hint should mention edit result, got: %s", hint)
	}
}

func TestExpiredRead_PostEditRereadOnlyOnce(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	s.recordEdit("/project/x.go")
	h1 := s.checkPostEditReread("/project/x.go")
	if h1 == "" {
		t.Fatal("expected first re-read hint")
	}
	h2 := s.checkPostEditReread("/project/x.go")
	if h2 != "" {
		t.Fatalf("expected no duplicate re-read hint, got: %s", h2)
	}
}

func TestExpiredRead_PostEditRereadGapTooLarge(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	// Edit the file.
	s.recordEdit("/project/old.go")

	// Simulate many sequence ticks passing (beyond gap threshold).
	for i := 0; i < postEditRereadGap+2; i++ {
		s.seq++
	}

	hint := s.checkPostEditReread("/project/old.go")
	if hint != "" {
		t.Fatalf("expected no hint after large gap, got: %s", hint)
	}
}

func TestExpiredRead_PostEditRereadNoEdit(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	// File was never edited -> no re-read warning.
	hint := s.checkPostEditReread("/project/never.go")
	if hint != "" {
		t.Fatalf("expected no hint for unedited file, got: %s", hint)
	}
}

func TestExpiredRead_ResetClearsState(t *testing.T) {
	s := newExpiredReadState()

	s.recordRead("/project/a.go")
	s.recordEdit("/project/a.go")
	s.reset()

	// After reset, reading then editing should produce hint again.
	s.recordRead("/project/a.go")
	hint := s.recordEdit("/project/a.go")
	if hint == "" {
		t.Fatal("expected hint after reset")
	}
}

func TestExpiredRead_EmptyPath(t *testing.T) {
	s := newExpiredReadState()
	defer s.reset()

	s.recordRead("")
	hint := s.recordEdit("")
	if hint != "" {
		t.Fatalf("expected no hint for empty path, got: %s", hint)
	}
	hint = s.checkPostEditReread("")
	if hint != "" {
		t.Fatalf("expected no hint for empty path, got: %s", hint)
	}
}

// TestExpiredReadRecordUndoClearsState pins #1459-B: after undo_edit the
// rolled-back file's bookkeeping is forgotten - the anchor-rebuilding
// re-read records normally and the next edit does not misreport stale.
func TestExpiredReadRecordUndoClearsState(t *testing.T) {
	e := newExpiredReadState()
	e.recordRead("/repo/a.go")
	if hint := e.recordEdit("/repo/a.go"); hint == "" {
		t.Fatal("expected expiry hint on first edit after read")
	}
	// Undo: state forgets the file...
	e.recordUndo("/repo/a.go")
	// ...so the anchor-rebuilding re-read is tracked as a FRESH read
	// (readBeforeEdit set again, no lingering editedFiles guard).
	e.recordRead("/repo/a.go")
	if !e.readBeforeEdit["/repo/a.go"] {
		t.Fatal("post-undo re-read dropped by lingering editedFiles guard")
	}
	if _, still := e.editedFiles["/repo/a.go"]; still {
		t.Fatal("editedFiles not cleared by recordUndo")
	}
}
